// Package server 暴露 OpenAI 兼容 HTTP 接口，按模型名前缀路由到不同上游。
package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/pool"
	"wild-work/internal/provider"
	"wild-work/internal/usage"
)

// Runtime 是一个平台的一组运行时资源：pool + upstream + 静态模型兜底。
type Runtime struct {
	Kind         provider.Kind
	Pool         *pool.Pool
	Upstream     provider.Upstream
	StaticModels []provider.ModelInfo

	mu       sync.RWMutex
	models   []provider.ModelInfo
	fetched  time.Time
	lastFail time.Time
}

// Config handler 依赖。
type Config struct {
	Runtimes map[provider.Kind]*Runtime
	APIKey   string // 空 = 不鉴权

	// WebUI 为内嵌的静态 Web UI 文件系统（go:embed 产物）；非 nil 时挂载到 /
	WebUI fs.FS
	// AttachAPI 由调用方注册管理 API 路由（internal/app 的 HandleAPI）
	AttachAPI func(mux *http.ServeMux)

	// 兼容旧调用方：只传 Pool/Upstream 时等价于只启用 workbuddy。
	Pool     *pool.Pool
	Upstream provider.Upstream

	MaxRotate    int
	HardCooldown time.Duration
	SoftCooldown time.Duration
	ErrThreshold int
	ErrCooldown  time.Duration
	RefreshSkew  time.Duration

	// RecordTokens 每次成功转发后回调 Token 用量（可空 = 不统计）。
	RecordTokens func(kind, uid string, prompt, completion, cacheRead, cacheWrite int64)

	// UsageRecorder token 用量记录器（可空）。
	UsageRecorder any // *usage.Recorder
}

// stickyEntry 粘性路由记录：记录上次路由账号及连续使用次数。
// 不使用 credits（pool 中余额仅在签到/手动刷新时更新，对话后是 stale 数据），
// 改用请求计数：连续请求 maxReqs 次后自动降级换账号。
type stickyEntry struct {
	uid      string
	reqCount int
	maxReqs  int
}

// Handler 主路由。
type Handler struct {
	cfg Config
	mux *http.ServeMux

	apiMu    sync.RWMutex // 保护 cfg.APIKey（面板可运行时修改）
	stickyMu sync.RWMutex
	sticky   map[string]*stickyEntry // runtimeKind → stickyEntry

	recordTokens func(kind, uid string, prompt, completion, cacheRead, cacheWrite int64)
}

// SetTokenRecorder 注入 Token 用量回调（由 app 装配，统计反代请求实际消耗）。
func (h *Handler) SetTokenRecorder(fn func(kind, uid string, prompt, completion, cacheRead, cacheWrite int64)) {
	h.recordTokens = fn
}

// SetUsageRecorder 注入 usage recorder（供 /api/token_usage 查询累计用量）。
func (h *Handler) SetUsageRecorder(rec any) {
	h.cfg.UsageRecorder = rec
}

func NewHandler(cfg Config) *Handler {
	if cfg.Runtimes == nil && cfg.Pool != nil && cfg.Upstream != nil {
		cfg.Runtimes = map[provider.Kind]*Runtime{
			provider.WorkBuddy: {Kind: provider.WorkBuddy, Pool: cfg.Pool, Upstream: cfg.Upstream, StaticModels: WorkBuddyStaticModels()},
		}
	}
	if cfg.MaxRotate <= 0 {
		cfg.MaxRotate = 3
	}
	if cfg.HardCooldown <= 0 {
		cfg.HardCooldown = 12 * time.Hour
	}
	if cfg.SoftCooldown <= 0 {
		cfg.SoftCooldown = 60 * time.Second
	}
	if cfg.ErrThreshold <= 0 {
		cfg.ErrThreshold = 3
	}
	if cfg.ErrCooldown <= 0 {
		cfg.ErrCooldown = 10 * time.Minute
	}
	if cfg.RefreshSkew <= 0 {
		cfg.RefreshSkew = 10 * time.Minute
	}
	h := &Handler{cfg: cfg, mux: http.NewServeMux(), sticky: make(map[string]*stickyEntry), recordTokens: cfg.RecordTokens}
	h.mux.HandleFunc("POST /v1/chat/completions", h.withAuth(h.chatCompletions))
	h.mux.HandleFunc("GET /v1/models", h.withAuth(h.models))
	h.mux.HandleFunc("GET /status", h.withAuth(h.status))
	h.mux.HandleFunc("GET /healthz", h.healthz)
	h.mux.HandleFunc("GET /api/token_usage", h.withAuth(h.tokenUsage))
	if cfg.WebUI != nil {
		// 使用中间件处理 index.html，替换 {{timestamp}} 为当前时间戳
		webFS := http.FS(cfg.WebUI)
		h.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "/index.html" {
				// 读取并替换 index.html
				f, err := webFS.Open("index.html")
				if err != nil {
					http.NotFound(w, r)
					return
				}
				defer f.Close()
				
				data, err := io.ReadAll(f)
				if err != nil {
					http.Error(w, err.Error(), http.StatusInternalServerError)
					return
				}
				
				timestamp := time.Now().Unix()
				content := strings.ReplaceAll(string(data), "{{timestamp}}", fmt.Sprintf("%d", timestamp))
				
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				w.Write([]byte(content))
				return
			}
			
			// 其他文件直接返回
			http.FileServer(webFS).ServeHTTP(w, r)
		}))
	}
	if cfg.AttachAPI != nil {
		cfg.AttachAPI(h.mux)
	}
	return h
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) { h.mux.ServeHTTP(w, r) }

// stickyKey 粘性路由 key（按渠道独立）
func (h *Handler) stickyKey(kind provider.Kind) string { return kind.String() }

// pickWithSticky 粘性路由选择账号。
// 优先使用上次成功路由的账号，直到：
//   - 账号进入冷却/禁用状态
//   - 连续成功请求达到 maxReqs 次（默认 50），自动轮换
//
// 任一条件触发则降级为 Pick() 选新账号并重置粘性记录。
func (h *Handler) pickWithSticky(rt *Runtime) *auth.Auth {
	const defaultMaxReqs = 50

	h.stickyMu.RLock()
	sticky := h.sticky[h.stickyKey(rt.Kind)]
	h.stickyMu.RUnlock()

	// 尝试粘性路由
	if sticky != nil && sticky.uid != "" && sticky.reqCount < sticky.maxReqs {
		acct := rt.Pool.AuthByUID(sticky.uid)
		if acct != nil {
			status, ok := rt.Pool.Status(sticky.uid)
			if ok && !status.Cooling && !status.Disabled {
				log.Printf("sticky route platform=%s uid=%s count=%d/%d",
					rt.Kind, sticky.uid, sticky.reqCount, sticky.maxReqs)
				return acct
			}
		}
	}

	// 降级：选择余额最高的 healthy 账号
	acct := rt.Pool.Pick()
	if acct == nil {
		return nil
	}

	// 新建粘性记录
	h.stickyMu.Lock()
	h.sticky[h.stickyKey(rt.Kind)] = &stickyEntry{uid: acct.UID, maxReqs: defaultMaxReqs}
	h.stickyMu.Unlock()
	log.Printf("new sticky route platform=%s uid=%s maxReqs=%d", rt.Kind, acct.UID, defaultMaxReqs)
	return acct
}

// stickySuccess 粘性路由成功：递增请求计数。
func (h *Handler) stickySuccess(rt *Runtime) {
	h.stickyMu.Lock()
	defer h.stickyMu.Unlock()
	key := h.stickyKey(rt.Kind)
	if e := h.sticky[key]; e != nil {
		e.reqCount++
	}
}

// stickyClear 粘性路由失败（错误/冷却）：清除粘性记录，下次请求强制重新选号。
func (h *Handler) stickyClear(rt *Runtime) {
	h.stickyMu.Lock()
	defer h.stickyMu.Unlock()
	delete(h.sticky, h.stickyKey(rt.Kind))
}

func (h *Handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		key := h.currentAPIKey()
		if key != "" {
			authz := r.Header.Get("Authorization")
			if !strings.HasPrefix(authz, "Bearer ") || strings.TrimPrefix(authz, "Bearer ") != key {
				writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid API key")
				return
			}
		}
		next(w, r)
	}
}

func (h *Handler) SetAPIKey(key string) { h.apiMu.Lock(); defer h.apiMu.Unlock(); h.cfg.APIKey = key }
func (h *Handler) currentAPIKey() string {
	h.apiMu.RLock()
	defer h.apiMu.RUnlock()
	return h.cfg.APIKey
}
func (h *Handler) healthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (h *Handler) status(w http.ResponseWriter, r *http.Request) {
	accounts := map[string]any{}
	for _, k := range h.runtimeKinds() {
		rt := h.cfg.Runtimes[k]
		accounts[k.String()] = rt.Pool.List()
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": accounts})
}

var workbuddyStaticModels = []provider.ModelInfo{
	{ID: "glm-5.2", ContextWindow: 131072}, {ID: "glm-5.1", ContextWindow: 131072}, {ID: "glm-5v-turbo", ContextWindow: 131072},
	{ID: "kimi-k2.7", ContextWindow: 131072}, {ID: "minimax-m3", ContextWindow: 131072}, {ID: "hy3", ContextWindow: 131072},
	{ID: "hy3-preview", ContextWindow: 131072}, {ID: "hy3-preview-agent", ContextWindow: 131072},
	// DeepSeek 限免模型
	{ID: "deepseek-v4.1-flash", ContextWindow: 1000000, MaxTokens: 128000},
}

var traeworkStaticModels = []provider.ModelInfo{
	{ID: "glm-5.2"}, {ID: "glm-5-turbo"}, {ID: "glm-5"},
	{ID: "kimi-k2.6"}, {ID: "kimi-k2.7-code"}, {ID: "minimax-m3"}, {ID: "qwen3-coder"}, {ID: "Doubao-Seed-2.1-Pro"},
}

// dynamicModelsCache 保留给旧测试/旧单平台语义；实际多平台缓存放在 Runtime 内。
var dynamicModelsCache struct {
	sync.RWMutex
	ids      []provider.ModelInfo
	fetched  time.Time
	lastFail time.Time
}

const (
	dynamicModelsTTL        = time.Hour
	modelsFetchFailCooldown = 5 * time.Minute
)

func (h *Handler) models(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"object": "list", "data": h.modelList()})
}

func (h *Handler) modelList() []map[string]any {
	out := []map[string]any{}
	for _, k := range h.runtimeKinds() {
		rt := h.cfg.Runtimes[k]
		if rt.Pool == nil || len(rt.Pool.List()) == 0 { // 只暴露已接入账号的平台
			continue
		}
		infos := h.fetchRuntimeModels(rt)
		if len(infos) == 0 {
			infos = rt.StaticModels
		}
		for _, mi := range infos {
			id := k.String() + "/" + mi.ID
			entry := map[string]any{"id": id, "object": "model", "created": 1753600000, "owned_by": k.String()}
			if mi.ContextWindow > 0 {
				entry["context_length"] = mi.ContextWindow
			}
			if mi.MaxTokens > 0 {
				entry["max_output_tokens"] = mi.MaxTokens
			}
			out = append(out, entry)
		}
	}
	return out
}

func (h *Handler) fetchRuntimeModels(rt *Runtime) []provider.ModelInfo {
	if rt.Kind == provider.WorkBuddy { // 兼容旧单平台缓存观察点
		dynamicModelsCache.RLock()
		if len(dynamicModelsCache.ids) > 0 && time.Since(dynamicModelsCache.fetched) < dynamicModelsTTL {
			out := dynamicModelsCache.ids
			dynamicModelsCache.RUnlock()
			return out
		}
		if !dynamicModelsCache.lastFail.IsZero() && time.Since(dynamicModelsCache.lastFail) < modelsFetchFailCooldown {
			dynamicModelsCache.RUnlock()
			return nil
		}
		dynamicModelsCache.RUnlock()
	}
	rt.mu.RLock()
	if len(rt.models) > 0 && time.Since(rt.fetched) < dynamicModelsTTL {
		out := rt.models
		rt.mu.RUnlock()
		return out
	}
	if rt.Kind != provider.WorkBuddy && !rt.lastFail.IsZero() && time.Since(rt.lastFail) < modelsFetchFailCooldown {
		rt.mu.RUnlock()
		return nil
	}
	rt.mu.RUnlock()
	acct := rt.Pool.Pick()
	if acct == nil {
		return nil
	}
	infos, err := rt.Upstream.FetchModels(acct)
	if err != nil || len(infos) == 0 {
		now := time.Now()
		rt.mu.Lock()
		rt.lastFail = now
		rt.mu.Unlock()
		if rt.Kind == provider.WorkBuddy {
			dynamicModelsCache.Lock()
			dynamicModelsCache.lastFail = now
			dynamicModelsCache.Unlock()
		}
		return nil
	}
	now := time.Now()
	rt.mu.Lock()
	rt.models = infos
	rt.fetched = now
	rt.lastFail = time.Time{}
	rt.mu.Unlock()
	if rt.Kind == provider.WorkBuddy { // 兼容旧测试观察点
		dynamicModelsCache.Lock()
		dynamicModelsCache.ids = infos
		dynamicModelsCache.fetched = now
		dynamicModelsCache.lastFail = time.Time{}
		dynamicModelsCache.Unlock()
	}
	return infos
}

func (h *Handler) chatCompletions(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "read body: "+err.Error())
		return
	}
	var peek struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
	}
	_ = json.Unmarshal(body, &peek)
	rt, model, err := h.runtimeForModel(peek.Model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_model", err.Error())
		return
	}
	body, err = rewriteModel(body, model)
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}

	tried := map[string]bool{}
	var lastErr error
	for i := 0; i < h.cfg.MaxRotate; i++ {
		acct := h.pickWithSticky(rt)
		if acct == nil {
			break
		}
		if tried[acct.UID] {
			// 粘性路由选回已尝试的账号，清除粘性记录后重试
			h.stickyClear(rt)
			acct = rt.Pool.PickExcluding(tried)
			if acct == nil {
				break
			}
		}
		tried[acct.UID] = true
		if acct.NeedsRefresh(h.cfg.RefreshSkew) {
			log.Printf("refresh start platform=%s uid=%s reason=request", rt.Kind, acct.UID)
			if err := rt.Upstream.RefreshToken(acct); err != nil {
				log.Printf("refresh failed platform=%s uid=%s err=%v", rt.Kind, acct.UID, err)
				lastErr = err
				h.stickyClear(rt)
				var ue *provider.Error
				if errors.As(err, &ue) && ue.Kind == provider.ErrSessionDead {
					rt.Pool.Disable(acct.UID, "refresh session dead")
				} else {
					rt.Pool.Cooldown(acct.UID, pool.CoolErr, h.cfg.ErrCooldown, "refresh: "+err.Error())
				}
				continue
			}
			if err := acct.SaveAtomic(); err != nil {
				log.Printf("refresh save failed platform=%s uid=%s err=%v", rt.Kind, acct.UID, err)
			}
			log.Printf("refresh success platform=%s uid=%s expires_at=%d", rt.Kind, acct.UID, acct.ExpiresAt)
		}
		rc, status, respBody, terr := rt.Upstream.ChatStream(acct, body)
		if terr != nil {
			lastErr = terr
			h.stickyClear(rt)
			rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
			continue
		}
		if status >= 400 {
			h.stickyClear(rt)
			kind := rt.Upstream.Classify(status, string(respBody))
			switch kind {
			case provider.ErrHardCredit:
				rt.Pool.Cooldown(acct.UID, pool.CoolHard, h.cfg.HardCooldown, "余额/权益不足")
			case provider.ErrSoftRate:
				rt.Pool.Cooldown(acct.UID, pool.CoolSoft, h.cfg.SoftCooldown, "429 rate limit")
			case provider.ErrSessionDead:
				rt.Pool.Disable(acct.UID, "session dead")
			case provider.ErrNotFound:
				rt.Pool.Cooldown(acct.UID, pool.CoolSoft, h.cfg.SoftCooldown, "upstream 404")
			default:
				rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
			}
			// 上游错误直接透传给客户端，不做包装
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_, _ = w.Write(respBody)
			return
		}
		defer rc.Close()
		rt.Pool.NoteSuccess(acct.UID)
		h.stickySuccess(rt)
		kind, uid := rt.Kind.String(), acct.UID
		if peek.Stream {
			track := &tokenTrackingReader{r: rc}
				_ = rt.Upstream.Stream(w, track)
				track.finish()
				if rec := h.recordTokens; rec != nil {
					if p, c, cr, cw, ok := track.ScanResult(); ok && (p > 0 || c > 0) {
						rec(kind, uid, p, c, cr, cw)
					}
				}
				return
		}
		resp, err := rt.Upstream.Aggregate(rc)
		if err != nil {
			writeOpenAIError(w, http.StatusBadGateway, "upstream_parse", err.Error())
			return
		}
		if rec := h.recordTokens; rec != nil {
				if u, ok := resp["usage"].(map[string]any); ok {
					p, _ := asInt(u["prompt_tokens"])
					c, _ := asInt(u["completion_tokens"])
					if p == 0 && c == 0 {
						c = int64(0)
						if t, ok := asInt(u["total_tokens"]); ok {
							c = t // 上游只给 total 时按 completion 计入
						}
					}
					cr, _ := asInt(u["prompt_cache_hit_tokens"])
					if cr == 0 {
						cr, _ = asInt(u["cache_read_input_tokens"])
					}
					if cr == 0 {
						cr, _ = asInt(u["cached_tokens"])
					}
					cw, _ := asInt(u["prompt_cache_write_tokens"])
					if cw == 0 {
						cw, _ = asInt(u["cache_creation_input_tokens"])
					}
					if p > 0 || c > 0 {
						rec(kind, uid, p, c, cr, cw)
					}
				}
			}
		writeJSON(w, http.StatusOK, resp)
		return
	}
	msg := "all accounts unavailable (cooling/disabled)"
	if lastErr != nil {
		msg += ": " + lastErr.Error()
	}
	writeOpenAIError(w, http.StatusServiceUnavailable, "no_healthy_account", msg)
}

func (h *Handler) runtimeForModel(model string) (*Runtime, string, error) {
	parts := strings.SplitN(strings.TrimSpace(model), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, "", fmt.Errorf("model must use explicit prefix: workbuddy/<model> / traework/<model> / qoder/<model>")
	}
	kind := provider.Kind(parts[0])
	rt := h.cfg.Runtimes[kind]
	if rt == nil || rt.Pool == nil || rt.Upstream == nil {
		return nil, "", fmt.Errorf("provider %q is not configured", kind)
	}
	if len(rt.Pool.List()) == 0 {
		return nil, "", fmt.Errorf("provider %q has no account", kind)
	}
	return rt, parts[1], nil
}

func rewriteModel(body []byte, model string) ([]byte, error) {
	var obj map[string]any
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	obj["model"] = model
	return json.Marshal(obj)
}

func (h *Handler) runtimeKinds() []provider.Kind {
	ks := make([]provider.Kind, 0, len(h.cfg.Runtimes))
	for k := range h.cfg.Runtimes {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	return ks
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	raw, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// ---------------------------------------------------------------------------
// Token 用量统计
// ---------------------------------------------------------------------------

var (
	usageObjRe = regexp.MustCompile(`"usage":\s*\{(.*?)\}`)
	usageNumRe = regexp.MustCompile(`"(prompt_tokens|completion_tokens|total_tokens|prompt_cache_hit_tokens|cache_read_input_tokens|cached_tokens|prompt_cache_write_tokens|cache_creation_input_tokens)":\s*(\d+)`)
	dataObjRe  = regexp.MustCompile(`^data:\s*(\{.*\})\s*$`)
)

// tokenTrackingReader 包裹上游 SSE 响应流：透明转发的同时按行抓取用量。
// 支持两种流格式：
//  1. OpenAI 兼容：收尾 chunk 内 `"usage":{"prompt_tokens":..,"completion_tokens":..}`
//  2. SOLO 原生：`event:token_usage` + `data:{"prompt_tokens":..}`（无 usage 键）
//
// 覆盖赋值即取到整轮消耗；部分上游只回 total_tokens，finish 时归一为 completion。
// 缓存命中量优先链：prompt_cache_hit_tokens → cache_read_input_tokens → cached_tokens。
type tokenTrackingReader struct {
	r          io.ReadCloser
	pending    []byte
	prompt     int64
	completion int64
	total      int64
	cacheRead  int64
	cacheWrite int64
	has        bool
}

func (t *tokenTrackingReader) Read(p []byte) (int, error) {
	n, err := t.r.Read(p)
	if n > 0 {
		t.scan(p[:n])
	}
	return n, err
}

func (t *tokenTrackingReader) Close() error { return t.r.Close() }

// finish 处理流末尾可能没有换行的残留数据，并归一化仅含 total 的 usage。
func (t *tokenTrackingReader) finish() {
	if len(t.pending) > 0 {
		t.scan(nil)
		t.pending = nil
	}
	if t.has && t.prompt == 0 && t.completion == 0 {
		t.completion = t.total
	}
}

func (t *tokenTrackingReader) ScanResult() (prompt, completion, cacheRead, cacheWrite int64, ok bool) {
	return t.prompt, t.completion, t.cacheRead, t.cacheWrite, t.has
}

func (t *tokenTrackingReader) scan(chunk []byte) {
	t.pending = append(t.pending, chunk...)
	for {
		i := bytes.IndexByte(t.pending, '\n')
		if i < 0 {
			break
		}
		line := t.pending[:i]
		t.pending = t.pending[i+1:]
		t.parseLine(line)
	}
	if len(t.pending) > 1<<16 {
		t.pending = t.pending[len(t.pending)-1<<15:]
	}
}

func (t *tokenTrackingReader) parseLine(line []byte) {
	if m := usageObjRe.FindSubmatch(line); m != nil {
		// OpenAI 兼容：行内嵌 "usage":{...}
		for _, kv := range usageNumRe.FindAllSubmatch(m[1], -1) {
			v, _ := strconv.ParseInt(string(kv[2]), 10, 64)
			switch string(kv[1]) {
			case "prompt_tokens":
				t.prompt = v
			case "completion_tokens":
				t.completion = v
			case "total_tokens":
				t.total = v
			case "prompt_cache_hit_tokens", "cache_read_input_tokens", "cached_tokens":
				t.cacheRead = v
			case "prompt_cache_write_tokens", "cache_creation_input_tokens":
				t.cacheWrite = v
			}
		}
		t.has = true
		return
	}
	// SOLO 原生：token_usage 事件的 data 行 {"prompt_tokens":..,"completion_tokens":..}
	m := dataObjRe.FindSubmatch(line)
	if m == nil {
		return
	}
	var obj map[string]any
	if json.Unmarshal(m[1], &obj) != nil {
		return
	}
	p, ok := obj["prompt_tokens"].(float64)
	if !ok {
		return
	}
	t.prompt = int64(p)
	if c, ok := obj["completion_tokens"].(float64); ok {
		t.completion = int64(c)
	}
	if tt, ok := obj["total_tokens"].(float64); ok {
		t.total = int64(tt)
	}
	if cr, ok := obj["prompt_cache_hit_tokens"].(float64); ok {
		t.cacheRead = int64(cr)
	} else if cr, ok := obj["cache_read_input_tokens"].(float64); ok {
		t.cacheRead = int64(cr)
	} else if cr, ok := obj["cached_tokens"].(float64); ok {
		t.cacheRead = int64(cr)
	}
	if cw, ok := obj["prompt_cache_write_tokens"].(float64); ok {
		t.cacheWrite = int64(cw)
	} else if cw, ok := obj["cache_creation_input_tokens"].(float64); ok {
		t.cacheWrite = int64(cw)
	}
	t.has = true
}

// asInt 把 JSON 反序列化的数字（float64 或 string）安全转成 int64。
func asInt(v any) (int64, bool) {
	switch n := v.(type) {
	case float64:
		return int64(n), true
	case string:
		iv, err := strconv.ParseInt(n, 10, 64)
		return iv, err == nil
	default:
		return 0, false
	}
}

func writeOpenAIError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]any{"message": msg, "type": "api_error", "code": code}})
}

// tokenUsage 返回累计 Token 用量统计（按渠道分组）。
func (h *Handler) tokenUsage(w http.ResponseWriter, r *http.Request) {
	if h.cfg.UsageRecorder == nil {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "Token recording not enabled"})
		return
	}
	rec, ok := h.cfg.UsageRecorder.(*usage.Recorder)
	if !ok {
		writeJSON(w, http.StatusNotImplemented, map[string]any{"error": "Token recording not enabled"})
		return
	}
	type tokenStats struct {
		Prompt      int64 `json:"prompt_tokens"`
		Completion  int64 `json:"completion_tokens"`
		CacheRead   int64 `json:"cache_read_tokens"`
		CacheWrite  int64 `json:"cache_write_tokens"`
		Total       int64 `json:"total_tokens"`
	}
	stats := make(map[string]*tokenStats)
	for kind := range h.cfg.Runtimes {
		stats[kind.String()] = &tokenStats{}
	}
	// 从 recorder 获取所有账号的 token 统计
	report := rec.Report()
	for _, au := range report {
		kindStr := au.Kind
		if stats[kindStr] == nil {
			stats[kindStr] = &tokenStats{}
		}
		s := stats[kindStr]
		// 累加每日 token 统计
		for _, td := range au.Tokens {
			s.Prompt += td.Prompt
			s.Completion += td.Completion
			s.CacheRead += td.CacheRead
			s.CacheWrite += td.CacheWrite
		}
		s.Total = s.Prompt + s.Completion + s.CacheRead + s.CacheWrite
	}
	// 汇总总用量
	var totalPrompt, totalCompletion, totalCacheRead, totalCacheWrite int64
	for _, s := range stats {
		totalPrompt += s.Prompt
		totalCompletion += s.Completion
		totalCacheRead += s.CacheRead
		totalCacheWrite += s.CacheWrite
	}
	out := map[string]any{
		"channels": stats,
		"summary": map[string]int64{
			"prompt_tokens":      totalPrompt,
			"completion_tokens":  totalCompletion,
			"cache_read_tokens":  totalCacheRead,
			"cache_write_tokens": totalCacheWrite,
			"total_tokens":       totalPrompt + totalCompletion + totalCacheRead + totalCacheWrite,
		},
	}
	writeJSON(w, http.StatusOK, out)
}

func WorkBuddyStaticModels() []provider.ModelInfo {
	return append([]provider.ModelInfo{}, workbuddyStaticModels...)
}
func TraeWorkStaticModels() []provider.ModelInfo {
	return append([]provider.ModelInfo{}, traeworkStaticModels...)
}
