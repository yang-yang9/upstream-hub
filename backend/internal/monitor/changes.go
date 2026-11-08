// changes.go 抽出变化检测的纯函数，使核心判定逻辑可以脱离 DB / Connector 单测。
// 语义与原先 monitor/service.go 内联实现逐字一致，对应 spec：docs/specs/monitor-rate-scanning.md。
package monitor

import (
	"fmt"
	"time"

	"github.com/worryzyy/upstream-hub/internal/notify"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// rateChangeDiff 比较新旧倍率快照，产出变化日志与通知投影。
// prev == nil（分组首见）或 ratio / completion 双值均未变时 changed = false，
// 此时返回零值，调用方无需写日志或推送。
func rateChangeDiff(
	c *storage.Channel,
	modelName string,
	prev *storage.RateSnapshot,
	ratio, comp float64,
	now time.Time,
) (*storage.RateChangeLog, notify.RateChange, bool) {
	if prev == nil {
		return nil, notify.RateChange{}, false
	}
	if prev.Ratio == ratio && prev.CompletionRatio == comp {
		return nil, notify.RateChange{}, false
	}
	oldRatio := prev.Ratio
	oldComp := prev.CompletionRatio
	log := &storage.RateChangeLog{
		ChannelID:          c.ID,
		ModelName:          modelName,
		OldRatio:           &oldRatio,
		NewRatio:           ratio,
		OldCompletionRatio: &oldComp,
		NewCompletionRatio: comp,
		ChangedAt:          now,
	}
	return log, notify.RateChange{
		GroupName: modelName,
		OldRatio:  oldRatio,
		NewRatio:  ratio,
		OldComp:   oldComp,
		NewComp:   comp,
		ChangedAt: now,
	}, true
}

// cooldownOverrides fail-open 解析渠道的 per-channel 冷却覆盖（P1-6）。
// 脏数据（历史遗留 / 手改库）按无覆盖处理，回落全局 Policy，不因它丢告警。
func cooldownOverrides(c *storage.Channel) notify.CooldownOverrides {
	ov, err := notify.ParseCooldownOverrides(c.NotifyCooldowns)
	if err != nil {
		return nil
	}
	return ov
}

// balanceLowMessage 判断余额是否低于渠道阈值并构造告警消息。
// 阈值 <= 0（未配置）或余额 >= 阈值（严格小于语义）时不触发。
func balanceLowMessage(c *storage.Channel, balance float64) (notify.Message, bool) {
	if c.BalanceThreshold <= 0 || balance >= c.BalanceThreshold {
		return notify.Message{}, false
	}
	return notify.Message{
		Event:     storage.EventBalanceLow,
		ChannelID: c.ID,
		Subject:   fmt.Sprintf("[upstream-hub] %s 余额低于阈值", c.Name),
		Body:      fmt.Sprintf("当前余额: %.4f，阈值: %.4f", balance, c.BalanceThreshold),
		Cooldown:  cooldownOverrides(c),
	}, true
}

// balancePlan 一次余额扫描后的低余额状态迁移计划（P0-3，docs/specs/balance-recovery.md）。
type balancePlan struct {
	message  notify.Message // 恢复消息；send=false 时零值
	send     bool           // 是否派发恢复通知
	low      bool           // 本次扫描后的目标状态（balance_low_active）
	changed  bool           // 目标状态与当前值不同，需持久化
}

// balanceStatePlan 依据本次扫描余额决定低余额状态迁移与恢复通知。
// 标记 / 清除始终维护（与 balanceLowMessage 的严格小于语义对称：balance >= threshold
// 即视为已恢复）；recoveryEnabled 只控制恢复通知的派发（默认关，spec AC-1）。
// 阈值 <= 0（未配置 / 已删除）时静默清除既有标记——"恢复"无从界定，不发通知。
func balanceStatePlan(c *storage.Channel, balance float64, recoveryEnabled bool) balancePlan {
	// 低于阈值：标记（告警是否被冷却 / 静默压掉不影响状态，与 P1-4 streak 语义一致）。
	if c.BalanceThreshold > 0 && balance < c.BalanceThreshold {
		return balancePlan{low: true, changed: !c.BalanceLowActive}
	}
	// 回升（或阈值被删）：只在曾标记过时迁移。
	if !c.BalanceLowActive {
		return balancePlan{}
	}
	p := balancePlan{low: false, changed: true}
	if c.BalanceThreshold > 0 && recoveryEnabled {
		p.message = notify.Message{
			Event:     storage.EventBalanceRecovered,
			ChannelID: c.ID,
			Subject:   fmt.Sprintf("[upstream-hub] %s 余额已恢复", c.Name),
			Body:      fmt.Sprintf("当前余额: %.4f，阈值: %.4f", balance, c.BalanceThreshold),
			// 恢复事件不参与冷却，但照常携带覆盖（Dispatcher 对非冷却事件忽略它）。
			Cooldown: cooldownOverrides(c),
		}
		p.send = true
	}
	return p
}
