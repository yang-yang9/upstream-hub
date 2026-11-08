// 跨渠道比价聚合（docs/specs/rate-compare.md）。纯函数，不碰数据库：
// 输入全渠道快照 + 渠道元信息，输出零配置双视图——
//   - 视图 ① 整体倍率排行：每渠道最低 ratio，全渠道可比，无分组名对齐问题；
//   - 视图 ② 同名分组比价：normalizeGroupName 归一化后字面匹配的分组内跨渠道对比。
//
// 分组名跨站没有可靠自动对齐（用户侧接口拿不到分组→模型映射），
// 自造命名的分组不硬凑；completion_ratio 当前恒为 0，不参与排序。
package monitor

import (
	"math"
	"sort"
	"strings"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// ChannelRateRank 视图 ① 整体倍率排行的一条渠道。
type ChannelRateRank struct {
	ChannelID   uint                `json:"channel_id"`
	ChannelName string              `json:"channel_name"`
	ChannelType storage.ChannelType `json:"channel_type"`
	MinRatio    float64             `json:"min_ratio"`
	MinGroup    string              `json:"min_group"`
	GroupCount  int                 `json:"group_count"`
	LastSeenAt  time.Time           `json:"last_seen_at"`
}

// RateCompareEntry 视图 ② 某分组下一条渠道报价。
type RateCompareEntry struct {
	ChannelID   uint                `json:"channel_id"`
	ChannelName string              `json:"channel_name"`
	ChannelType storage.ChannelType `json:"channel_type"`
	Ratio       float64             `json:"ratio"`
	LastSeenAt  time.Time           `json:"last_seen_at"`
}

// RateCompareGroup 视图 ② 的一个同名分组（归一化聚合）。
type RateCompareGroup struct {
	ModelName     string             `json:"model_name"`
	Description   string             `json:"description,omitempty"`
	Entries       []RateCompareEntry `json:"entries"`
	BestChannelID uint               `json:"best_channel_id"`
	// SpreadPct 组内 (最高−最低)/最低 × 100，四舍五入 1 位小数；最低为 0（免费分组）时无定义取 0。
	SpreadPct float64 `json:"spread_pct"`
}

// RateCompareResult 双视图聚合结果。
type RateCompareResult struct {
	Channels []ChannelRateRank  `json:"channels"`
	Groups   []RateCompareGroup `json:"groups"`
}

// normalizeGroupName 分组名归一化：TrimSpace + ToLower。
func normalizeGroupName(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// buildRateCompare 聚合双视图。channels 提供 id → name/type 元信息，
// 快照的 channel_id 对不上（渠道已删的残留）时该快照丢弃。
func buildRateCompare(snapshots []storage.RateSnapshot, channels []storage.Channel, minChannels int) RateCompareResult {
	meta := make(map[uint]storage.Channel, len(channels))
	for _, c := range channels {
		meta[c.ID] = c
	}

	// 视图 ①：按渠道聚合，min_ratio / min_group / group_count / last_seen_at。
	type chanAgg struct {
		rank     ChannelRateRank
		lastSeen time.Time
	}
	chanByID := map[uint]*chanAgg{}
	for _, s := range snapshots {
		ch, ok := meta[s.ChannelID]
		if !ok {
			continue
		}
		agg, exists := chanByID[s.ChannelID]
		if !exists {
			agg = &chanAgg{rank: ChannelRateRank{
				ChannelID:   ch.ID,
				ChannelName: ch.Name,
				ChannelType: ch.Type,
				MinRatio:    s.Ratio,
				MinGroup:    s.ModelName,
			}}
			chanByID[s.ChannelID] = agg
		} else if s.Ratio < agg.rank.MinRatio {
			agg.rank.MinRatio = s.Ratio
			agg.rank.MinGroup = s.ModelName
		}
		agg.rank.GroupCount++
		if s.LastSeenAt.After(agg.lastSeen) {
			agg.lastSeen = s.LastSeenAt
		}
	}
	result := RateCompareResult{Channels: make([]ChannelRateRank, 0, len(chanByID))}
	for _, agg := range chanByID {
		agg.rank.LastSeenAt = agg.lastSeen
		result.Channels = append(result.Channels, agg.rank)
	}
	sort.Slice(result.Channels, func(i, j int) bool {
		if result.Channels[i].MinRatio != result.Channels[j].MinRatio {
			return result.Channels[i].MinRatio < result.Channels[j].MinRatio
		}
		return result.Channels[i].ChannelName < result.Channels[j].ChannelName
	})

	// 视图 ②：按归一化分组名聚合。raw / desc 跟随条目，排序后用于确定展示名与描述。
	type entryWithMeta struct {
		entry RateCompareEntry
		raw   string // 该条快照的原始分组名（未归一化）
		desc  string // 该条快照的描述
	}
	groups := map[string][]entryWithMeta{}
	for _, s := range snapshots {
		ch, ok := meta[s.ChannelID]
		if !ok {
			continue
		}
		key := normalizeGroupName(s.ModelName)
		groups[key] = append(groups[key], entryWithMeta{
			entry: RateCompareEntry{
				ChannelID:   ch.ID,
				ChannelName: ch.Name,
				ChannelType: ch.Type,
				Ratio:       s.Ratio,
				LastSeenAt:  s.LastSeenAt,
			},
			raw:  s.ModelName,
			desc: s.Description,
		})
	}

	type groupWithKey struct {
		key string
		g   RateCompareGroup
	}
	var list []groupWithKey
	for key, entries := range groups {
		if len(entries) < minChannels {
			continue
		}
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].entry.Ratio != entries[j].entry.Ratio {
				return entries[i].entry.Ratio < entries[j].entry.Ratio
			}
			return entries[i].entry.ChannelName < entries[j].entry.ChannelName
		})
		g := RateCompareGroup{
			ModelName:     entries[0].raw, // 展示名取排序首条目（最便宜渠道）的原始名
			BestChannelID: entries[0].entry.ChannelID,
		}
		for _, e := range entries {
			g.Entries = append(g.Entries, e.entry)
			if g.Description == "" {
				g.Description = e.desc
			}
		}
		g.SpreadPct = spreadPct(g.Entries)
		list = append(list, groupWithKey{key: key, g: g})
	}
	sort.Slice(list, func(i, j int) bool {
		if len(list[i].g.Entries) != len(list[j].g.Entries) {
			return len(list[i].g.Entries) > len(list[j].g.Entries)
		}
		return list[i].key < list[j].key
	})
	result.Groups = make([]RateCompareGroup, 0, len(list))
	for _, g := range list {
		result.Groups = append(result.Groups, g.g)
	}
	return result
}

// spreadPct 组内价差：(最高−最低)/最低 × 100，四舍五入 1 位小数。
// 最低为 0（免费分组）时价差无定义，取 0。entries 由调用方保证非空。
func spreadPct(entries []RateCompareEntry) float64 {
	lo, hi := entries[0].Ratio, entries[0].Ratio
	for _, e := range entries {
		if e.Ratio < lo {
			lo = e.Ratio
		}
		if e.Ratio > hi {
			hi = e.Ratio
		}
	}
	if lo <= 0 {
		return 0
	}
	return math.Round((hi-lo)/lo*1000) / 10
}
