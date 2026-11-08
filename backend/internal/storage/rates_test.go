// storage 集成测试（外部包 storage_test，规避与 storagetest 的导入环）。
// 运行：UPSTREAMHUB_TEST_DB=1，DSN 见 docs/development.md（需带 TimeZone=UTC）。
// 时间种子全部显式 UTC，聚合断言按 SQL 的 date_trunc 语义在 Go 里复算期望值，
// 避免本地时区差异导致 flaky。
package storage_test

import (
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/storage"
	"github.com/worryzyy/upstream-hub/internal/storage/storagetest"
)

func TestRatesUpsertPrevContract(t *testing.T) {
	r := storagetest.Rates(t)
	now := time.Now().UTC().Truncate(time.Microsecond) // PostgreSQL 只存微秒，种子截断避免 .Equal 挂在纳秒上

	// 首插：prev 为 nil，FirstSeenAt 取 LastSeenAt。
	snap := &storage.RateSnapshot{
		ChannelID: 1, ModelName: "g1", Description: "d",
		Ratio: 2, CompletionRatio: 3, LastSeenAt: now,
	}
	prev, err := r.Upsert(snap)
	if err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	if prev != nil {
		t.Fatalf("first upsert prev = %+v, want nil", prev)
	}
	if !snap.FirstSeenAt.Equal(now) {
		t.Errorf("FirstSeenAt = %v, want %v", snap.FirstSeenAt, now)
	}

	// 更新：prev 返回旧值（monitor 依赖此契约判断是否变化）。
	prev, err = r.Upsert(&storage.RateSnapshot{
		ChannelID: 1, ModelName: "g1", Description: "d2",
		Ratio: 2.5, CompletionRatio: 3.5, LastSeenAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("second upsert: %v", err)
	}
	if prev == nil {
		t.Fatal("update upsert prev = nil, want old snapshot")
	}
	if prev.Ratio != 2 || prev.CompletionRatio != 3 || prev.Description != "d" {
		t.Errorf("prev = %+v, want old values (2/3/d)", prev)
	}

	// 当前行已是新值。
	list, err := r.ListByChannel(1)
	if err != nil || len(list) != 1 {
		t.Fatalf("ListByChannel = %v, %v", list, err)
	}
	if list[0].Ratio != 2.5 || list[0].CompletionRatio != 3.5 || list[0].Description != "d2" {
		t.Errorf("current row = %+v, want updated values", list[0])
	}

	// 不同 model_name 是独立快照。
	prev, err = r.Upsert(&storage.RateSnapshot{
		ChannelID: 1, ModelName: "g2", Ratio: 1, LastSeenAt: now,
	})
	if err != nil || prev != nil {
		t.Errorf("other model upsert = %v, %v, want nil prev", prev, err)
	}
	// ListByChannel 按 model_name 升序。
	list, _ = r.ListByChannel(1)
	if len(list) != 2 || list[0].ModelName != "g1" || list[1].ModelName != "g2" {
		t.Errorf("ListByChannel order = %v", list)
	}
}

func TestRatesChanges(t *testing.T) {
	r := storagetest.Rates(t)
	now := time.Now().UTC().Truncate(time.Microsecond) // PostgreSQL 只存微秒，种子截断避免 .Equal 挂在纳秒上

	// ChangedAt 为零时自动填 now。
	if err := r.AppendChange(&storage.RateChangeLog{ChannelID: 1, ModelName: "g1", NewRatio: 2}); err != nil {
		t.Fatalf("AppendChange: %v", err)
	}
	// 显式时间。
	older := now.Add(-2 * time.Hour)
	if err := r.AppendChange(&storage.RateChangeLog{
		ChannelID: 2, ModelName: "g2", NewRatio: 3, ChangedAt: older,
	}); err != nil {
		t.Fatalf("AppendChange: %v", err)
	}

	// 倒序；channelID 过滤；limit 生效。
	all, err := r.ListChanges(0, 0)
	if err != nil || len(all) != 2 {
		t.Fatalf("ListChanges = %v, %v", all, err)
	}
	if !all[0].ChangedAt.After(all[1].ChangedAt) {
		t.Errorf("ListChanges not desc: %v", all)
	}
	onlyCh2, err := r.ListChanges(2, 0)
	if err != nil || len(onlyCh2) != 1 || onlyCh2[0].ChannelID != 2 {
		t.Errorf("ListChanges(2) = %v, %v", onlyCh2, err)
	}
	if !onlyCh2[0].ChangedAt.Equal(older) {
		t.Errorf("explicit ChangedAt = %v, want %v", onlyCh2[0].ChangedAt, older)
	}
}

func TestRatesBalanceHistoryAndRetention(t *testing.T) {
	r := storagetest.Rates(t)
	now := time.Now().UTC().Truncate(time.Microsecond) // PostgreSQL 只存微秒，种子截断避免 .Equal 挂在纳秒上

	seed := []storage.BalanceSnapshot{
		{ChannelID: 1, Balance: 10, SampledAt: now.Add(-3 * time.Hour)},
		{ChannelID: 1, Balance: 11, SampledAt: now.Add(-1 * time.Hour)},
		{ChannelID: 2, Balance: 5, SampledAt: now.Add(-2 * time.Hour)},
		{ChannelID: 2, Balance: 100, SampledAt: now.Add(-72 * time.Hour)},
	}
	for i := range seed {
		if err := r.AppendBalance(&seed[i]); err != nil {
			t.Fatalf("AppendBalance: %v", err)
		}
	}

	hist, err := r.BalanceHistory(1, 10)
	if err != nil || len(hist) != 2 {
		t.Fatalf("BalanceHistory = %v, %v", hist, err)
	}
	if hist[0].Balance != 11 || hist[1].Balance != 10 {
		t.Errorf("BalanceHistory not desc by sampled_at: %v", hist)
	}

	// 严格小于 cutoff：-2h 的 5 被删，-1h 的 11 保留。
	cutoff := now.Add(-90 * time.Minute)
	n, err := r.DeleteBalanceSnapshotsBefore(cutoff)
	if err != nil {
		t.Fatalf("DeleteBalanceSnapshotsBefore: %v", err)
	}
	if n != 3 { // -3h(10)、-2h(5)、-72h(100) 都早于 cutoff
		t.Errorf("deleted rows = %d, want 3", n)
	}
	hist, _ = r.BalanceHistory(1, 10)
	if len(hist) != 1 || hist[0].Balance != 11 {
		t.Errorf("after retention BalanceHistory = %v", hist)
	}
}

// bucketExpected 在 Go 里复算 date_trunc + 每 (channel, bucket) 取最后一条 + 按 bucket 求和，
// 与 AggregateBalanceTrend / Hourly 的 SQL 语义一致，作为期望值。
func bucketExpected(t *testing.T, seeds []storage.BalanceSnapshot, trunc time.Duration) map[time.Time]float64 {
	t.Helper()
	last := map[time.Time]map[uint]storage.BalanceSnapshot{}
	for _, s := range seeds {
		b := s.SampledAt.Truncate(trunc)
		if last[b] == nil {
			last[b] = map[uint]storage.BalanceSnapshot{}
		}
		if cur, ok := last[b][s.ChannelID]; !ok || s.SampledAt.After(cur.SampledAt) {
			last[b][s.ChannelID] = s
		}
	}
	out := map[time.Time]float64{}
	for b, chans := range last {
		sum := 0.0
		for _, s := range chans {
			sum += s.Balance
		}
		out[b] = sum
	}
	return out
}

func TestAggregateBalanceTrendDaily(t *testing.T) {
	r := storagetest.Rates(t)
	now := time.Now().UTC().Truncate(time.Microsecond) // PostgreSQL 只存微秒，种子截断避免 .Equal 挂在纳秒上

	seeds := []storage.BalanceSnapshot{
		// 渠道 1 今天两条：日内取最后一条 11。
		{ChannelID: 1, Balance: 10, SampledAt: now.Add(-5 * time.Hour)},
		{ChannelID: 1, Balance: 11, SampledAt: now.Add(-3 * time.Hour)},
		// 渠道 2 今天一条：5。
		{ChannelID: 2, Balance: 5, SampledAt: now.Add(-2 * time.Hour)},
		// 渠道 1 昨天：100（独立日桶）。
		{ChannelID: 1, Balance: 100, SampledAt: now.Add(-26 * time.Hour)},
		// 渠道 2 十天前：超出 7 天窗口，不参与。
		{ChannelID: 2, Balance: 999, SampledAt: now.Add(-10 * 24 * time.Hour)},
	}
	for i := range seeds {
		if err := r.AppendBalance(&seeds[i]); err != nil {
			t.Fatalf("AppendBalance: %v", err)
		}
	}

	got, err := r.AggregateBalanceTrend(7)
	if err != nil {
		t.Fatalf("AggregateBalanceTrend: %v", err)
	}
	want := bucketExpected(t, seeds[:4], 24*time.Hour) // 排除窗外种子
	if len(got) != len(want) {
		t.Fatalf("trend rows = %d, want %d (%+v)", len(got), len(want), got)
	}
	for _, row := range got {
		b := row.Day
		if w, ok := want[b]; !ok || row.Balance != w {
			t.Errorf("day %v balance = %v, want %v", b, row.Balance, want[b])
		}
	}
}

func TestAggregateBalanceTrendHourly(t *testing.T) {
	r := storagetest.Rates(t)
	now := time.Now().UTC().Truncate(time.Microsecond) // PostgreSQL 只存微秒，种子截断避免 .Equal 挂在纳秒上

	seeds := []storage.BalanceSnapshot{
		// 同一小时内两条：取最后一条 11。
		{ChannelID: 1, Balance: 10, SampledAt: now.Add(-2*time.Hour - 5*time.Minute)},
		{ChannelID: 1, Balance: 11, SampledAt: now.Add(-2*time.Hour + 5*time.Minute)},
		// 两小时前的整点桶（独立小时）：渠道 2 的 5。
		{ChannelID: 2, Balance: 5, SampledAt: now.Add(-2 * time.Hour)},
		// 三十小时前：超出 24 小时窗口。
		{ChannelID: 2, Balance: 999, SampledAt: now.Add(-30 * time.Hour)},
	}
	for i := range seeds {
		if err := r.AppendBalance(&seeds[i]); err != nil {
			t.Fatalf("AppendBalance: %v", err)
		}
	}

	got, err := r.AggregateBalanceTrendHourly(24)
	if err != nil {
		t.Fatalf("AggregateBalanceTrendHourly: %v", err)
	}
	want := bucketExpected(t, seeds[:3], time.Hour)
	if len(got) != len(want) {
		t.Fatalf("hourly rows = %d, want %d (%+v)", len(got), len(want), got)
	}
	for _, row := range got {
		b := row.Day
		if w, ok := want[b]; !ok || row.Balance != w {
			t.Errorf("hour %v balance = %v, want %v", b, row.Balance, want[b])
		}
	}
}

func TestAggregateBalanceTrendEmpty(t *testing.T) {
	r := storagetest.Rates(t)
	for name, fn := range map[string]func() ([]storage.DailyAggregate, error){
		"daily":  func() ([]storage.DailyAggregate, error) { return r.AggregateBalanceTrend(7) },
		"hourly": func() ([]storage.DailyAggregate, error) { return r.AggregateBalanceTrendHourly(24) },
	} {
		rows, err := fn()
		if err != nil {
			t.Fatalf("%s empty: %v", name, err)
		}
		if len(rows) != 0 {
			t.Errorf("%s empty table rows = %+v, want none", name, rows)
		}
	}
}

// TestListBalanceSince 窗口查询：只返回该渠道 sampled_at >= since 的行（升序），
// 其它渠道与窗口外的行都不出现。对应 spec：docs/specs/usage-forecast.md AC-4。
func TestListBalanceSince(t *testing.T) {
	r := storagetest.Rates(t)
	now := time.Now().UTC().Truncate(time.Microsecond)

	seed := []storage.BalanceSnapshot{
		{ChannelID: 1, Balance: 100, SampledAt: now.Add(-8 * 24 * time.Hour)}, // 窗口外
		{ChannelID: 1, Balance: 80, SampledAt: now.Add(-6 * 24 * time.Hour)},
		{ChannelID: 1, Balance: 60, SampledAt: now.Add(-3 * 24 * time.Hour)},
		{ChannelID: 1, Balance: 40, SampledAt: now.Add(-1 * time.Hour)},
		{ChannelID: 2, Balance: 7, SampledAt: now.Add(-2 * 24 * time.Hour)}, // 其它渠道
	}
	for i := range seed {
		if err := r.AppendBalance(&seed[i]); err != nil {
			t.Fatalf("AppendBalance: %v", err)
		}
	}

	// 边界恰好等于 since（严格 >= 语义：包含）。
	since := now.Add(-7 * 24 * time.Hour)
	rows, err := r.ListBalanceSince(1, since)
	if err != nil {
		t.Fatalf("ListBalanceSince: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows = %d, want 3（窗口外 8d 的被剔除、渠道 2 不出现）: %+v", len(rows), rows)
	}
	for i, want := range []float64{80, 60, 40} {
		if rows[i].Balance != want {
			t.Errorf("rows[%d].Balance = %v, want %v（升序）", i, rows[i].Balance, want)
		}
	}

	// 无匹配 → 空切片不报错。
	rows, err = r.ListBalanceSince(99, since)
	if err != nil || len(rows) != 0 {
		t.Errorf("no-match rows = %v, err = %v, want empty", rows, err)
	}
}
