package notify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/worryzyy/upstream-hub/internal/crypto"
	"github.com/worryzyy/upstream-hub/internal/storage"
)

// 特征测试（第一批）：Dispatcher 的纯辅助函数。
// fanout / suppress / DispatchRateBatch 的测试见后续批次（需要 fake store 缝隙）。
// 对应 spec：docs/specs/notification-dispatch.md。

func TestBackoffDelay(t *testing.T) {
	for _, tc := range []struct {
		attempt int
		want    time.Duration
	}{
		{1, 1 * time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
		{6, 30 * time.Second}, // 32s 被上限截断
		{10, 30 * time.Second},
	} {
		if got := backoffDelay(tc.attempt); got != tc.want {
			t.Errorf("backoffDelay(%d) = %v, want %v", tc.attempt, got, tc.want)
		}
	}
}

func TestSubsetForSubscriptions(t *testing.T) {
	changes := []RateChange{
		{GroupName: "g1"},
		{GroupName: "g2"},
	}

	// 空订阅 = 订阅一切，返回全部。
	got := subsetForSubscriptions(1, changes, nil)
	if !reflect.DeepEqual(got, changes) {
		t.Errorf("empty subs should return all changes, got %v", got)
	}

	// 订阅上游 1 的 g1 分组：只切出 g1。
	subs := []Subscription{{ChannelID: 1, Mode: SubscriptionModeGroups, Groups: []string{"g1"}}}
	got = subsetForSubscriptions(1, changes, subs)
	if len(got) != 1 || got[0].GroupName != "g1" {
		t.Errorf("subs g1 should return [g1], got %v", got)
	}

	// 订阅另一个上游：切不出任何东西。
	subs = []Subscription{{ChannelID: 2, Mode: SubscriptionModeAll}}
	got = subsetForSubscriptions(1, changes, subs)
	if len(got) != 0 {
		t.Errorf("other channel subscription should return empty, got %v", got)
	}

	// 订阅上游 1 的 all：全量命中。
	subs = []Subscription{{ChannelID: 1, Mode: SubscriptionModeAll}}
	got = subsetForSubscriptions(1, changes, subs)
	if len(got) != 2 {
		t.Errorf("all subscription should return both, got %v", got)
	}

	// 空输入 + 空订阅：空结果。
	if got := subsetForSubscriptions(1, nil, nil); len(got) != 0 {
		t.Errorf("nil changes should return empty, got %v", got)
	}
}

// TestSendNormalizesMaxAttempts 钉住构造器的归一化行为：
// SendMaxAttempts <= 0 时按 1 处理。
func TestSendNormalizesMaxAttempts(t *testing.T) {
	for _, attempts := range []int{0, -3} {
		d := NewDispatcherWithCooldown(nil, nil, nil, Policy{SendMaxAttempts: attempts}, nil)
		if d.Policy().SendMaxAttempts != 1 {
			t.Errorf("SendMaxAttempts=%d should normalize to 1, got %d", attempts, d.Policy().SendMaxAttempts)
		}
	}
	d := NewDispatcherWithCooldown(nil, nil, nil, Policy{SendMaxAttempts: 2}, nil)
	if d.Policy().SendMaxAttempts != 2 {
		t.Errorf("positive SendMaxAttempts should be kept, got %d", d.Policy().SendMaxAttempts)
	}
}

// TDD red：NewDispatcherWithStore 尚不存在。窄接口缝隙允许用 fake store
// 驱动 fanout（不需要真 PostgreSQL）。更完整的用例在 Phase 5。
func TestNewDispatcherWithStoreEmptyFanout(t *testing.T) {
	f := &fakeStore{}
	d := NewDispatcherWithStore(f, nil, nil, Policy{}, nil)
	if err := d.Dispatch(context.Background(), Message{Event: storage.EventBalanceLow, ChannelID: 1}); err != nil {
		t.Errorf("Dispatch with no enabled channels = %v, want nil", err)
	}
	if f.listCalls != 1 {
		t.Errorf("ListEnabledChannels calls = %d, want 1", f.listCalls)
	}
}

// fakeStore 是 NotifierStore 的最小假实现。
type fakeStore struct {
	channels  []storage.NotificationChannel
	logs      []*storage.NotificationLog
	err       error // ListEnabledChannels 返回的错误
	listCalls int
}

func (f *fakeStore) ListEnabledChannels() ([]storage.NotificationChannel, error) {
	f.listCalls++
	return f.channels, f.err
}

func (f *fakeStore) AppendLog(log *storage.NotificationLog) error {
	f.logs = append(f.logs, log)
	return nil
}

// ---------- Phase 5：fake 缝隙驱动的 Dispatcher 行为测试 ----------
// fakeNotifier 经 Register 以合成类型注册，Build() 可解析；
// 密文走真 crypto.NewCipher。attempts <= 2，控制真实退避等待在 1s 内。

const fakeNotifyType storage.NotificationChannelType = "fake-test"

// sentRecord 记录一次成功送达。
type sentRecord struct {
	notifier string
	msg      Message
}

type fakeState struct {
	mu    sync.Mutex
	sends []sentRecord
}

func (s *fakeState) sent() []sentRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]sentRecord(nil), s.sends...)
}

// fakeNotifier 按配置"前 fail 次返回错误"的假 Notifier。
type fakeNotifier struct {
	state *fakeState
	name  string
	fail  int
	calls int
}

func (f *fakeNotifier) Send(ctx context.Context, msg Message) error {
	f.calls++
	if f.calls <= f.fail {
		return fmt.Errorf("fake send failure #%d", f.calls)
	}
	f.state.mu.Lock()
	f.state.sends = append(f.state.sends, sentRecord{notifier: f.name, msg: msg})
	f.state.mu.Unlock()
	return nil
}

func (f *fakeNotifier) Type() storage.NotificationChannelType { return fakeNotifyType }

// registerFake 注册合成 Notifier 类型（覆盖式注册，包内测试串行执行无竞态）。
func registerFake(t *testing.T, state *fakeState) {
	t.Helper()
	Register(fakeNotifyType, func(raw string) (Notifier, error) {
		var cfg struct {
			Name string `json:"name"`
			Fail int    `json:"fail"`
		}
		if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
			return nil, err
		}
		return &fakeNotifier{state: state, name: cfg.Name, fail: cfg.Fail}, nil
	})
}

// fakeChannel 构造一个启用的 fake 类型通知渠道。
func fakeChannel(t *testing.T, cipher *crypto.Cipher, id uint, name string, fail int, subs string) storage.NotificationChannel {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"name": name, "fail": fail})
	if err != nil {
		t.Fatalf("marshal fake config: %v", err)
	}
	cfg, err := cipher.Encrypt(string(raw))
	if err != nil {
		t.Fatalf("encrypt fake config: %v", err)
	}
	return storage.NotificationChannel{
		ID:            id,
		Name:          name,
		Type:          fakeNotifyType,
		ConfigCipher:  cfg,
		Subscriptions: subs,
		Enabled:       true,
	}
}

// fakeCooldown 可编排的冷却存储。
type fakeCooldown struct {
	ok    bool
	err   error
	calls int
}

func (f *fakeCooldown) TryClaimCooldown(channelID uint, event storage.NotificationEvent, cooldown time.Duration) (bool, error) {
	f.calls++
	return f.ok, f.err
}

func newFakeDispatcher(t *testing.T, store NotifierStore, cooldown CooldownStore, policy Policy) *Dispatcher {
	t.Helper()
	cipher, err := crypto.NewCipher("test-only-key")
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return NewDispatcherWithStore(store, cipher, nil, policy, cooldown)
}

func TestDispatchFanoutSubscriptions(t *testing.T) {
	state := &fakeState{}
	registerFake(t, state)
	msg := Message{Event: storage.EventBalanceLow, ChannelID: 1, Subject: "余额告警", Body: "body"}

	// 空订阅 = 订阅一切；订阅别的上游 = 不发。
	store := &fakeStore{channels: []storage.NotificationChannel{
		fakeChannel(t, mustCipher(t), 1, "all", 0, "[]"),
		fakeChannel(t, mustCipher(t), 2, "other", 0, `[{"channel_id":2,"mode":"all"}]`),
	}}
	d := newFakeDispatcher(t, store, nil, Policy{})
	if err := d.Dispatch(context.Background(), msg); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	sends := state.sent()
	if len(sends) != 1 || sends[0].notifier != "all" || sends[0].msg.Subject != "余额告警" {
		t.Errorf("sends = %+v, want one to \"all\"", sends)
	}
	if len(store.logs) != 1 || !store.logs[0].Success {
		t.Errorf("logs = %+v, want 1 success log", store.logs)
	}

	// 特征：groups 模式订阅对非 rate 事件忽略分组过滤（见 subscription_test）。
	state2 := &fakeState{}
	registerFake(t, state2)
	store2 := &fakeStore{channels: []storage.NotificationChannel{
		fakeChannel(t, mustCipher(t), 1, "groups", 0, `[{"channel_id":1,"mode":"groups","groups":["g-other"]}]`),
	}}
	d2 := newFakeDispatcher(t, store2, nil, Policy{})
	if err := d2.Dispatch(context.Background(), msg); err != nil {
		t.Fatalf("Dispatch: %v", err)
	}
	if got := state2.sent(); len(got) != 1 {
		t.Errorf("groups sub should receive non-rate event, sends = %+v", got)
	}
}

func mustCipher(t *testing.T) *crypto.Cipher {
	t.Helper()
	c, err := crypto.NewCipher("test-only-key")
	if err != nil {
		t.Fatalf("NewCipher: %v", err)
	}
	return c
}

func TestDispatchSuppress(t *testing.T) {
	balMsg := Message{Event: storage.EventBalanceLow, ChannelID: 1, Subject: "s", Body: "b"}

	for _, tc := range []struct {
		name        string
		msg         Message
		cooldown    time.Duration
		claimOK     bool
		claimErr    error
		wantSent    int
		wantClaims  int
	}{
		{"claim ok sends", balMsg, time.Minute, true, nil, 1, 1},
		{"claim false suppresses", balMsg, time.Minute, false, nil, 0, 1},
		{"cooldown store error sends anyway", balMsg, time.Minute, false, errors.New("db down"), 1, 1},
		{"non balance_low never claims", Message{Event: storage.EventRateChanged, ChannelID: 1}, time.Minute, false, nil, 1, 0},
		{"system message channel 0 bypasses", Message{Event: storage.EventBalanceLow, ChannelID: 0}, time.Minute, false, nil, 1, 0},
		{"cooldown disabled bypasses", balMsg, 0, false, nil, 1, 0},
	} {
		state := &fakeState{}
		registerFake(t, state)
		store := &fakeStore{channels: []storage.NotificationChannel{
			fakeChannel(t, mustCipher(t), 1, "ch", 0, "[]"),
		}}
		cd := &fakeCooldown{ok: tc.claimOK, err: tc.claimErr}
		d := newFakeDispatcher(t, store, cd, Policy{BalanceLowCooldown: tc.cooldown})
		if err := d.Dispatch(context.Background(), tc.msg); err != nil {
			t.Fatalf("%s: Dispatch: %v", tc.name, err)
		}
		if got := len(state.sent()); got != tc.wantSent {
			t.Errorf("%s: sends = %d, want %d", tc.name, got, tc.wantSent)
		}
		if cd.calls != tc.wantClaims {
			t.Errorf("%s: cooldown claims = %d, want %d", tc.name, cd.calls, tc.wantClaims)
		}
	}
}

func TestDispatchRateBatch(t *testing.T) {
	now := time.Now()

	// 守卫：nil 渠道 / 空变化 → 直接返回，不触碰 store。
	guardStore := &fakeStore{}
	d := newFakeDispatcher(t, guardStore, nil, Policy{})
	if err := d.DispatchRateBatch(context.Background(), nil, []RateChange{{GroupName: "g"}}); err != nil {
		t.Errorf("nil channel should return nil, got %v", err)
	}
	ch := &storage.Channel{ID: 1, Name: "alpha"}
	if err := d.DispatchRateBatch(context.Background(), ch, nil); err != nil {
		t.Errorf("empty changes should return nil, got %v", err)
	}
	if guardStore.listCalls != 0 {
		t.Errorf("guards should not hit store, calls = %d", guardStore.listCalls)
	}

	// MinChangePct 过滤：首见（OldRatio=0）恒过；小变动被滤掉。
	state := &fakeState{}
	registerFake(t, state)
	store := &fakeStore{channels: []storage.NotificationChannel{
		fakeChannel(t, mustCipher(t), 1, "ch", 0, "[]"),
	}}
	d = newFakeDispatcher(t, store, nil, Policy{MinChangePct: 10, BatchRateChanges: true})
	changes := []RateChange{
		{GroupName: "first-seen", OldRatio: 0, NewRatio: 1, ChangedAt: now},
		{GroupName: "small", OldRatio: 100, NewRatio: 101, ChangedAt: now},
		{GroupName: "big", OldRatio: 100, NewRatio: 115, ChangedAt: now},
	}
	if err := d.DispatchRateBatch(context.Background(), ch, changes); err != nil {
		t.Fatalf("DispatchRateBatch: %v", err)
	}
	sends := state.sent()
	if len(sends) != 1 {
		t.Fatalf("batch sends = %d, want 1 merged", len(sends))
	}
	if !strings.Contains(sends[0].msg.Subject, "2 个分组变动") {
		t.Errorf("merged subject %q missing filtered count", sends[0].msg.Subject)
	}

	// 订阅切片：只订 g1 的渠道只收到 g1。
	state2 := &fakeState{}
	registerFake(t, state2)
	store2 := &fakeStore{channels: []storage.NotificationChannel{
		fakeChannel(t, mustCipher(t), 1, "g1-only", 0, `[{"channel_id":1,"mode":"groups","groups":["g1"]}]`),
	}}
	d = newFakeDispatcher(t, store2, nil, Policy{BatchRateChanges: true})
	if err := d.DispatchRateBatch(context.Background(), ch, []RateChange{
		{GroupName: "g1", OldRatio: 1, NewRatio: 2, ChangedAt: now},
		{GroupName: "g2", OldRatio: 1, NewRatio: 2, ChangedAt: now},
	}); err != nil {
		t.Fatalf("DispatchRateBatch: %v", err)
	}
	sends = state2.sent()
	if len(sends) != 1 || !strings.Contains(sends[0].msg.Body, "g1") || strings.Contains(sends[0].msg.Body, "g2") {
		t.Errorf("subscription slice sends = %+v, want only g1", sends)
	}

	// BatchRateChanges=false：逐条发送。
	state3 := &fakeState{}
	registerFake(t, state3)
	store3 := &fakeStore{channels: []storage.NotificationChannel{
		fakeChannel(t, mustCipher(t), 1, "ch", 0, "[]"),
	}}
	d = newFakeDispatcher(t, store3, nil, Policy{BatchRateChanges: false})
	if err := d.DispatchRateBatch(context.Background(), ch, changes[:2]); err != nil {
		t.Fatalf("DispatchRateBatch: %v", err)
	}
	if got := len(state3.sent()); got != 2 {
		t.Errorf("per-item sends = %d, want 2", got)
	}

	// 部分失败：errors.Join 返回错误，成功渠道照发、日志两条都写。
	state4 := &fakeState{}
	registerFake(t, state4)
	store4 := &fakeStore{channels: []storage.NotificationChannel{
		fakeChannel(t, mustCipher(t), 1, "bad", 5, "[]"),
		fakeChannel(t, mustCipher(t), 2, "good", 0, "[]"),
	}}
	d = newFakeDispatcher(t, store4, nil, Policy{SendMaxAttempts: 1, BatchRateChanges: true})
	err := d.DispatchRateBatch(context.Background(), ch, []RateChange{
		{GroupName: "g1", OldRatio: 1, NewRatio: 2, ChangedAt: now},
	})
	if err == nil {
		t.Fatal("partial failure should return joined error")
	}
	if !strings.Contains(err.Error(), "bad") {
		t.Errorf("error %q should name failing channel", err)
	}
	if got := len(state4.sent()); got != 1 || state4.sent()[0].notifier != "good" {
		t.Errorf("good channel sends = %+v", state4.sent())
	}
	if len(store4.logs) != 2 {
		t.Errorf("logs = %d, want 2 (fail + success)", len(store4.logs))
	}

	// store 出错：直接返回。
	store5 := &fakeStore{err: errors.New("db down")}
	d = newFakeDispatcher(t, store5, nil, Policy{})
	if err := d.DispatchRateBatch(context.Background(), ch, changes); err == nil {
		t.Error("store error should propagate")
	}
}

func TestSendWithRetry(t *testing.T) {
	msg := Message{Event: storage.EventBalanceLow, Subject: "s", Body: "b"}

	// 首发成功。
	state := &fakeState{}
	n := &fakeNotifier{state: state, name: "ok"}
	d := newFakeDispatcher(t, &fakeStore{}, nil, Policy{})
	if err := d.sendWithRetry(context.Background(), "ok", n, msg); err != nil {
		t.Errorf("first-try success: %v", err)
	}
	if got := len(state.sent()); got != 1 {
		t.Errorf("sends = %d, want 1", got)
	}

	// 失败一次后成功（attempts=2，含 ~1s 真实退避）。
	state2 := &fakeState{}
	n2 := &fakeNotifier{state: state2, name: "flaky", fail: 1}
	d2 := newFakeDispatcher(t, &fakeStore{}, nil, Policy{SendMaxAttempts: 2})
	if err := d2.sendWithRetry(context.Background(), "flaky", n2, msg); err != nil {
		t.Errorf("retry success: %v", err)
	}
	if got := len(state2.sent()); got != 1 || n2.calls != 2 {
		t.Errorf("flaky sends = %d, calls = %d, want 1 send / 2 calls", got, n2.calls)
	}

	// 全部失败：返回最后错误。
	n3 := &fakeNotifier{state: state2, name: "dead", fail: 10}
	if err := d2.sendWithRetry(context.Background(), "dead", n3, msg); err == nil {
		t.Error("all attempts failed should error")
	}

	// ctx 已取消：立即返回，不发送。
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	n4 := &fakeNotifier{state: state2, name: "cancelled"}
	if err := d2.sendWithRetry(ctx, "cancelled", n4, msg); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled ctx err = %v, want context.Canceled", err)
	}
	if n4.calls != 0 {
		t.Errorf("cancelled ctx should not send, calls = %d", n4.calls)
	}
}
