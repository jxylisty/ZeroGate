package server

// 模型列表 / 探针测试：验证 2026-10-10 整改 ——
// 探针单飞行 + 最小间隔 + 账号轮换；整渠道不可用（全冷却/禁用）时从模型列表隐藏；
// 探针失败不惩罚账号。

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/pool"
	"zerogate/internal/provider"
	"zerogate/internal/upstream"
)

// modelsProbeUpstream 记录 FetchModels 调用次数与被探账号，供单飞行/轮换/零惩罚断言。
type modelsProbeUpstream struct {
	mu         sync.Mutex
	fetchCalls int
	probedUIDs []string
	failFetch  bool // true 时 FetchModels 返回错误
	delay      time.Duration
}

func (f *modelsProbeUpstream) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	f.mu.Lock()
	f.fetchCalls++
	f.probedUIDs = append(f.probedUIDs, a.UID)
	d := f.delay
	fail := f.failFetch
	f.mu.Unlock()
	if d > 0 {
		time.Sleep(d)
	}
	if fail {
		return nil, &provider.Error{Kind: provider.ErrServer, Status: 500, Msg: "probe boom"}
	}
	return []provider.ModelInfo{{ID: "m1", ContextWindow: 400000}}, nil
}

func (f *modelsProbeUpstream) counts() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.probedUIDs))
	copy(out, f.probedUIDs)
	return f.fetchCalls, out
}

// provider.Upstream 其余方法空实现（测试只关心 FetchModels）。
func (f *modelsProbeUpstream) RefreshToken(a *auth.Auth) error { return nil }
func (f *modelsProbeUpstream) ChatStream(a *auth.Auth, body []byte) (io.ReadCloser, int, []byte, error) {
	return io.NopCloser(strings.NewReader("")), http.StatusOK, nil, nil
}
func (f *modelsProbeUpstream) Stream(w http.ResponseWriter, r io.Reader) error { return nil }
func (f *modelsProbeUpstream) Classify(status int, body string) provider.ErrKind {
	return provider.ErrNone
}
func (f *modelsProbeUpstream) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	return nil, nil
}
func (f *modelsProbeUpstream) UserResource(a *auth.Auth) (int64, error) { return 0, nil }
func (f *modelsProbeUpstream) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	return 0, nil, nil
}
func (f *modelsProbeUpstream) DailyCheckin(a *auth.Auth) error  { return nil }
func (f *modelsProbeUpstream) GrowthTravel(a *auth.Auth) error  { return nil }
func (f *modelsProbeUpstream) GrowthLottery(a *auth.Auth) error { return nil }
func (f *modelsProbeUpstream) GrowthTasksDetailed(a *auth.Auth) provider.GrowthTasksResult {
	return provider.GrowthTasksResult{}
}
func (f *modelsProbeUpstream) Aggregate(r io.Reader) (map[string]any, error) {
	return upstream.AggregateWithThink(r, true)
}

// newModelsHandler 构造带 N 个健康账号的 Qoder Runtime。
func newModelsHandler(t *testing.T, fu provider.Upstream, uids ...string) (*Handler, *pool.Pool) {
	t.Helper()
	p := pool.New(t.TempDir() + "/state.json")
	for i, uid := range uids {
		a := &auth.Auth{UID: uid, Kind: "qoder", AccessToken: "t"}
		a.ExpiresAt = time.Now().Add(time.Hour).Unix()
		p.Add(a)
		p.SetExpire(uid, time.Now().Add(time.Duration(i+1)*time.Hour).Unix())
	}
	h := NewHandler(Config{
		Runtimes: map[provider.Kind]*Runtime{
			provider.Qoder: {Kind: provider.Qoder, Pool: p, Upstream: fu},
		},
	})
	return h, p
}

// 探针单飞行：并发 10 次 /v1/models 在缓存未命中时，上游 FetchModels 只被打 1 次。
func TestModelProbeSingleflight(t *testing.T) {
	fu := &modelsProbeUpstream{delay: 50 * time.Millisecond}
	h, _ := newModelsHandler(t, fu, "a1", "a2")

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.modelList()
		}()
	}
	wg.Wait()

	calls, _ := fu.counts()
	if calls != 1 {
		t.Fatalf("FetchModels calls = %d, want 1 (单飞行应只打一次上游)", calls)
	}
	// 探针完成后缓存已填充，后续请求能拿到完整列表
	if got := h.modelList(); len(got) == 0 {
		t.Fatal("探针完成后模型列表仍为空")
	}
}

// 探针账号轮换：连续两次探针（缓存失效后）应命中不同账号。
func TestModelProbeRotatesAccounts(t *testing.T) {
	fu := &modelsProbeUpstream{}
	h, _ := newModelsHandler(t, fu, "a1", "a2")

	rt := h.cfg.Runtimes[provider.Qoder]
	// 第一次探针 → a1（最早到期）
	if got := h.fetchRuntimeModels(rt); got == nil {
		t.Fatal("first probe returned nil")
	}
	// 失效缓存，触发第二次探针
	rt.mu.Lock()
	rt.models = nil
	rt.fetched = time.Time{}
	rt.lastFail = time.Time{}
	rt.probeAt = time.Time{}
	rt.probing = false
	rt.mu.Unlock()
	if got := h.fetchRuntimeModels(rt); got == nil {
		t.Fatal("second probe returned nil")
	}

	_, uids := fu.counts()
	if len(uids) < 2 {
		t.Fatalf("probes = %v, want 2 次", uids)
	}
	if uids[0] == uids[1] {
		t.Fatalf("探针未轮换：两次都打 %s", uids[0])
	}
}

// 探针失败零惩罚：FetchModels 返回 5xx 后，账号池冷却/禁用/err_count 全部不变。
func TestModelProbeDoesNotPenalizeAccount(t *testing.T) {
	fu := &modelsProbeUpstream{failFetch: true}
	h, p := newModelsHandler(t, fu, "a1")

	rt := h.cfg.Runtimes[provider.Qoder]
	before := p.List()[0]

	if got := h.fetchRuntimeModels(rt); got != nil {
		t.Fatalf("探针应失败返回 nil, got %v", got)
	}
	after := p.List()[0]
	if after.Cooling || after.Disabled || after.ErrCount != 0 || after.Until.Unix() != before.Until.Unix() {
		t.Fatalf("探针失败惩罚了账号: cooling=%v disabled=%v errCount=%d until=%v (before=%+v)",
			after.Cooling, after.Disabled, after.ErrCount, after.Until, before)
	}
	// 失败退避期内再问 → 直接返回 nil，不再打上游
	beforeCalls, _ := fu.counts()
	if got := h.fetchRuntimeModels(rt); got != nil {
		t.Fatal("失败退避期内不应发起新探针")
	}
	afterCalls, _ := fu.counts()
	if afterCalls != beforeCalls {
		t.Fatalf("失败退避期内仍打上游: before=%d after=%d", beforeCalls, afterCalls)
	}
}

// 整渠道不可用（账号全冷却）→ 模型与别名从 /v1/models 隐藏。
func TestModelListHidesChannelWhenAllAccountsCooling(t *testing.T) {
	fu := &modelsProbeUpstream{}
	h, p := newModelsHandler(t, fu, "a1")

	// 先让探针成功、缓存有模型
	rt := h.cfg.Runtimes[provider.Qoder]
	if got := h.fetchRuntimeModels(rt); got == nil {
		t.Fatal("probe failed")
	}

	// 别名指向该渠道，也应随渠道隐藏
	h.SetModelRedirects(map[string][]string{"alias-q1": {"qoder/m1"}})

	// 全部冷却
	p.Cooldown("a1", pool.CoolErr, time.Hour, "test")
	if healthy, total := p.HealthyCount(); healthy != 0 || total != 1 {
		t.Fatalf("healthy=%d total=%d, want 0/1", healthy, total)
	}

	list := h.modelList()
	for _, m := range list {
		id, _ := m["id"].(string)
		if strings.HasPrefix(id, "qoder/") || id == "alias-q1" {
			t.Fatalf("渠道全冷却后仍挂牌 %s", id)
		}
	}

	// 冷却解除 → 自动回来（不删配置）
	p.ReenableIfCredits("a1", 100)
	st := p.List()[0]
	if st.Cooling {
		t.Fatalf("cooling 未解除: %+v", st)
	}
	list = h.modelList()
	found := false
	for _, m := range list {
		if id, _ := m["id"].(string); id == "qoder/m1" || id == "alias-q1" {
			found = true
		}
	}
	if !found {
		t.Fatal("冷却解除后模型/别名未恢复挂牌")
	}
}

// 并发安全回归：/v1/models 高频并发不再触发数据竞争（-race 下跑）。
func TestModelListConcurrentNoRace(t *testing.T) {
	fu := &modelsProbeUpstream{delay: 20 * time.Millisecond}
	h, _ := newModelsHandler(t, fu, "a1", "a2", "a3")
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = h.modelList()
		}()
	}
	wg.Wait()
}
