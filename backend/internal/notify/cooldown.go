package notify

import (
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
)

// CooldownOverrides per-channel 冷却覆盖（P1-6）。
// 键限 4 类参与冷却的事件（见 cooldownEventKeys），值 = 冷却时长；
// 0 = 显式关闭该事件的冷却（每次都发）。nil / 空 = 不覆盖，跟随全局 Policy。
type CooldownOverrides map[storage.NotificationEvent]time.Duration

// cooldownEventKeys notify_cooldowns JSON 允许的键（事件名 → 事件）。
var cooldownEventKeys = map[string]storage.NotificationEvent{
	"balance_low":          storage.EventBalanceLow,
	"balance_forecast_low": storage.EventBalanceForecast,
	"login_failed":         storage.EventLoginFailed,
	"monitor_failed":       storage.EventMonitorFailed,
}

// ParseCooldownOverrides 解析渠道 notify_cooldowns 字段的 JSON 覆盖配置。
//
// 值的单位是分钟（整数 >= 0）。空串 / "{}" 返回空 map（不覆盖）。
// 非法 JSON / 未知键 / 负数 / 小数 / 非数字值都是错误（API 侧转 400）；
// 运行时遇到 DB 脏数据由调用方 fail-open（忽略错误、按无覆盖处理）。
func ParseCooldownOverrides(raw string) (CooldownOverrides, error) {
	if raw == "" {
		return CooldownOverrides{}, nil
	}
	var rawMap map[string]any
	if err := json.Unmarshal([]byte(raw), &rawMap); err != nil {
		return nil, fmt.Errorf("notify cooldowns %q: %w", raw, err)
	}
	out := CooldownOverrides{}
	for k, v := range rawMap {
		ev, ok := cooldownEventKeys[k]
		if !ok {
			return nil, fmt.Errorf("notify cooldowns: unknown event key %q（允许：balance_low / balance_forecast_low / login_failed / monitor_failed）", k)
		}
		f, ok := v.(float64)
		if !ok {
			return nil, fmt.Errorf("notify cooldowns: %q 的值必须是分钟数（数字）", k)
		}
		if f < 0 || f != math.Trunc(f) {
			return nil, fmt.Errorf("notify cooldowns: %q 的值必须是 >= 0 的整数分钟", k)
		}
		out[ev] = time.Duration(f) * time.Minute
	}
	return out, nil
}
