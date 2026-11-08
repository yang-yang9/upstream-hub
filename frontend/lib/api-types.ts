/**
 * API response shapes for upstream-hub backend.
 * Keep in sync with backend/internal/storage/*.go and backend/internal/api/*.go.
 */

export type ChannelType = "newapi" | "sub2api"

export type CredentialMode = "password" | "token"

export type NotificationChannelType =
  | "telegram"
  | "webhook"
  | "email"
  | "wecom"
  | "dingtalk"
  | "feishu"
  | "bark"

export type CaptchaProviderType =
  | "capsolver"
  | "2captcha"
  | "anticaptcha"
  | "yescaptcha"

export type MonitorJob = "login" | "refresh" | "balance" | "rates"

export type NotificationEvent =
  | "balance_low"
  | "balance_forecast_low"
  | "rate_changed"
  | "login_failed"
  | "captcha_failed"
  | "monitor_failed"
  | "login_recovered"
  | "monitor_recovered"

export interface Channel {
  id: number
  name: string
  type: ChannelType
  site_url: string
  username: string
  credential_mode: CredentialMode
  turnstile_enabled: boolean
  captcha_config_id?: number | null
  proxy?: string
  balance_threshold: number
  monitor_enabled: boolean
  /** per-channel 冷却覆盖 JSON（P1-6）：{"balance_low":10,...}，空 = 跟随全局 */
  notify_cooldowns?: string
  last_balance?: number | null
  last_balance_at?: string | null
  last_error?: string
  created_at: string
  updated_at: string
}

export interface CaptchaConfig {
  id: number
  name: string
  type: CaptchaProviderType
  endpoint?: string
  extra?: string
  enabled: boolean
  created_at: string
  updated_at: string
}

export interface RateSnapshot {
  id: number
  channel_id: number
  model_name: string
  description?: string
  ratio: number
  completion_ratio: number
  first_seen_at: string
  last_seen_at: string
}

export interface RateChangeLog {
  id: number
  channel_id: number
  model_name: string
  old_ratio: number | null
  new_ratio: number
  old_completion_ratio?: number | null
  new_completion_ratio?: number
  changed_at: string
}

export interface BalanceSnapshot {
  id: number
  channel_id: number
  balance: number
  sampled_at: string
}

export interface NotificationSubscription {
  channel_id: number
  mode: "all" | "groups"
  groups?: string[]
}

export interface NotificationChannel {
  id: number
  name: string
  type: NotificationChannelType
  enabled: boolean
  subscriptions?: string
  created_at: string
  updated_at: string
}

export interface NotificationLog {
  id: number
  channel_id: number
  event: NotificationEvent
  subject: string
  body: string
  success: boolean
  error_message?: string
  sent_at: string
}

export interface MonitorLog {
  id: number
  channel_id: number
  job: MonitorJob
  success: boolean
  error_message?: string
  duration_ms: number
  started_at: string
  finished_at: string
}

export interface DashboardLowest {
  channel_id: number
  name: string
  balance: number | null
}

export interface DashboardChannelStat {
  id: number
  name: string
  type: string
  monitor_enabled: boolean
  last_balance?: number | null
  last_error?: string
}

export interface DashboardSummary {
  total_channels: number
  active_channels: number
  failed_channels: number
  total_balance: number
  lowest_balance: DashboardLowest | null
  channels: DashboardChannelStat[]
  recent_rate_changes: RateChangeLog[]
  recent_notification_logs: NotificationLog[]
}

export interface BalanceTrendPoint {
  day: string
  balance: number
}

/** 余额消耗预测（GET /channels/:id/forecast）。对应 spec：docs/specs/usage-forecast.md。 */
export interface ChannelForecast {
  channel_id: number
  daily_burn: number
  latest: number
  days_left: number
  depleted_at: string | null
  samples: number
  window_days: number
  depleting: boolean
}

/** 跨渠道比价（GET /rate-compare）。对应 spec：docs/specs/rate-compare.md。 */
export interface ChannelRateRank {
  channel_id: number
  channel_name: string
  channel_type: ChannelType
  min_ratio: number
  min_group: string
  group_count: number
  last_seen_at: string
}

export interface RateCompareEntry {
  channel_id: number
  channel_name: string
  channel_type: ChannelType
  ratio: number
  last_seen_at: string
}

export interface RateCompareGroup {
  model_name: string
  description?: string
  entries: RateCompareEntry[]
  best_channel_id: number
  spread_pct: number
}

export interface RateCompareResult {
  channels: ChannelRateRank[]
  groups: RateCompareGroup[]
}

export interface UpgradeCheckResult {
  current_version: string
  latest_version: string
  has_update: boolean
  changelog: string
  published_at: string
  html_url: string
  warning?: string
}
