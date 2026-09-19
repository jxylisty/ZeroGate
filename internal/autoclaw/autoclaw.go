// Package autoclaw 实现 AutoClaw（Z.ai/智谱）官方代理通道的上游客户端。
//
// 通道说明：AutoClaw 桌面端走官方 OpenAI 兼容代理
//
//	POST https://autoglm-acceleration-api.zhipuai.cn/autoclaw-proxy/proxy/autoclaw/chat/completions
//
// 关键鉴权头（从官方桌面客户端 app.asar 逆向）：
//   - X-Authorization: Bearer <accessToken>（注意不是标准 Authorization）
//   - X-Harness-Type: zcode  —— 豁免 api-proxy 系统提示词白名单校验（缺失会 400 invalid request）
//   - X-Client-Type: pc、X-Product: autoclaw、X-Tm: win32、X-Version、X-Lang、x_trace_id: autoclaw-desktop
//
// 用户 API（积分/刷新）走 userapi 签名头：
//
//	X-Auth-Sign = MD5("100003&<unix秒>&38d2391985e2369a5fb8227d8e6cd5e5")
package autoclaw

import (
	"bytes"
	"crypto/md5"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
	"wild-work/internal/upstream"
)

// 渠道常量（上游协议逆向值，勿随意改）。
const (
	// AppID / AppSecret 来自官方 webElectronApi 常量。
	AppID       = "100003"
	AppSecret   = "38d2391985e2369a5fb8227d8e6cd5e5"
	ClientVer   = "1.12.1"
	ProxyBase   = "https://autoglm-acceleration-api.zhipuai.cn"
	ProxyPath   = "/autoclaw-proxy/proxy/autoclaw"
	ChatPath    = ProxyPath + "/chat/completions"
	TaskCompletePath = ProxyPath + "/autoclaw-task-complete"
	WalletsPath = "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw"
	ScopePath   = "/agent-assetmgr/api/v1/wallet-instances?wallet_type=all&wallet_scope=all"
	RefreshPath = "/userapi/v1/refresh"
	// ModelConfigPath 官方模型配置接口：动态拉取当前可用模型列表（含 glm-5.3-flash 等新模型）。
	ModelConfigPath = "/agent-assetmgr/api/v1/model-config"
)

// Client AutoClaw 上游客户端。
type Client struct {
	HTTP    *http.Client
	Base    string // userapi/proxy 基址（autoglm-acceleration-api.zhipuai.cn）
	Channel string // "autoclaw"，费率分组
	Version string // X-Version（与官方桌面端一致）
}

// New 生产默认。
func New() *Client {
	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		HTTP:    &http.Client{Timeout: 180 * time.Second, Transport: tr},
		Base:    ProxyBase,
		Channel: "autoclaw",
		Version: ClientVer,
	}
}

// ---------------------------------------------------------------------------
// 错误分类
// ---------------------------------------------------------------------------

// ErrKind 复用 provider 语义。
const (
	ErrNone        = provider.ErrNone
	ErrHardCredit  = provider.ErrHardCredit
	ErrSoftRate    = provider.ErrSoftRate
	ErrSessionDead = provider.ErrSessionDead
	ErrNotFound    = provider.ErrNotFound
	ErrServer      = provider.ErrServer
	ErrClient      = provider.ErrClient
)

// Classify 按 HTTP 状态码 + body 判定错误类别。
func (c *Client) Classify(status int, body string) provider.ErrKind { return Classify(status, body) }

// Classify 独立分类（供测试直接调用）。
func Classify(status int, body string) provider.ErrKind {
	low := strings.ToLower(body)
	switch {
	case status == http.StatusUnauthorized || strings.Contains(low, "invalid token") || strings.Contains(low, "登录") && status == http.StatusUnauthorized:
		return ErrSessionDead
	case status == http.StatusForbidden:
		// 403 可能是 WAF/风控：短冷却口风，不永久禁用
		return ErrClient
	case status == http.StatusTooManyRequests || strings.Contains(low, "rate limit"):
		return ErrSoftRate
	case status >= 500:
		return ErrServer
	case status == http.StatusNotFound:
		return ErrNotFound
	case status >= 400:
		return ErrClient
	}
	return ErrNone
}

// ---------------------------------------------------------------------------
// 签名与请求头
// ---------------------------------------------------------------------------

// sign 计算 userapi 签名 X-Auth-Sign = MD5("appid&ts&secret")。
func sign(ts string) string {
	sum := md5.Sum([]byte(fmt.Sprintf("%s&%s&%s", AppID, ts, AppSecret)))
	return hex.EncodeToString(sum[:])
}

// userapiHeaders userapi（wallets/refresh）签名请求头；token 走标准 Authorization。
func userapiHeaders(rawToken string) http.Header {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "*/*")
	h.Set("X-Version", ClientVer)
	h.Set("X-Tm", "web")
	h.Set("X-Product", "autoclaw")
	h.Set("X-Client-Type", "web")
	h.Set("X-Channel", "official")
	h.Set("X-Auth-Appid", AppID)
	h.Set("X-Auth-TimeStamp", ts)
	h.Set("X-Auth-Sign", sign(ts))
	h.Set("X-Trace-Id", newTraceID())
	h.Set("X-Lang", "en")
	if rawToken != "" {
		h.Set("Authorization", "Bearer "+rawToken)
	}
	return h
}

// proxyHeaders 官方代理端点请求头；token 走 X-Authorization。
func (c *Client) proxyHeaders(rawToken, model string) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "text/event-stream")
	h.Set("X-Authorization", "Bearer "+rawToken)
	h.Set("X-Request-Id", newTraceID())
	h.Set("X-Request-Model", model)
	h.Set("X-Client-Type", "pc")
	h.Set("X-Product", "autoclaw")
	h.Set("X-Harness-Type", "zcode") // 豁免 api-proxy 系统提示词白名单校验（缺失 → 400 invalid request）
	h.Set("X-Tm", "win32")
	h.Set("X-Version", c.Version)
	h.Set("X-Lang", "en")
	h.Set("x_trace_id", "autoclaw-desktop")
	return h
}

// newTraceID 生成 16 字节随机 hex（与官方 X-Trace-Id 一致）。
func newTraceID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

// randRead 占位（保留语义：随机源）。
var _ = randRead

func randRead(b []byte) (int, error) { return rand.Read(b) }

// rawToken 由 auth 剥离 Bearer 前缀（磁盘凭证可能带前缀）。
func rawToken(a *auth.Auth) string {
	t := strings.TrimSpace(a.AccessToken)
	if len(t) >= 7 && strings.EqualFold(t[:7], "Bearer ") {
		return t[7:]
	}
	return t
}

// ---------------------------------------------------------------------------
// provider.Upstream 接口
// ---------------------------------------------------------------------------

// RefreshToken 用 refresh_token 换取新令牌。
func (c *Client) RefreshToken(a *auth.Auth) error {
	a.Lock()
	defer a.Unlock()
	if strings.TrimSpace(a.RefreshToken) == "" {
		return fmt.Errorf("no refreshToken")
	}
	body, _ := json.Marshal(map[string]any{
		"source_id":     "autoclaw",
		"device_id":     a.DeviceID,
		"refresh_token": strings.TrimPrefix(a.RefreshToken, "Bearer "),
	})
	req, err := http.NewRequest(http.MethodPost, c.Base+RefreshPath, bytes.NewReader(body))
	if err != nil {
		return err
	}
	for k, vs := range userapiHeaders(rawToken(a)) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("refresh failed (http %d): %s", resp.StatusCode, truncate(string(raw), 160))
	}
	var env struct {
		Code int64 `json:"code"`
		Data struct {
			AccessToken  string `json:"access_token"`
			RefreshToken string `json:"refresh_token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("refresh parse: %w", err)
	}
	if env.Code != 0 || env.Data.AccessToken == "" {
		return fmt.Errorf("refresh_failed: code=%d (re-login required)", env.Code)
	}
	a.AccessToken = env.Data.AccessToken
	if env.Data.RefreshToken != "" {
		a.RefreshToken = env.Data.RefreshToken
	}
	return nil
}

// modelCanonical 客户端模型名 → 官方代理模型 ID（无前缀输入，ChatStream 已由 handler 剥前缀）。
var modelCanonical = map[string]string{
	"zai_auto":          "zai_auto",
	"auto":              "zai_auto",
	"zai_auto-fast":     "zai_auto-fast",
	"auto-fast":         "zai_auto-fast",
	"zaicoding_glm-5.3": "zaicoding_glm-5.3",
	"glm-5.3":           "zaicoding_glm-5.3",
	"glm-5.3-flash":     "glm-5.3-flash",     // ⭐ 原生多模态轻量版，消耗积分少
	"flash":             "glm-5.3-flash",     // 简写支持
}

// canonicalModel 归一化模型名；未知模型回落 zai_auto。
func canonicalModel(m string) string {
	if v, ok := modelCanonical[m]; ok {
		return v
	}
	return "zai_auto"
}

// ChatStream 转发到官方代理端点并返回响应流（OpenAI SSE）。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	var peek struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &peek)
	model := canonicalModel(peek.Model)

	// 重写 body：model 归一化 + 强制 stream:true（官方代理对非流式请求返回整包 JSON，
	// 网关一侧统一用 SSE 再按需聚合，保证 Stream/Aggregate 两条路径都有数据）
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil {
		obj["model"] = model
		obj["stream"] = true
		if body2, e := json.Marshal(obj); e == nil {
			body = body2
		}
	}

	req, err := http.NewRequest(http.MethodPost, c.Base+ChatPath, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}
	for k, vs := range c.proxyHeaders(rawToken(a), model) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		log.Printf("autoclaw chat_stream uid=%s model=%s: transport error: %v", a.UID, model, err)
		return nil, 0, nil, err
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		kind := c.Classify(resp.StatusCode, string(raw))
		log.Printf("autoclaw chat_stream uid=%s model=%s: upstream %d %s body=%s",
			a.UID, model, resp.StatusCode, kind, truncate(string(raw), 200))
		return nil, resp.StatusCode, raw, nil
	}
	return resp.Body, resp.StatusCode, nil, nil
}

// autoclawModels 静态模型目录（官方 model-config 实测；上下文 1M / 输出上限来自配置）。
var autoclawModels = []provider.ModelInfo{
	{ID: "zai_auto", Name: "Auto (DeepSeek-V4)", ContextWindow: 1048576, MaxTokens: 393216},
	{ID: "zai_auto-fast", Name: "Auto-Fast (DeepSeek-V4)", ContextWindow: 1048576, MaxTokens: 393216},
	{ID: "zaicoding_glm-5.3", Name: "GLM-5.3 Coding", ContextWindow: 1048576, MaxTokens: 307200},
	{ID: "glm-5.3-flash", Name: "GLM-5.3 Flash", ContextWindow: 1048576, MaxTokens: 131072},
}

// StaticModels 供前端静态兜底。
func StaticModels() []provider.ModelInfo {
	out := make([]provider.ModelInfo, len(autoclawModels))
	copy(out, autoclawModels)
	return out
}

// FetchModels 返回 AutoClaw 可用模型（先静态兜底，再从官方 model-config 接口动态拉取合并）。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	// 1) 静态兜底
	out := make([]provider.ModelInfo, len(autoclawModels))
	copy(out, autoclawModels)

	// 2) 动态拉取官方模型配置（失败不阻塞，回落静态列表）
	req, err := http.NewRequest(http.MethodGet, c.Base+ModelConfigPath, nil)
	if err != nil {
		return out, nil
	}
	for k, vs := range userapiHeaders(rawToken(a)) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		log.Printf("autoclaw fetch_models uid=%s: %v (fallback static)", a.UID, err)
		return out, nil
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return out, nil
	}

	// 3) 解析模型列表（兼容 data[] / models[] 两种返回结构），按 ID 去重合并
	var cfg struct {
		Models []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"models"`
		Data []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &cfg) != nil {
		return out, nil
	}
	merged := cfg.Models
	if len(merged) == 0 {
		merged = cfg.Data
	}
	exist := make(map[string]bool, len(out))
	for _, m := range out {
		exist[m.ID] = true
	}
	for _, m := range merged {
		if m.ID == "" || exist[m.ID] {
			continue
		}
		exist[m.ID] = true
		out = append(out, provider.ModelInfo{ID: m.ID, Name: m.Name})
	}
	return out, nil
}

// FetchModelPricing 官方无公开积分单价，返回静态标签由前端兜底。
func (c *Client) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	return []provider.ModelPricing{
		{Model: "zai_auto", Channel: "autoclaw", Rate: 0, Note: "限免期 0 积分 · 默认自动路由（实测 deepseek-v4-flash-vision）"},
		{Model: "zai_auto-fast", Channel: "autoclaw", Rate: 0, Note: "限免期 0 积分 · 快速自动路由"},
		{Model: "zaicoding_glm-5.3", Channel: "autoclaw", Rate: 0, Note: "限免期 0 积分 · GLM-5.3 编码模型，积分等级高"},
		{Model: "glm-5.3-flash", Channel: "autoclaw", Rate: 0, Note: "限免期 0 积分 · GLM-5.3 Flash 原生多模态轻量版，消耗积分少 ⭐"},
	}, nil
}

// UserResource 剩余积分（wallets 聚合）。
func (c *Client) UserResource(a *auth.Auth) (int64, error) {
	remain, _, err := c.UserResourceDetail(a)
	return remain, err
}

// UserResourceDetail 积分与套餐明细（wallets + wallet-instances）。
func (c *Client) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	// 1) wallets 聚合余额
	req, err := http.NewRequest(http.MethodGet, c.Base+WalletsPath, nil)
	if err != nil {
		return 0, nil, err
	}
	for k, vs := range userapiHeaders(rawToken(a)) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	remain, err := c.walletBalance(req)
	if err != nil {
		return 0, nil, err
	}
	// 2) wallet-instances 分类套餐
	items := []provider.ResourceItem{}
	req2, err := http.NewRequest(http.MethodGet, c.Base+ScopePath, nil)
	if err != nil {
		return remain, items, nil
	}
	for k, vs := range userapiHeaders(rawToken(a)) {
		for _, v := range vs {
			req2.Header.Add(k, v)
		}
	}
	balance, list, err := c.scopeBalance(req2)
	if err == nil && balance > remain {
		remain = balance
	}
	items = append(items, list...)
	return remain, items, nil
}

// walletBalance 解析 wallets 响应 total_balance。
func (c *Client) walletBalance(req *http.Request) (int64, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		kind := c.Classify(resp.StatusCode, string(raw))
		return 0, &provider.Error{Kind: kind, Status: resp.StatusCode, Msg: truncate(string(raw), 160)}
	}
	var env struct {
		Code int64 `json:"code"`
		Data struct {
			TotalBalance json.RawMessage `json:"total_balance"`
			Wallets      []struct {
				Scope      string          `json:"scope"`
				WalletType string          `json:"wallet_type"`
				Balance    json.RawMessage `json:"total_balance"`
			} `json:"wallets"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, fmt.Errorf("wallets parse: %w", err)
	}
	if env.Code != 0 {
		return 0, fmt.Errorf("wallets code=%d", env.Code)
	}
	remain := parseNum(env.Data.TotalBalance)
	for _, w := range env.Data.Wallets {
		if b := parseNum(w.Balance); b > remain {
			remain = b
		}
	}
	return remain, nil
}

// scopeBalance 解析 wallet-instances 分类余额。
func (c *Client) scopeBalance(req *http.Request) (int64, []provider.ResourceItem, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return 0, nil, fmt.Errorf("scope http %d", resp.StatusCode)
	}
	var env struct {
		Code int64 `json:"code"`
		Data struct {
			TotalBalance json.RawMessage `json:"total_balance"`
			Instances    []struct {
				WalletType  string          `json:"wallet_type"`
				WalletScope string          `json:"wallet_scope"`
				Balance     json.RawMessage `json:"balance"`
				ExpireAt    json.RawMessage `json:"expire_at"`
			} `json:"wallet_instances"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, nil, fmt.Errorf("scope parse: %w", err)
	}
	if env.Code != 0 {
		return 0, nil, fmt.Errorf("scope code=%d", env.Code)
	}
	items := make([]provider.ResourceItem, 0, len(env.Data.Instances))
	for _, it := range env.Data.Instances {
		items = append(items, provider.ResourceItem{
			Name:  it.WalletType + "/" + it.WalletScope,
			Total: parseNum(it.Balance),
			Used:  0,
			Remain: parseNum(it.Balance),
			ExpireAt: parseNum(it.ExpireAt),
		})
	}
	return parseNum(env.Data.TotalBalance), items, nil
}

// parseNum 兼容 number/字符串数字。
func parseNum(raw json.RawMessage) int64 {
	if len(raw) == 0 || string(raw) == "null" {
		return 0
	}
	var n int64
	if json.Unmarshal(raw, &n) == nil {
		return n
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		var n2 int64
		if _, err := fmt.Sscanf(s, "%d", &n2); err == nil {
			return n2
		}
	}
	return 0
}

// DailyCheckin AutoClaw 任务领取（实测端点 autoclaw-task-complete）：
// 每日任务 + 新号一次性任务均可主动领取，全部幂等（already_completed 防重复）。
// 全部已领时返回“已签到”类错误（调度器识别为成功态）。
func (c *Client) DailyCheckin(a *auth.Auth) error {
	claimed, already, firstErr := 0, true, error(nil)
	for _, t := range checkinTasks {
		pts, done, err := c.claimTask(a, t.ID)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if done {
			claimed += pts
			already = false
		}
	}
	if claimed > 0 {
		log.Printf("autoclaw checkin uid=%s claimed=%d points", a.UID, claimed)
		return nil
	}
	if already {
		return fmt.Errorf("autoclaw 已签到")
	}
	if firstErr != nil {
		return firstErr
	}
	return nil
}

// checkinTasks 可领取任务（points 为参考值，实测以返回 reward_points 为准）。
var checkinTasks = []struct {
	ID  string
	Pts int
}{
	{"daily_signin", 200},
	{"daily_inspiration_center", 200},
	{"newbie_cloud_lobster", 500},
	{"newbie_local_lobster", 500},
}

// claimTask 领取单个任务；done=true 表示本次领取成功（新增 pts）。
func (c *Client) claimTask(a *auth.Auth, taskID string) (pts int, done bool, err error) {
	body, _ := json.Marshal(map[string]string{"task_id": taskID})
	req, err := http.NewRequest(http.MethodPost, c.Base+TaskCompletePath, bytes.NewReader(body))
	if err != nil {
		return 0, false, err
	}
	for k, vs := range userapiHeaders(rawToken(a)) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	// 实测：task-complete 走桌面端指纹（win32/pc）可领；web 指纹可能被拒
	req.Header.Set("X-Tm", "win32")
	req.Header.Set("X-Client-Type", "pc")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return 0, false, fmt.Errorf("task-complete http %d: %s", resp.StatusCode, truncate(string(raw), 120))
	}
	var env struct {
		Code int64 `json:"code"`
		Data struct {
			Success         bool   `json:"success"`
			AlreadyComplete bool   `json:"already_completed"`
			RewardPoints    int    `json:"reward_points"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, false, fmt.Errorf("task-complete parse: %w", err)
	}
	if env.Code == 0 && env.Data.AlreadyComplete {
		return 0, false, nil // 今日已领，幂等跳过
	}
	if !env.Data.Success {
		return 0, false, fmt.Errorf("task %s: success=false body=%s", taskID, truncate(string(raw), 120))
	}
	return env.Data.RewardPoints, true, nil
}

// Stream 透传 OpenAI SSE 流。
func (c *Client) Stream(w http.ResponseWriter, r io.Reader) error { return upstream.Stream(w, r) }

// Aggregate 聚合 OpenAI SSE 为非流式响应。
func (c *Client) Aggregate(r io.Reader) (map[string]any, error) { return upstream.Aggregate(r) }

// StreamWithThink / AggregateWithThink 实现 provider.ThinkPassthrough。
func (c *Client) StreamWithThink(w http.ResponseWriter, r io.Reader, passthrough bool) error {
	return upstream.StreamWithThink(w, r, passthrough)
}

func (c *Client) AggregateWithThink(r io.Reader, keepReasoning bool) (map[string]any, error) {
	return upstream.AggregateWithThink(r, keepReasoning)
}

// truncate 截断日志片段。
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
// ---------------------------------------------------------------------------
// 面板自助加号：短信验证码登录（对齐 autoclaw2api cmd/login）
// ---------------------------------------------------------------------------

// LoginResult 登录成功返回的凭证（userapi agent-login 响应）。
type LoginResult struct {
	DeviceID     string `json:"-"`
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	UserID       string `json:"user_id"`
	UserName     string `json:"user_name"`
	Phone        string `json:"phone"`
	FirstLogin   bool   `json:"first_login"`
	WebFirst     bool   `json:"web_first_login"`
}

// userAPIBase 返回 userapi 基址（当前固定国内）。
func (c *Client) userAPIBase() string {
	return c.Base + "/userapi"
}

// userAPIJSON 发 userapi 请求并解 {code,msg,data} 信封。
func (c *Client) userAPIJSON(method, path, rawToken string, body any) (json.RawMessage, error) {
	var rd io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(raw)
	}
	req, err := http.NewRequest(method, c.userAPIBase()+path, rd)
	if err != nil {
		return nil, err
	}
	for k, vs := range userapiHeaders(rawToken) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("autoclaw userapi HTTP %d: %s", resp.StatusCode, truncate(string(raw), 160))
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("autoclaw userapi parse: %w (body: %s)", err, truncate(string(raw), 160))
	}
	if env.Code != 0 {
		return nil, fmt.Errorf("autoclaw userapi code=%d msg=%s", env.Code, truncate(env.Msg, 160))
	}
	return env.Data, nil
}

// newDeviceID 生成登录设备 UUID（与官方 web 一致，一次性绑定）。
func newDeviceID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// SendCode 发送短信验证码（面板自助加号第一段）。
func (c *Client) SendCode(phone string) (string, error) {
	deviceID := newDeviceID()
	body := map[string]any{
		"source_id": "autoclaw",
		"device_id": deviceID,
		"phone":     strings.TrimSpace(phone),
	}
	if _, err := c.userAPIJSON(http.MethodPost, "/v1/agent-send-code", "", body); err != nil {
		return "", err
	}
	return deviceID, nil
}

// Login 手机号+验证码换凭证，返回 LoginResult（调用方落盘）。
// code 必须纯数字（官方 web 转 Number 发送，字符串会被 400001 拒）。
func (c *Client) Login(deviceID, phone, code string) (*LoginResult, error) {
	codeNum, err := strconv.ParseInt(strings.TrimSpace(code), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("验证码必须为数字: %v", err)
	}
	body := map[string]any{
		"source_id": "autoclaw",
		"device_id": deviceID,
		"phone":     strings.TrimSpace(phone),
		"code":      codeNum,
		"platform":  "web",
	}
	data, err := c.userAPIJSON(http.MethodPost, "/v1/agent-login/", "", body)
	if err != nil {
		return nil, err
	}
	var lr LoginResult
	if err := json.Unmarshal(data, &lr); err != nil {
		return nil, fmt.Errorf("autoclaw login parse: %w", err)
	}
	if strings.TrimSpace(lr.AccessToken) == "" {
		return nil, fmt.Errorf("autoclaw login: 响应缺 access_token")
	}
	lr.DeviceID = deviceID
	return &lr, nil
}
