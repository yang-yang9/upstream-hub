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
	}, true
}
