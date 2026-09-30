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

	"zerogate/internal/audit"
	"zerogate/internal/auth"
	"zerogate/internal/clientkey"
	"zerogate/internal/limiter"
	"zerogate/internal/pool"
	"zerogate/internal/provider"
	"zerogate/internal/usage"
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
	ModelMerge     map[string][]string // 跨渠道合并路由：同款自动优选到期最近/余额最低的账号

	// RateLimit 本地小时窗限速器（可空 = 不限速），每 (渠道, 账号) 300 req/h。
	RateLimit *limiter.Limiter
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

	// breakerMu 保护 breaker：渠道级熔断（runtimeKind → 熔断到期时间）。
	// 整池不可用（全部封禁/冷却/停用）时置位，期间直接快速失败、不再转发上游 ——
	// 防止客户端重试风暴把"账号已被封禁"反复打给上游而加深风控标记。
	breakerMu sync.Mutex
	breaker   map[string]time.Time

	recordTokens func(kind, uid, model string, prompt, completion, cacheRead, cacheWrite int64)

	clientKeys     *clientkey.Store
	auditRec       *audit.Recorder
	redirectsMu    sync.RWMutex
	modelRedirects map[string][]string
	modelMerge     map[string][]string
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

func (h *Handler) GetModelMerge() map[string][]string {
	h.redirectsMu.RLock()
	defer h.redirectsMu.RUnlock()
	if h.modelMerge == nil {
		return nil
	}
	out := make(map[string][]string, len(h.modelMerge))
	for k, v := range h.modelMerge {
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
		breaker:        make(map[string]time.Time),
		recordTokens:   cfg.RecordTokens,
		clientKeys:     cfg.ClientKeys,
		auditRec:       cfg.AuditRec,
		modelRedirects: cfg.ModelRedirects,
		modelMerge:     cfg.ModelMerge,
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

// ---------------------------------------------------------------------------
// 渠道级熔断（2026-09-23）
//
// 背景：客户端（ZCode / Trae 等）在请求失败后会自行重试，间隔可短至 8 秒。
// 若某渠道整池都不可用（账号全部封禁/冷却/停用），daemon 仍会为每次重试
// 向上一一转发，上游看到的是"凭证已失效却持续暴力请求" —— AutoClaw 两个
// 账号即因此被判定风控封禁（410004）。
//
// 熔断语义：整池不可用时置位，冷却期内直接快速失败（不转发上游）；
// 到期自动放行一次探测（半开），成功则清除、失败则重新置位。
// ---------------------------------------------------------------------------

// breakerCooldown 熔断保持时长：整池不可用后，多久内不再转发上游。
const breakerCooldown = 60 * time.Second

// breakerOpen 判断渠道是否处于熔断中（未到期 = 仍然熔断）。
func (h *Handler) breakerOpen(kind provider.Kind) (bool, time.Duration) {
	h.breakerMu.Lock()
	defer h.breakerMu.Unlock()
	until, ok := h.breaker[string(kind)]
	if !ok {
		return false, 0
	}
	if d := time.Until(until); d > 0 {
		return true, d
	}
	// 已到期：清除记录，放行本次请求（半开探测）
	delete(h.breaker, string(kind))
	return false, 0
}

// breakerTrip 置位熔断（整池不可用时调用）。
func (h *Handler) breakerTrip(kind provider.Kind) {
	h.breakerMu.Lock()
	defer h.breakerMu.Unlock()
	h.breaker[string(kind)] = time.Now().Add(breakerCooldown)
}

// breakerReset 清除熔断（该渠道恢复成功时调用）。
func (h *Handler) breakerReset(kind provider.Kind) {
	h.breakerMu.Lock()
	defer h.breakerMu.Unlock()
	delete(h.breaker, string(kind))
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
	merges := h.GetModelMerge()
	aliasNames := make([]string, 0, len(redirects)+len(merges))
	for name := range merges {
		aliasNames = append(aliasNames, name)
	}
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
	merges := h.GetModelMerge()
	var candidates []string
	var isMerge bool
	if list, ok := redirects[requestedModel]; ok && len(list) > 0 {
		candidates = append([]string(nil), list...)
	} else if list, ok := merges[requestedModel]; ok && len(list) > 0 {
		candidates = append([]string(nil), list...)
		isMerge = true
	} else {
		candidates = []string{requestedModel}
	}
	// 合并路由模式：提前跨候选找最优账号。找到直接执行一轮；否则回退到传统降级链
	var mergeBestAcct *auth.Auth
	var mergeBestRT *Runtime
	var mergeBestTarget string
	var mergeBestBody []byte
	if isMerge {
		mergeBestAcct, mergeBestRT, mergeBestTarget, mergeBestBody = h.pickBestAcross(candidates, body)
		if mergeBestAcct == nil || mergeBestRT == nil {
			isMerge = false
		}
	}

	var lastErr error
	var lastStatus int = http.StatusServiceUnavailable
	var lastCandKind provider.Kind
	totalRateLimited := 0
	streamRetried := false // 零 token 上游断流重试标记：整请求上限 1 次，防重试风暴

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

		// 渠道级熔断：整池不可用（全部封禁/冷却/停用）时不转发上游 ——
		// 客户端重试风暴下继续打上游只会加深风控标记。冷却期内快速失败。
		// 记 lastCandKind：熔断路径也要在审计里留下渠道名，否则排查时只看到空渠道。
		if open, wait := h.breakerOpen(rt.Kind); open {
			lastErr = fmt.Errorf("%s 渠道熔断中（账号池整体不可用，%.0fs 后自动重试）", rt.Kind, wait.Seconds())
			lastStatus = http.StatusServiceUnavailable
			lastCandKind = rt.Kind
			log.Printf("channel breaker open platform=%s wait=%.0fs (skip upstream)", rt.Kind, wait.Seconds())
			continue
		}
		// 整池健康数 = 0：无需尝试，直接置位熔断并快速失败
		if healthy, total := rt.Pool.HealthyCount(); total > 0 && healthy == 0 {
			h.breakerTrip(rt.Kind)
			lastErr = fmt.Errorf("%s 渠道账号池整体不可用（%d 个账号全部禁用/冷却），已暂停转发", rt.Kind, total)
			lastStatus = http.StatusServiceUnavailable
			lastCandKind = rt.Kind
			log.Printf("channel breaker trip platform=%s (%d/%d healthy) — 暂停转发 %v", rt.Kind, healthy, total, breakerCooldown)
			continue
		}

		tried := map[string]bool{}
		candSuccess := false
		rateLimited := 0 // 被本地限速跳过的账号数

		for i := 0; i < h.cfg.MaxRotate; i++ {
			var acct *auth.Auth
			if isMerge && mergeBestAcct != nil {
				acct = mergeBestAcct
				mergeBestAcct = nil
				rt = mergeBestRT
				targetModel = mergeBestTarget
				candBody = mergeBestBody
			} else {
				acct = h.pickWithSticky(rt)
			}
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
			// 本地小时窗限速（GLM_proxy 300 req/h）：超限号跳过不报错，
			// 记入 tried 驱动轮换到下一号；全池超限时循环结束走 429。
			if h.cfg.RateLimit != nil && !h.cfg.RateLimit.Allow(rt.Kind.String(), acct.UID) {
				log.Printf("local rate limit skip platform=%s uid=%s", rt.Kind, acct.UID)
				if !tried[acct.UID] {
					tried[acct.UID] = true
					rateLimited++
				}
				h.stickyClear(rt)
				continue
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
				var ue *provider.Error
				if errors.As(terr, &ue) && ue.Kind == provider.ErrBridgeDown {
					// 连不上本地桥（桥进程没跑）：错在桥，不在账号。只给一次短冷却
					// 让整池不可用时熔断能快速失败，绝不累计 errCount —— 否则桥一挂
					// 就会把整池账号各罚一次长冷却（2026-09-30 zcode 5 号全冻 10 分钟）。
					log.Printf("bridge down platform=%s uid=%s: %v", rt.Kind, acct.UID, terr)
					rt.Pool.Cooldown(acct.UID, pool.CoolSoft, h.cfg.SoftCooldown, "本地桥不可达（非账号问题）")
					continue
				}
				rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
				continue
			}
			// 诊断（ZG_DUMP_BODY=1 时生效）：落盘实际转发给上游的请求体，
			// 用于排查 11128 等"渠道校验不通过"类拒绝的具体触发字段。
			// 默认只落 4xx/5xx；ZG_DUMP_ALL=1 时才连 2xx 成功请求也落，便于
			// 拿成功 vs 失败两条请求体形态对拍（如 Trae vs ZCode 提示词差异）。
			ensureDumpInit()
			dumpRequestBody(rt.Kind.String(), targetModel, status, respBody, candBody)
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
				case provider.ErrBanned:
					// 410004 等封号信号：24h 长冷却，累计 3 次自动禁用
					rt.Pool.Ban(acct.UID, "上游封禁（410004）")
				case provider.ErrNotFound:
					rt.Pool.Cooldown(acct.UID, pool.CoolSoft, h.cfg.SoftCooldown, "upstream 404")
				default:
					rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
				}
				// 限流、排队等待(如 Qoder 10605)、余额不足、会话失效、封禁、模型未找到或服务端异常时，继续重试账号池其他账号或备选渠道
				isRateLimit := kind == provider.ErrSoftRate || status == http.StatusTooManyRequests || strings.Contains(string(respBody), "10605")
				if isRateLimit || kind == provider.ErrHardCredit || kind == provider.ErrSessionDead ||
					kind == provider.ErrBanned ||
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

			// 空流先验门控（2026-09-28 qoder 故障）：上游偶发返回 HTTP 200 + 空流
			// （几百毫秒内 0 token EOF 结束，如 qoder 的 streamAsOpenAI 会忠实补上
			// [DONE]，客户端拿到的是"合法的空回复"而非错误）。此处对流做一次带字节
			// 上限的窥探：流结束且从未出现任何 SSE data: 行 → 判定空流，按上游错误
			// 处理（stickyClear + NoteError），轮换下一账号重试。窥探时机在写任何
			// 响应头之前，重试对客户端完全无感；窥探到的字节经 peekReader 原样续传，
			// 正常流首包零额外等待。
			var prc io.ReadCloser = rc
			if peek.Stream {
				empty, head, perr := sseStreamLooksEmpty(rc)
				if perr != nil {
					lastErr = fmt.Errorf("%s(%s) 流读取失败: %w", rt.Kind, acct.UID, perr)
					lastStatus = http.StatusBadGateway
					_ = rc.Close()
					h.stickyClear(rt)
					rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
					continue
				}
				if empty {
					lastErr = fmt.Errorf("%s(%s) 上游返回空流（200 但无任何 data 帧）", rt.Kind, acct.UID)
					lastStatus = http.StatusBadGateway
					_ = rc.Close()
					log.Printf("empty stream platform=%s uid=%s elapsed=%dms — rotate to next account",
						rt.Kind, acct.UID, time.Since(startTime).Milliseconds())
					h.stickyClear(rt)
					rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
					continue
				}
				prc = &peekedStream{ReadCloser: rc, head: head}
			}

			// 请求成功！
			candSuccess = true
			defer rc.Close()
			if h.cfg.RateLimit != nil {
				h.cfg.RateLimit.Hit(rt.Kind.String(), acct.UID)
			}
			rt.Pool.NoteSuccess(acct.UID)
			h.stickySuccess(rt)
			h.breakerReset(rt.Kind) // 渠道恢复可用：清除熔断
			kind, uid := rt.Kind.String(), acct.UID

			if peek.Stream {
				track := &tokenTrackingReader{r: prc}
				// 客户端带 think:true 且渠道支持时透传思考字段（reasoning_content），
				// 否则维持剥离（ZCode 等客户端会把思考当正文渲染）。
				// 用 recordingWriter 包装下游：记录"是否已向客户端写出字节"，
				// 零 token 断流重试只允许发生在客户端尚未收到任何数据时。
				rw := newRecordingWriter(w)
				var streamErr error
				if tp, ok := rt.Upstream.(provider.ThinkPassthrough); ok && peek.Think {
					streamErr = tp.StreamWithThink(rw, track, true)
				} else {
					streamErr = rt.Upstream.Stream(rw, track)
				}
				track.finish()
				// 提前结算 token 统计与降级判定（断流分支同样需要）：
				// 上游自报了 usage 却 prompt/completion 全 0 = 没吐任何内容的"假成功"。
				var p, c, cr, cw int64
				if pr, crk, crd, cwr, ok := track.ScanResult(); ok {
					p, c, cr, cw = pr, crk, crd, cwr
				}
				degraded := track.has && p == 0 && c == 0
				// 上游中途断流（超时/连接重置）：下游只看到连接关闭会当成"正常结束"，
				// 必须补一个 SSE error 事件让客户端知道是失败而非模型说完了。
				// 事件体必须是 OpenAI 标准 error 对象结构（error 为对象而非裸字符串），
				// 否则严格校验的客户端（zod invalid_union）会直接判定响应非法。
				if streamErr != nil {
					log.Printf("stream aborted platform=%s uid=%s elapsed=%dms err=%v",
						rt.Kind, acct.UID, time.Since(startTime).Milliseconds(), streamErr)
					// 零 token 断流自动重试：仅限"连接瞬断"（EOF 类）且客户端未收到任何字节时
					// 才换号重试；认证/风控类错误（401/403/405/10605/token 过期等）根本到不了
					// 这里（响应头阶段就按 status>=400 处理了），即使到达也一律不重试，
					// 避免重试风暴加深上游风控标记（AutoClaw 封号教训）。每请求上限 1 次。
					if degraded && !streamRetried && !rw.wroteAny && isStreamEOF(streamErr) {
						streamRetried = true
						rt.Pool.NoteError(acct.UID, h.cfg.ErrThreshold, h.cfg.ErrCooldown)
						h.stickyClear(rt)
						log.Printf("zero-token stream retry platform=%s uid=%s elapsed=%dms err=%v (retry #1)",
							rt.Kind, acct.UID, time.Since(startTime).Milliseconds(), streamErr)
						continue
					}
					msg := "上游连接中断：" + streamErr.Error()
					if payload, jerr := json.Marshal(map[string]any{
						"error": map[string]any{
							"message": msg,
							"type":    "upstream_error",
							"code":    "stream_aborted",
						},
					}); jerr == nil {
						if _, werr := io.WriteString(w, "data: "+string(payload)+"\n\n"); werr == nil {
							if fl, ok := w.(http.Flusher); ok {
								fl.Flush()
							}
						}
					}
				}
				// 上游自报了 usage 却 prompt/completion 全 0：无论流时长，都是没吐
				// 任何内容的"假成功"（快空流兜底 + 桥接实例卡死挂满超时的长流）。
				// 按一次上游错误计账触发熔断，防止路由持续把流量打进假成功账号。
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
						UsageRaw:         track.UsageRaw(),
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
			var usageRawSnippet string
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
				// OpenAI 风格嵌套：cached_tokens 藏在 prompt_tokens_details 里（qoder 实测形态）
				if cr == 0 {
					if d, ok := u["prompt_tokens_details"].(map[string]any); ok {
						cr, _ = asInt(d["cached_tokens"])
					}
				}
				if raw, err := json.Marshal(u); err == nil && len(raw) <= 512 {
					usageRawSnippet = string(raw)
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
					UsageRaw:         usageRawSnippet,
				})
			}
			writeJSON(w, http.StatusOK, resp)
			return
		}

		if candSuccess {
			return
		}
		totalRateLimited += rateLimited
		lastCandKind = rt.Kind
		log.Printf("model candidate %q exhausted, trying next candidate in fallback chain...", cand)
	}

	msg := "all accounts/candidates unavailable (cooling/disabled)"
	if lastErr != nil {
		msg += ": " + lastErr.Error()
	}
	if totalRateLimited > 0 {
		// 全部候选耗尽且发生过本地限速跳号：按 429 返回，Retry-After 估到小时窗重置
		lastStatus = http.StatusTooManyRequests
		msg = fmt.Sprintf("%s（本地限速跳过 %d 次）", msg, totalRateLimited)
	}
	if h.auditRec != nil {
		// Channel/UID 一并记录：失败请求此前不落渠道，排查"是谁在打、打到哪个渠道"
		// 时只能翻 app.log（2026-09-23 AutoClaw 封号排查即卡在这里）。
		h.auditRec.Record(audit.Entry{
			Client:     caller.Name,
			KeySnippet: keySnippet,
			ModelReq:   requestedModel,
			Channel:    string(lastCandKind),
			DurationMs: time.Since(startTime).Milliseconds(),
			Status:     lastStatus,
			Error:      msg,
			Stream:     peek.Stream,
		})
	}
	if lastStatus == http.StatusTooManyRequests {
		if totalRateLimited > 0 && h.cfg.RateLimit != nil {
			if d := h.cfg.RateLimit.RetryAfter(string(lastCandKind), ""); d > 0 {
				w.Header().Set("Retry-After", strconv.Itoa(int((d+time.Second-1)/time.Second)))
			}
		} else {
			w.Header().Set("Retry-After", "2")
		}
	}
	writeOpenAIError(w, lastStatus, "no_healthy_account", msg)
}

// pickBestAcross 跨候选渠道选择最优账号：到期最近优先，余额最低次之。
// 返回 (acct, rt, targetModel, candBody)；全枯竭返回 nil。
func (h *Handler) pickBestAcross(candidates []string, requestBody []byte) (*auth.Auth, *Runtime, string, []byte) {
	if len(candidates) == 0 {
		return nil, nil, "", nil
	}
	var bestAcct *auth.Auth
	var bestRT *Runtime
	var bestTarget string
	var bestBody []byte
	var bestExpire int64
	var bestCredits int64
	for _, cand := range candidates {
		rt, targetModel, err := h.runtimeForModel(cand)
		if err != nil {
			continue
		}
		acct := rt.Pool.Pick()
		if acct == nil {
			continue
		}
		candBody, err := rewriteModel(requestBody, targetModel)
		if err != nil {
			continue
		}
		st, ok := rt.Pool.Status(acct.UID)
		if !ok || st.Disabled || st.Cooling {
			continue
		}
		if bestAcct == nil {
			bestAcct, bestRT, bestTarget, bestBody = acct, rt, targetModel, candBody
			bestExpire, bestCredits = st.ExpireAt, st.Credits
			continue
		}
		if bestExpire == 0 && st.ExpireAt > 0 ||
			bestExpire > 0 && st.ExpireAt > 0 && st.ExpireAt < bestExpire ||
			st.ExpireAt == bestExpire && st.Credits < bestCredits {
			bestAcct, bestRT, bestTarget, bestBody = acct, rt, targetModel, candBody
			bestExpire, bestCredits = st.ExpireAt, st.Credits
		}
	}
	return bestAcct, bestRT, bestTarget, bestBody
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
	usageRaw   string // 最后一次见到的 usage 原文（审计取证：上游到底报没报缓存）
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

// UsageRaw 返回流中最后一次见到的 usage 原文，供审计取证（上游是否回传缓存字段）。
func (t *tokenTrackingReader) UsageRaw() string { return t.usageRaw }

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
		t.usageRaw = string(m[0])
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
			if raw, err := json.Marshal(u); err == nil {
				t.usageRaw = string(raw)
			}
			t.applyUsageMap(u)
		}
		return
	}
	// 2b. SOLO 原生：{"prompt_tokens":..,"completion_tokens":..}（无 usage 键）
	if p, ok := obj["prompt_tokens"].(float64); ok {
		t.has = true
		if raw, err := json.Marshal(obj); err == nil {
			t.usageRaw = string(raw)
		}
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
	// OpenAI 风格嵌套：cached_tokens 藏在 prompt_tokens_details 里（qoder 实测形态）
	if d, ok := u["prompt_tokens_details"].(map[string]any); ok {
		if v, ok := asInt(d["cached_tokens"]); ok && v > t.cacheRead {
			t.cacheRead = v
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

// peekedStream 把窥探阶段读走的字节先吐给下游，之后透传底层 reader。
type peekedStream struct {
	io.ReadCloser
	head []byte
	off  int
}

// recordingWriter 包装 http.ResponseWriter，记录是否已向客户端写出任何字节。
// 用于零 token 断流重试：只有客户端尚未收到数据（wroteAny=false）时才允许重试，
// 否则 SSE 协议已被第一个 chunk 推进，重试会让客户端收到两条不连续流。
// 实现 http.Flusher 转发，保证 qoder 等上游的 Flush 行为不丢。
type recordingWriter struct {
	http.ResponseWriter
	flusher  http.Flusher
	wroteAny bool
}

func newRecordingWriter(w http.ResponseWriter) *recordingWriter {
	rw := &recordingWriter{ResponseWriter: w}
	if fl, ok := w.(http.Flusher); ok {
		rw.flusher = fl
	}
	return rw
}

func (rw *recordingWriter) WriteHeader(code int) {
	rw.wroteAny = true
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *recordingWriter) Write(b []byte) (int, error) {
	if len(b) > 0 {
		rw.wroteAny = true
	}
	return rw.ResponseWriter.Write(b)
}

func (rw *recordingWriter) Flush() {
	if rw.flusher != nil {
		rw.flusher.Flush()
	}
}

// isStreamEOF 判断上游断流是否属于"连接瞬断"（EOF 类）。
// 只有这类错误允许零 token 自动重试；认证/风控类错误（401/403/405/10605
// 等）即使意外到达这里也一律不匹配，绝不重试，避免加深上游风控标记。
func isStreamEOF(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "unexpected eof") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "closed")
}

func (p *peekedStream) Read(b []byte) (int, error) {
	if p.off < len(p.head) {
		n := copy(b, p.head[p.off:])
		p.off += n
		return n, nil
	}
	return p.ReadCloser.Read(b)
}

const maxPeekBytes = 1 << 20

// sseStreamLooksEmpty 窥探一个刚拿到手的 200 流，判断它是否为"空流"：
// 流结束（EOF/错误）且从未出现任何 SSE data: 行 → 空流。
// 窥探在出现首个 data 帧或读满 maxPeekBytes 时立即停止，正常流只付首包
// 的等待时间；窥探到的字节经 head 返回，由 peekedStream 注回转发链路。
func sseStreamLooksEmpty(rc io.ReadCloser) (empty bool, head []byte, err error) {
	chunk := make([]byte, 4096)
	var buf []byte
	var tail []byte // 前一次读的末尾 ≤4 字节：保证跨 chunk 边界的 data: 也能检出
	for {
		n, rerr := rc.Read(chunk)
		if n > 0 {
			buf = append(buf, chunk[:n]...)
			search := chunk[:n]
			if len(tail) > 0 {
				search = make([]byte, 0, len(tail)+n)
				search = append(search, tail...)
				search = append(search, chunk[:n]...)
			}
			if bytes.Contains(search, []byte("data:")) {
				return false, buf, nil
			}
			if len(buf) > maxPeekBytes {
				// 迟迟不见 data 帧：交还上层如实转发，不在窥探阶段下判断
				return false, buf, nil
			}
			tail = append(tail, chunk[:n]...)
			if len(tail) > 4 {
				tail = tail[len(tail)-4:]
			}
		}
		if rerr != nil {
			if errors.Is(rerr, io.EOF) {
				return true, buf, nil
			}
			return false, buf, rerr
		}
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
