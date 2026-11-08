// 跨渠道比价纯函数测试（docs/specs/rate-compare.md AC-1 / AC-2）。
// buildRateCompare 不碰数据库：输入快照 + 渠道元信息，输出两个视图。
package monitor

import (
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

var compareBase = time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)

func TestNormalizeGroupName(t *testing.T) {
	cases := []struct{ in, want string }{
		{" default", "default"},
		{"Default ", "default"},
		{"VIP", "vip"},
		{"  ", ""},
		{"默认组", "默认组"},
	}
	for _, c := range cases {
		if got := normalizeGroupName(c.in); got != c.want {
			t.Errorf("normalizeGroupName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestBuildRateCompareChannelsView(t *testing.T) {
	channels := []storage.Channel{
		{ID: 1, Name: "alpha", Type: storage.ChannelTypeNewAPI},
		{ID: 2, Name: "beta", Type: storage.ChannelTypeSub2API},
		{ID: 3, Name: "gamma", Type: storage.ChannelTypeNewAPI}, // 无快照
	}
	snaps := []storage.RateSnapshot{
		{ChannelID: 1, ModelName: "default", Ratio: 1.0, LastSeenAt: compareBase},
		{ChannelID: 1, ModelName: "vip", Ratio: 0.8, LastSeenAt: compareBase.Add(time.Hour)},
		{ChannelID: 2, ModelName: "pro", Ratio: 1.2, LastSeenAt: compareBase.Add(-time.Hour)},
		{ChannelID: 99, ModelName: "default", Ratio: 0.1, LastSeenAt: compareBase}, // 渠道已删的残留
	}

	got := buildRateCompare(snaps, channels, 2)
	if len(got.Channels) != 2 {
		t.Fatalf("channels 长度 = %d, want 2（gamma 无快照、99 为孤儿应缺席）: %+v", len(got.Channels), got.Channels)
	}

	a := got.Channels[0]
	if a.ChannelID != 1 || a.ChannelName != "alpha" || a.ChannelType != storage.ChannelTypeNewAPI {
		t.Errorf("第 1 名 = %+v, want alpha/newapi（0.8 最低）", a)
	}
	if a.MinRatio != 0.8 || a.MinGroup != "vip" || a.GroupCount != 2 {
		t.Errorf("alpha = %+v, want min 0.8 @vip, 2 组", a)
	}
	if !a.LastSeenAt.Equal(compareBase.Add(time.Hour)) {
		t.Errorf("alpha LastSeenAt = %v, want 渠道内最新快照时间", a.LastSeenAt)
	}

	b := got.Channels[1]
	if b.ChannelID != 2 || b.MinRatio != 1.2 || b.MinGroup != "pro" || b.GroupCount != 1 {
		t.Errorf("第 2 名 = %+v, want beta min 1.2 @pro, 1 组", b)
	}

	// 视图 ②：default 出现在 alpha（含孤儿不算）→ 1 渠道 < 2，过滤。
	if len(got.Groups) != 0 {
		t.Errorf("groups 长度 = %d, want 0（default 仅 alpha 一家，pro 也仅 beta）", len(got.Groups))
	}
}

func TestBuildRateCompareChannelsViewTieBreak(t *testing.T) {
	channels := []storage.Channel{
		{ID: 1, Name: "delta", Type: storage.ChannelTypeNewAPI},
		{ID: 2, Name: "alpha", Type: storage.ChannelTypeSub2API},
	}
	snaps := []storage.RateSnapshot{
		{ChannelID: 1, ModelName: "default", Ratio: 0.8, LastSeenAt: compareBase},
		{ChannelID: 2, ModelName: "default", Ratio: 0.8, LastSeenAt: compareBase},
	}
	got := buildRateCompare(snaps, channels, 2)
	if got.Channels[0].ChannelName != "alpha" || got.Channels[1].ChannelName != "delta" {
		t.Errorf("并列 0.8 应按渠道名稳定序，got %s < %s",
			got.Channels[0].ChannelName, got.Channels[1].ChannelName)
	}
}

func TestBuildRateCompareGroupsView(t *testing.T) {
	channels := []storage.Channel{
		{ID: 1, Name: "alpha", Type: storage.ChannelTypeNewAPI},
		{ID: 2, Name: "beta", Type: storage.ChannelTypeSub2API},
		{ID: 3, Name: "gamma", Type: storage.ChannelTypeNewAPI},
	}
	snaps := []storage.RateSnapshot{
		{ChannelID: 1, ModelName: "Default", Ratio: 1.0, LastSeenAt: compareBase},
		{ChannelID: 2, ModelName: "default", Description: "基础分组", Ratio: 1.2, LastSeenAt: compareBase},
		{ChannelID: 1, ModelName: "vip", Ratio: 0.9, LastSeenAt: compareBase}, // 仅 alpha → 过滤
		{ChannelID: 2, ModelName: "pro", Ratio: 0.5, LastSeenAt: compareBase},
		{ChannelID: 3, ModelName: "pro", Ratio: 0.5, LastSeenAt: compareBase},
	}

	got := buildRateCompare(snaps, channels, 2)
	if len(got.Groups) != 2 {
		t.Fatalf("groups 长度 = %d, want 2（default / pro）: %+v", len(got.Groups), got.Groups)
	}

	// 两个分组条目数同为 2 → 归一化名升序：default < pro。
	d, p := got.Groups[0], got.Groups[1]
	if d.ModelName != "Default" || p.ModelName != "pro" {
		t.Errorf("分组序 = [%s, %s], want [Default, pro]（归一化名升序）", d.ModelName, p.ModelName)
	}

	// default 组：展示名取排序首条目（最便宜 alpha）的原始名 "Default"；
	// description 取条目里第一个非空；条目按 ratio 升序；best = alpha；spread 20.0%。
	if d.Description != "基础分组" {
		t.Errorf("default description = %q, want 基础分组（首条目空、取第二个非空）", d.Description)
	}
	if len(d.Entries) != 2 || d.Entries[0].ChannelName != "alpha" || d.Entries[1].ChannelName != "beta" {
		t.Errorf("default entries = %+v, want alpha(1.0) → beta(1.2)", d.Entries)
	}
	if d.Entries[0].Ratio != 1.0 || d.Entries[0].ChannelType != storage.ChannelTypeNewAPI {
		t.Errorf("default 首条目 = %+v, want alpha/newapi/1.0", d.Entries[0])
	}
	if d.BestChannelID != 1 {
		t.Errorf("default best_channel_id = %d, want 1", d.BestChannelID)
	}
	if d.SpreadPct != 20.0 {
		t.Errorf("default spread_pct = %v, want 20.0", d.SpreadPct)
	}

	// pro 组：ratio 并列 0.5 → 渠道名稳定序；spread 0。
	if p.Entries[0].ChannelName != "beta" || p.Entries[1].ChannelName != "gamma" {
		t.Errorf("pro entries 并列应按渠道名稳定序, got %s → %s",
			p.Entries[0].ChannelName, p.Entries[1].ChannelName)
	}
	if p.BestChannelID != 2 || p.SpreadPct != 0 {
		t.Errorf("pro = best %d spread %v, want best 2 spread 0", p.BestChannelID, p.SpreadPct)
	}

	// min_channels=1：vip 组保留（单条目、spread 0）。
	got1 := buildRateCompare(snaps, channels, 1)
	if len(got1.Groups) != 3 {
		t.Fatalf("min_channels=1 时 groups 长度 = %d, want 3（vip 保留）", len(got1.Groups))
	}
	found := false
	for _, g := range got1.Groups {
		if g.ModelName == "vip" {
			found = true
			if len(g.Entries) != 1 || g.SpreadPct != 0 || g.BestChannelID != 1 {
				t.Errorf("vip = %+v, want 单条目 / spread 0 / best 1", g)
			}
		}
	}
	if !found {
		t.Error("min_channels=1 时未见 vip 组")
	}
}

func TestBuildRateCompareFreeGroupSpread(t *testing.T) {
	channels := []storage.Channel{
		{ID: 1, Name: "alpha", Type: storage.ChannelTypeNewAPI},
		{ID: 2, Name: "beta", Type: storage.ChannelTypeSub2API},
	}
	snaps := []storage.RateSnapshot{
		{ChannelID: 1, ModelName: "free", Ratio: 0, LastSeenAt: compareBase},
		{ChannelID: 2, ModelName: "free", Ratio: 0.5, LastSeenAt: compareBase},
	}
	got := buildRateCompare(snaps, channels, 2)
	if len(got.Groups) != 1 {
		t.Fatalf("groups 长度 = %d, want 1", len(got.Groups))
	}
	if got.Groups[0].SpreadPct != 0 {
		t.Errorf("免费分组（min=0）spread 应无定义取 0, got %v", got.Groups[0].SpreadPct)
	}
	if got.Groups[0].BestChannelID != 1 {
		t.Errorf("best = %d, want 1（0 最低）", got.Groups[0].BestChannelID)
	}
}

func TestBuildRateCompareEmpty(t *testing.T) {
	channels := []storage.Channel{{ID: 1, Name: "alpha", Type: storage.ChannelTypeNewAPI}}
	got := buildRateCompare(nil, channels, 2)
	if len(got.Channels) != 0 || len(got.Groups) != 0 {
		t.Errorf("空输入应得空结果, got %+v", got)
	}
}
