// Package server 暴露 OpenAI 兼容 HTTP 接口，按模型名前缀路由到不同上游。
package server

import (
	"bytes"
	"context"
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
	"sync/atomic"
	"time"

	"wild-work/internal/audit"
	"wild-work/internal/auth"
	"wild-work/internal/clientkey"
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

	// RecordTokens 每次成功转发后回调 Token 用量（可空 = 不统计）。model 为实际路由到的上游模型名。
	RecordTokens func(kind, uid, model string, prompt, completion, cacheRead, cacheWrite int64)

	// UsageRecorder token 用量记录器（可空）。
	UsageRecorder any // *usage.Recorder

	// ClientKeys 客户端 Key 存储（可空）。
	ClientKeys *clientkey.Store

	// AuditRec 实时调用审计记录器（可空）。
	AuditRec *audit.Recorder

	// ModelRedirects 模型别名映射。
	ModelRedirects map[string][]string
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

	rrMu     sync.Mutex                // 保护 bridgeRR
	bridgeRR map[string]*atomic.Uint64 // 桥接渠道轮转计数（runtimeKind → 计数器）

	recordTokens func(kind, uid, model string, prompt, completion, cacheRead, cacheWrite int64)

	clientKeys     *clientkey.Store
	auditRec       *audit.Recorder
	redirectsMu    sync.RWMutex
	modelRedirects map[string][]string
}

// SetTokenRecorder 注入 Token 用量回调（由 app 装配，统计反代请求实际消耗）。
func (h *Handler) SetTokenRecorder(fn func(kind, uid, model string, prompt, completion, cacheRead, cacheWrite int64)) {
	h.recordTokens = fn
}

// SetUsageRecorder 注入 usage recorder（供 /api/token_usage 查询累计用量）。
func (h *Handler) SetUsageRecorder(rec any) {
	h.cfg.UsageRecorder = rec
}

func (h *Handler) SetClientKeys(s *clientkey.Store)   { h.clientKeys = s }
func (h *Handler) SetAuditRecorder(a *audit.Recorder) { h.auditRec = a }
func (h *Handler) SetModelRedirects(m map[string][]string) {
	h.redirectsMu.Lock()
	defer h.redirectsMu.Unlock()
	h.modelRedirects = m
}
func (h *Handler) GetModelRedirects() map[string][]string {
	h.redirectsMu.RLock()
	defer h.redirectsMu.RUnlock()
	if h.modelRedirects == nil {
		return nil
	}
	out := make(map[string][]string, len(h.modelRedirects))
	for k, v := range h.modelRedirects {
		out[k] = append([]string(nil), v...)
	}
	return out
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
	h := &Handler{
		cfg:            cfg,
		mux:            http.NewServeMux(),
		sticky:         make(map[string]*stickyEntry),
		recordTokens:   cfg.RecordTokens,
		clientKeys:     cfg.ClientKeys,
		auditRec:       cfg.AuditRec,
		modelRedirects: cfg.ModelRedirects,
	}
	h.mux.HandleFunc("POST /v1/chat/completions", h.withAuth(h.chatCompletions))
	h.mux.HandleFunc("GET /v1/models", h.withAuth(h.models))
	h.mux.HandleFunc("GET /status", h.withAuth(h.status))
	h.mux.HandleFunc("GET /healthz", h.healthz)
	// 管理面板与用量大屏都要匿名读取（与其余 /api/* 管理端点一致），
	// 之前挂在 withAuth 下导致面板 401 被吞、大屏恒为 0。
	h.mux.HandleFunc("GET /api/token_usage", h.tokenUsage)
	if cfg.WebUI != nil {
		// 所有 .html 页面均替换 {{timestamp}} 为当前时间戳（css/js 缓存破坏用）
		webFS := http.FS(cfg.WebUI)
		h.mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			name := strings.TrimPrefix(r.URL.Path, "/")
			if name == "" {
				name = "index.html"
			}
			if strings.HasSuffix(name, ".html") {
				f, err := webFS.Open(name)
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

// pickBridgeRotation 桥接渠道（zcode）不走长粘性：按「到期时间升序」轮转 healthy 账号。
// 集中单账号会把并发顶到 start-plan 每账号 5 并发上限（表现为并发时断流），
// 轮转让并发分摊到多账号、各账号额度同步消耗；同到期时间的按 UID 稳定排序。
func (h *Handler) pickBridgeRotation(rt *Runtime) *auth.Auth {
	auths := rt.Pool.Auths()
	if len(auths) == 0 {
		return nil
	}
	type cand struct {
		a   *auth.Auth
		exp int64
	}
	list := make([]cand, 0, len(auths))
	for _, a := range auths {
		st, ok := rt.Pool.Status(a.UID)
		if !ok || st.Cooling || st.Disabled {
			continue
		}
		list = append(list, cand{a: a, exp: st.ExpireAt})
	}
	if len(list) == 0 {
		return rt.Pool.Pick()
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].exp != list[j].exp {
			return list[i].exp < list[j].exp
		}
		return list[i].a.UID < list[j].a.UID
	})
	h.rrMu.Lock()
	if h.bridgeRR == nil {
		h.bridgeRR = make(map[string]*atomic.Uint64)
	}
	ctr, ok := h.bridgeRR[string(rt.Kind)]
	if !ok {
		ctr = &atomic.Uint64{}
		h.bridgeRR[string(rt.Kind)] = ctr
	}
	n := ctr.Add(1)
	h.rrMu.Unlock()
	return list[int(n-1)%len(list)].a
}

// pickWithSticky 粘性路由选择账号。
// 优先使用上次成功路由的账号，直到：
//   - 账号进入冷却/禁用状态
//   - 连续成功请求达到 maxReqs 次（默认 50），自动轮换
//
// 任一条件触发则降级为 Pick() 选新账号并重置粘性记录。
func (h *Handler) pickWithSticky(rt *Runtime) *auth.Auth {
	const defaultMaxReqs = 50

	// 桥接渠道（zcode）：并发集中单账号易撞每账号并发上限，且烧额度应齐烧。
	if rt.Kind == provider.ZCode {
		return h.pickBridgeRotation(rt)
	}

	h.stickyMu.RLock()
	sticky := h.sticky[h.stickyKey(rt.Kind)]
	h.stickyMu.RUnlock()

	// 尝试粘性路由
	if sticky != nil && sticky.uid != "" && sticky.reqCount < sticky.maxReqs {
		acct := rt.Pool.AuthByUID(sticky.uid)
		if acct != nil {
			status, ok := rt.Pool.Status(sticky.uid)
			if ok && !status.Cooling && !status.Disabled {
				// 检查池中是否有更早到期的账号：如果有更紧急到期的账号（例如早到期 > 1小时），打破粘性重新调度
				best := rt.Pool.Pick()
				if best != nil && best.UID != sticky.uid {
					bestStatus, bestOk := rt.Pool.Status(best.UID)
					if bestOk && bestStatus.ExpireAt > 0 {
						if status.ExpireAt == 0 || (status.ExpireAt-bestStatus.ExpireAt > 3600) {
							log.Printf("sticky break platform=%s: sooner expiring acct found uid=%s (exp in %ds) vs current=%s",
								rt.Kind, best.UID, bestStatus.ExpireAt-time.Now().Unix(), sticky.uid)
							acct = best
							h.stickyMu.Lock()
							h.sticky[h.stickyKey(rt.Kind)] = &stickyEntry{uid: acct.UID, maxReqs: defaultMaxReqs}
							h.stickyMu.Unlock()
							return acct
						}
					}
				}

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

type clientContextKeyType struct{}

var clientContextKey = clientContextKeyType{}

type callerInfo struct {
	Name string
	Key  string
}

func (h *Handler) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		masterKey := h.currentAPIKey()
		authz := r.Header.Get("Authorization")
		token := ""
		if strings.HasPrefix(authz, "Bearer ") {
			token = strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
		}

		caller := callerInfo{Name: "Anonymous", Key: token}

		hasClients := h.clientKeys != nil && len(h.clientKeys.List()) > 0
		// 无 masterKey 且无任何 clientKey 时为开放模式
		if masterKey == "" && !hasClients {
			r = r.WithContext(context.WithValue(r.Context(), clientContextKey, caller))
			next(w, r)
			return
		}

		// 匹配 masterKey
		if masterKey != "" && token == masterKey {
			caller.Name = "Default"
			r = r.WithContext(context.WithValue(r.Context(), clientContextKey, caller))
			next(w, r)
			return
		}

		// 匹配 clientKeys
		if h.clientKeys != nil {
			if name, ok := h.clientKeys.Validate(token); ok {
				caller.Name = name
				r = r.WithContext(context.WithValue(r.Context(), clientContextKey, caller))
				next(w, r)
				return
			}
		}

		writeOpenAIError(w, http.StatusUnauthorized, "invalid_api_key", "missing or invalid API key")
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
	seen := map[string]bool{}

	// 首先暴露所有配置的别名模型（如 claude-3-5-sonnet、deepseek-chat 等）
	redirects := h.GetModelRedirects()
	aliasNames := make([]string, 0, len(redirects))
	for name := range redirects {
		aliasNames = append(aliasNames, name)
	}
	sort.Strings(aliasNames)
	for _, name := range aliasNames {
		if !seen[name] {
			seen[name] = true
			out = append(out, map[string]any{
				"id":       name,
				"object":   "model",
				"created":  1753600000,
				"owned_by": "zerogate",
			})
		}
	}

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
			if !seen[id] {
				seen[id] = true
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
	startTime := time.Now()
	caller, _ := r.Context().Value(clientContextKey).(callerInfo)
	if caller.Name == "" {
		caller.Name = "Default"
	}
	keySnippet := ""
	if len(caller.Key) > 10 {
		keySnippet = caller.Key[:6] + "..." + caller.Key[len(caller.Key)-4:]
	} else if caller.Key != "" {
		keySnippet = "***"
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeOpenAIError(w, http.StatusBadRequest, "invalid_request", "read body: "+err.Error())
		return
	}
	var peek struct {
		Model  string `json:"model"`
		Stream bool   `json:"stream"`
		Think  bool   `json:"think"` // chatui 等客户端显式索取思考流（reasoning_content 透传）
	}
	_ = json.Unmarshal(body, &peek)
	requestedModel := strings.TrimSpace(peek.Model)

	// 剔除私有字段 think，避免上游对未知字段报错
	if peek.Think {
		var m map[string]any
		if json.Unmarshal(body, &m) == nil {
			delete(m, "think")
			if nb, err := json.Marshal(m); err == nil {
				body = nb
			}
		}
	}

	// 解析候选列表：命中别名则展开为候选链；否则为单项候选
	redirects := h.GetModelRedirects()
	var candidates []string
	if list, ok := redirects[requestedModel]; ok && len(list) > 0 {
		candidates = append([]string(nil), list...)
	} else {
		candidates = []string{requestedModel}
	}

	var lastErr error
	var lastStatus int = http.StatusServiceUnavailable

	for _, cand := range candidates {
		rt, targetModel, rerr := h.runtimeForModel(cand)
		if rerr != nil {
			lastErr = rerr
			continue
		}

		candBody, rerr := rewriteModel(body, targetModel)
		if rerr != nil {
			lastErr = rerr
			continue
		}

		tried := map[string]bool{}
		candSuccess := false

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
			rc, status, respBody, terr := rt.Upstream.ChatStream(acct, candBody)
			if terr != nil {
				lastErr = terr
				lastStatus = http.StatusBadGateway
				h.stickyClear(rt)
				rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
				continue
			}
			if status >= 400 {
				lastStatus = status
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
				// 限流、排队等待(如 Qoder 10605)、余额不足、会话失效、模型未找到或服务端异常时，继续重试账号池其他账号或备选渠道
				isRateLimit := kind == provider.ErrSoftRate || status == http.StatusTooManyRequests || strings.Contains(string(respBody), "10605")
				if isRateLimit || kind == provider.ErrHardCredit || kind == provider.ErrSessionDead ||
					kind == provider.ErrNotFound || status == http.StatusPaymentRequired || status >= 500 {
					lastErr = fmt.Errorf("upstream %s(%s) status %d: %s", rt.Kind, acct.UID, status, string(respBody))
					if isRateLimit {
						lastStatus = http.StatusTooManyRequests
					}
					continue
				}
				// 业务层非重试错误（例如用户参数格式错误 400），才直接返回客户端
				// 注意：若上游返回 401/403（非 10605 的鉴权失败），绝不可把 401/403 透传给客户端，
				// 否则如 ZCode 等客户端会误判为自己的客户端 Key 失效而崩溃终止会话。应映射为 502 Bad Gateway
				clientStatus := status
				if status == http.StatusUnauthorized || status == http.StatusForbidden {
					clientStatus = http.StatusBadGateway
				}
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(clientStatus)
				if isOpenAIError(respBody) {
					_, _ = w.Write(respBody)
				} else {
					_, _ = w.Write(wrapUpstreamError(clientStatus, respBody))
				}
				if h.auditRec != nil {
					h.auditRec.Record(audit.Entry{
						Client:      caller.Name,
						KeySnippet:  keySnippet,
						ModelReq:    requestedModel,
						ModelTarget: targetModel,
						Channel:     rt.Kind.String(),
						UID:         acct.UID,
						DurationMs:  time.Since(startTime).Milliseconds(),
						Status:      clientStatus,
						Error:       string(respBody),
						Stream:      peek.Stream,
					})
				}
				return
			}

			// 请求成功！
			candSuccess = true
			defer rc.Close()
			rt.Pool.NoteSuccess(acct.UID)
			h.stickySuccess(rt)
			kind, uid := rt.Kind.String(), acct.UID

			if peek.Stream {
				track := &tokenTrackingReader{r: rc}
				// 客户端带 think:true 且渠道支持时透传思考字段（reasoning_content），
				// 否则维持剥离（ZCode 等客户端会把思考当正文渲染）
				if tp, ok := rt.Upstream.(provider.ThinkPassthrough); ok && peek.Think {
					_ = tp.StreamWithThink(w, track, true)
				} else {
					_ = rt.Upstream.Stream(w, track)
				}
				track.finish()
				var p, c, cr, cw int64
				if pr, crk, crd, cwr, ok := track.ScanResult(); ok {
					p, c, cr, cw = pr, crk, crd, cwr
				}
				// 流以 200 干净结束却零 token 且总时长超过 1 分钟：上游实际没吐任何
				// 内容（典型：桥接实例签发卡死挂满自身 120s 超时）。按一次上游错误
				// 计账触发熔断，防止路由持续把流量打进"假成功"的账号；审计记 502。
				degraded := p == 0 && c == 0 && time.Since(startTime) >= time.Minute
				if degraded {
					rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
					log.Printf("zero-token long stream platform=%s uid=%s elapsed=%dms — counted as upstream error",
						rt.Kind, acct.UID, time.Since(startTime).Milliseconds())
				}
				if rec := h.recordTokens; rec != nil && (p > 0 || c > 0) {
					rec(kind, uid, targetModel, p, c, cr, cw)
				}
				if h.clientKeys != nil && (p > 0 || c > 0) {
					h.clientKeys.RecordUsage(caller.Key, p+c)
				}
				if h.auditRec != nil {
					auditStatus := http.StatusOK
					if degraded {
						auditStatus = http.StatusBadGateway
					}
					h.auditRec.Record(audit.Entry{
						Client:           caller.Name,
						KeySnippet:       keySnippet,
						ModelReq:         requestedModel,
						ModelTarget:      targetModel,
						Channel:          kind,
						UID:              uid,
						PromptTokens:     p,
						CompletionTokens: c,
						CacheTokens:      cr,
						TotalTokens:      p + c,
						DurationMs:       time.Since(startTime).Milliseconds(),
						Status:           auditStatus,
						Stream:           true,
					})
				}
				return
			}

			// 非流式
			var resp map[string]any
			var err error
			if tp, ok := rt.Upstream.(provider.ThinkPassthrough); ok && peek.Think {
				resp, err = tp.AggregateWithThink(rc, true)
			} else {
				resp, err = rt.Upstream.Aggregate(rc)
			}
			if err != nil {
				writeOpenAIError(w, http.StatusBadGateway, "upstream_parse", err.Error())
				return
			}
			var p, c, cr, cw int64
			if u, ok := resp["usage"].(map[string]any); ok {
				p, _ = asInt(u["prompt_tokens"])
				c, _ = asInt(u["completion_tokens"])
				if p == 0 && c == 0 {
					if t, ok := asInt(u["total_tokens"]); ok {
						c = t
					}
				}
				cr, _ = asInt(u["prompt_cache_hit_tokens"])
				if cr == 0 {
					cr, _ = asInt(u["cache_read_input_tokens"])
				}
				if cr == 0 {
					cr, _ = asInt(u["cached_tokens"])
				}
				cw, _ = asInt(u["prompt_cache_write_tokens"])
				if cw == 0 {
					cw, _ = asInt(u["cache_creation_input_tokens"])
				}
			}
			if rec := h.recordTokens; rec != nil && (p > 0 || c > 0) {
				rec(kind, uid, targetModel, p, c, cr, cw)
			}
			if h.clientKeys != nil && (p > 0 || c > 0) {
				h.clientKeys.RecordUsage(caller.Key, p+c)
			}
			if h.auditRec != nil {
				h.auditRec.Record(audit.Entry{
					Client:           caller.Name,
					KeySnippet:       keySnippet,
					ModelReq:         requestedModel,
					ModelTarget:      targetModel,
					Channel:          kind,
					UID:              uid,
					PromptTokens:     p,
					CompletionTokens: c,
					CacheTokens:      cr,
					TotalTokens:      p + c,
					DurationMs:       time.Since(startTime).Milliseconds(),
					Status:           http.StatusOK,
					Stream:           false,
				})
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}

		if candSuccess {
			return
		}
		log.Printf("model candidate %q exhausted, trying next candidate in fallback chain...", cand)
	}

	msg := "all accounts/candidates unavailable (cooling/disabled)"
	if lastErr != nil {
		msg += ": " + lastErr.Error()
	}
	if h.auditRec != nil {
		h.auditRec.Record(audit.Entry{
			Client:     caller.Name,
			KeySnippet: keySnippet,
			ModelReq:   requestedModel,
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     lastStatus,
			Error:      msg,
			Stream:     peek.Stream,
		})
	}
	if lastStatus == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "2")
	}
	writeOpenAIError(w, lastStatus, "no_healthy_account", msg)
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
	// 1. OpenAI 兼容：行内嵌 "usage":{...}（扁平结构：zcode/glm、trae 等）
	if m := usageObjRe.FindSubmatch(line); m != nil {
		t.has = true
		for _, kv := range usageNumRe.FindAllSubmatch(m[1], -1) {
			v, _ := strconv.ParseInt(string(kv[2]), 10, 64)
			t.applyField(string(kv[1]), v)
		}
		return
	}
	// 2. data:{...} 行：SOLO 原生或 qoder 嵌套封装
	m := dataObjRe.FindSubmatch(line)
	if m == nil {
		return
	}
	var obj map[string]any
	if json.Unmarshal(m[1], &obj) != nil {
		return
	}
	// 2a. qoder：data:{"body":"<内层 JSON 字符串>"} —— usage 藏在转义的内层 JSON 里，
	//     且 usage 自带嵌套 details 对象，正则截断不可靠，必须完整解析。
	if body, ok := obj["body"].(string); ok && body != "" {
		var inner map[string]any
		if json.Unmarshal([]byte(body), &inner) != nil {
			return
		}
		if u, ok := inner["usage"].(map[string]any); ok {
			t.has = true
			t.applyUsageMap(u)
		}
		return
	}
	// 2b. SOLO 原生：{"prompt_tokens":..,"completion_tokens":..}（无 usage 键）
	if p, ok := obj["prompt_tokens"].(float64); ok {
		t.has = true
		t.applyField("prompt_tokens", int64(p))
		if c, ok := obj["completion_tokens"].(float64); ok {
			t.applyField("completion_tokens", int64(c))
		}
		if tt, ok := obj["total_tokens"].(float64); ok {
			t.applyField("total_tokens", int64(tt))
		}
		if cr, ok := obj["prompt_cache_hit_tokens"].(float64); ok {
			t.applyField("prompt_cache_hit_tokens", int64(cr))
		} else if cr, ok := obj["cache_read_input_tokens"].(float64); ok {
			t.applyField("cache_read_input_tokens", int64(cr))
		} else if cr, ok := obj["cached_tokens"].(float64); ok {
			t.applyField("cached_tokens", int64(cr))
		}
		if cw, ok := obj["prompt_cache_write_tokens"].(float64); ok {
			t.applyField("prompt_cache_write_tokens", int64(cw))
		} else if cw, ok := obj["cache_creation_input_tokens"].(float64); ok {
			t.applyField("cache_creation_input_tokens", int64(cw))
		}
	}
}

// applyField 按 usage 字段名写入对应计数。
func (t *tokenTrackingReader) applyField(name string, v int64) {
	switch name {
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

// applyUsageMap 从完整 usage 对象提取字段（qoder 嵌套 details 结构安全）。
func (t *tokenTrackingReader) applyUsageMap(u map[string]any) {
	for _, k := range []string{
		"prompt_tokens", "completion_tokens", "total_tokens",
		"prompt_cache_hit_tokens", "cache_read_input_tokens", "cached_tokens",
		"prompt_cache_write_tokens", "cache_creation_input_tokens",
	} {
		if v, ok := asInt(u[k]); ok {
			t.applyField(k, v)
		}
	}
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

// isOpenAIError 判断响应体是否为 OpenAI 标准错误格式（含 error.message）。
func isOpenAIError(raw []byte) bool {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	return json.Unmarshal(raw, &e) == nil && e.Error.Message != ""
}

// wrapUpstreamError 把非标准上游错误体包装成 OpenAI 标准 error 格式。
// message 依次尝试常见字段（error.message / message / msg / errMsg），
// 原始体放进 upstream_body 供调试。
func wrapUpstreamError(status int, raw []byte) []byte {
	msg := ""
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		if e, ok := obj["error"].(map[string]any); ok {
			if m, _ := e["message"].(string); m != "" {
				msg = m
			}
		}
		if msg == "" {
			for _, k := range []string{"message", "msg", "errMsg", "err_msg", "error_msg"} {
				if m, ok := obj[k].(string); ok && m != "" {
					msg = m
					break
				}
			}
		}
	}
	if msg == "" {
		msg = "upstream error (status " + http.StatusText(status) + ")"
	}
	out, _ := json.Marshal(map[string]any{
		"error": map[string]any{
			"message":       msg,
			"type":          "upstream_error",
			"code":          fmt.Sprintf("http_%d", status),
			"upstream_body": string(raw),
		},
	})
	return out
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
		Prompt     int64 `json:"prompt_tokens"`
		Completion int64 `json:"completion_tokens"`
		CacheRead  int64 `json:"cache_read_tokens"`
		CacheWrite int64 `json:"cache_write_tokens"`
		Total      int64 `json:"total_tokens"`
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
	// 按模型累计（供前端按模型官方单价折算节省金额）；
	// 旧格式 CSV 行没有模型名，记成 "<渠道> (历史)"，前端按渠道主力模型档估算。
	modelStats := make(map[string]*tokenStats)
	for _, au := range report {
		for _, mu := range au.Models {
			key := mu.Model
			if key == "" {
				key = au.Kind + " (历史)"
			}
			ms := modelStats[key]
			if ms == nil {
				ms = &tokenStats{}
				modelStats[key] = ms
			}
			ms.Prompt += mu.Prompt
			ms.Completion += mu.Completion
			ms.CacheRead += mu.CacheRead
			ms.CacheWrite += mu.CacheWrite
			ms.Total = ms.Prompt + ms.Completion + ms.CacheRead + ms.CacheWrite
		}
	}
	out := map[string]any{
		"channels": stats,
		"models":   modelStats,
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
