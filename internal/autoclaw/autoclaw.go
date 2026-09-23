// Package autoclaw 实现 AutoClaw（Z.ai/智谱）官方代理通道的上游客户端。
//
// 通道说明：AutoClaw 桌面端走官方 OpenAI 兼容代理
//
//	POST https://autoglm-acceleration-api.zhipuai.cn/autoclaw-proxy/proxy/autoclaw/chat/completions
//
// 关键鉴权头（从官方桌面客户端 app.asar 逆向）：
//   - X-Authorization: Bearer <accessToken>（注意不是标准 Authorization）
//   - X-Client-Type: pc、X-Product: autoclaw、X-Tm: win、X-Version:1.18.4、X-Lang: zh-CN、
//     X-Channel: official、X-Request-Id/x_trace_id: UUID（带横线）
//
// ⚠️ 2026-09-21 契约反转（详见 docs/lessons/06-autoclaw-contract-inversion-and-vip-wall.md）：
//   - X-Harness-Type: zcode 从"豁免系统提示词白名单"反转为"带上即 400"——已删除；
//   - 请求体第一条 system 消息必须为系统 banner（AutoClawSystemBanner），否则 400 invalid request。
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

	"zerogate/internal/auth"
	"zerogate/internal/provider"
	"zerogate/internal/upstream"
)

// 渠道常量（上游协议逆向值，勿随意改）。
const (
	// AppID / AppSecret 来自官方 webElectronApi 常量。
	AppID     = "100003"
	AppSecret = "38d2391985e2369a5fb8227d8e6cd5e5"
	ClientVer = "1.12.1" // userapi/签名接口版本（实测可用，勿动）
	// 代理端点专属版本号，对齐正在工作的桌面端 1.18.4（仅用于 proxyHeaders，
	// 不影响已实测的 userapi ClientVer 签名流）
	proxyVersion = "1.18.4"
	// 系统提示词 banner（2026-09-21 起必需）：请求体第一条 system 消息必须以此开头，
	// 否则上游 400 invalid request。与客户端自带 system 共存实测两种姿势均可
	// （banner 独立消息前置 / 拼接进首条 system），daemon 侧采用独立消息前置，不动客户端内容。
	AutoClawSystemBanner = "You are a personal assistant running inside OpenClaw.\n## Tooling"
	// 旧版代理端点（主入口）
	ProxyBase = "https://autoglm-acceleration-api.zhipuai.cn"
	ProxyPath = "/autoclaw-proxy/proxy/autoclaw"
	ChatPath  = ProxyPath + "/chat/completions"
	// 忙窗自愈重试窗口与退避上限。
	//
	// ⚠️ 2026-09-23 教训（详见 docs/lessons/09）：此处曾设为 270 秒「时间窗内持续重试」，
	// 结果在账号被封（410004）期间，客户端每 8 秒重试一轮 × 每轮 270 秒内部重试，
	// 把"凭证已失效"刷成"持续暴力请求"，两个账号先后被上游风控封禁。
	// 现在收紧为短窗口 + 少量次数：忙窗（400/403 pay-view）仍能自愈，
	// 但绝不长时间占着上游 —— 真正的封禁信号由 isFailoverable 立即中断。
	retryWindow     = 45 * time.Second
	retryMaxBackoff = 3 * time.Second
	// retryMaxAttempts 单次请求内的最大尝试次数（硬上限，防退避抖动导致打太多次）。
	retryMaxAttempts = 6
	// 每日签到/任务领取端点（web 签名头实测有效返回 reward_points=200）。
	// 注意：task-complete 挂在 proxy 根下，再拼 ProxyPath 会多出 /autoclaw 段导致 401 Invalid token。
	TaskCompletePath = "/autoclaw-proxy/proxy/autoclaw-task-complete"
	// 活动（token 送赠）配置与奖励领取端点（加速度主机）。
	// 注意：config 用 小写 authorization 认证；reward 用 小写 authorization + body {modal_id,reward_type}。
	PromotionConfigPath = "/autoclaw-proxy/proxy/autoclaw-promotion-config"
	PromotionRewardPath = "/autoclaw-proxy/proxy/autoclaw-promotion-reward"
	WalletsPath         = "/agent-assetmgr/api/v2/wallets?biz_app_id=autoclaw"
	ScopePath           = "/agent-assetmgr/api/v1/wallet-instances?wallet_type=all&wallet_scope=all"
	RefreshPath         = "/userapi/v1/refresh"
	ModelConfigPath     = "/agent-assetmgr/api/v1/model-config"
)

// 注意：曾设计 api.z.ai / open.bigmodel.cn 多端点直连（ZaiBase/BigModelBase/
// BigModelCodingBase）+ FetchModelsZai/zaiHeaders/bigmodelHeaders。实测定论：
// AutoClaw 的 accessToken 是 acceleration 代理专用 JWT，这些第三方 OpenAI 端点
// 一律返回 401/404（token 不认可），只有 ProxyBase 能鉴权 —— 故全部删除，
// 模型路由统一走 ProxyBase 的双标识（routeID + body.model，见 modelRoutes）。

// Client AutoClaw 上游客户端。
type Client struct {
	HTTP    *http.Client
	Base    string // userapi/proxy 基址（autoglm-acceleration-api.zhipuai.cn）
	Channel string // "autoclaw"，费率分组
	Version string // X-Version（与官方桌面端一致）

	// StreamHTTP 供流式对话使用：无总超时，思考型模型可长时间输出。
	// http.Client.Timeout 覆盖响应体读取，会把长思考的 SSE 硬掐断。nil 时回退到 HTTP。
	StreamHTTP *http.Client
}

// New 生产默认。
func New() *Client {
	tr := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second, // 流式无总超时，靠它兜住建连卡死
	}
	return &Client{
		HTTP:       &http.Client{Timeout: 180 * time.Second, Transport: tr},
		StreamHTTP: &http.Client{Transport: tr},
		Base:       ProxyBase,
		Channel:    "autoclaw",
		Version:    ClientVer,
	}
}

// streamClient 返回流式对话用的 HTTP 客户端（无总超时）。
func (c *Client) streamClient() *http.Client {
	if c.StreamHTTP != nil {
		return c.StreamHTTP
	}
	return c.HTTP
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
	ErrBanned      = provider.ErrBanned
)

// Classify 按 HTTP 状态码 + body 判定错误类别。
func (c *Client) Classify(status int, body string) provider.ErrKind { return Classify(status, body) }

// Classify 独立分类（供测试直接调用）。
// 业务码优先（借 GLM_proxy BAN-RISK 分类表）：
//   - 410004 = 账号封禁（多在 403 中）→ ErrBanned
//   - 810002 = 免费层限流（403 撞车信号）→ ErrSoftRate
//   - 810000 = 积分用尽（402）→ ErrHardCredit
func Classify(status int, body string) provider.ErrKind {
	low := strings.ToLower(body)
	switch {
	case strings.Contains(low, "410004"):
		return ErrBanned
	case strings.Contains(low, "810002"):
		return ErrSoftRate
	case strings.Contains(low, "810000"):
		return ErrHardCredit
	case status == http.StatusUnauthorized || strings.Contains(low, "invalid token") || strings.Contains(low, "登录") && status == http.StatusUnauthorized:
		return ErrSessionDead
	case status == http.StatusForbidden:
		// 403 可能是 WAF/风控：短冷却口风，不永久禁用
		return ErrClient
	case status == http.StatusTooManyRequests || strings.Contains(low, "rate limit"):
		return ErrSoftRate
	case status == http.StatusPaymentRequired:
		return ErrHardCredit
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

// taskCompleteHeaders task-complete 专属请求头（严格对齐官方 commonHeaders）。
// completeClientTask 用 commonHeaders = authHeaders + X-Lang + (X-Channel?) + authorization，
// 其中 X-Tm = getPlatformTm() → Windows 上是 "win"（不是 web/win32）。
// 注意：该接口不能带 X-Client-Type / X-Harness-Type，否则被服务端判定"非客户端触发"→ 400。
func taskCompleteHeaders(rawToken string) http.Header {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "*/*")
	h.Set("X-Version", ClientVer)
	h.Set("X-Tm", "win")
	h.Set("X-Product", "autoclaw")
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
// 严格对齐桌面端实测契约（见文件头注释），缺少/错值会导致 400 invalid request：
//   - X-Request-Id / x_trace_id = UUID（带横线）
//   - X-Tm: win（不是 win32）、X-Lang: zh-CN、X-Channel: official、Accept: */*
//
// ⚠️ 2026-09-21 契约反转：X-Harness-Type: zcode 已反转成"带上即 400"，严禁再加回
// （系统提示词校验改由请求体 banner 满足，见 ChatStream 注入逻辑）。
func (c *Client) proxyHeaders(rawToken, model string) http.Header {
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "*/*")
	h.Set("X-Authorization", "Bearer "+rawToken)
	h.Set("X-Request-Id", newDeviceID())
	h.Set("X-Request-Model", model)
	h.Set("X-Client-Type", "pc")
	h.Set("X-Product", "autoclaw")
	h.Set("X-Tm", "win")
	h.Set("X-Version", proxyVersion)
	h.Set("X-Lang", "zh-CN")
	h.Set("X-Channel", "official")
	h.Set("x_trace_id", newDeviceID())
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
		return &provider.Error{Kind: Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 160)}
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

// routeSpec 一次 AutoClaw 模型路由的规约（双标识，参考开源 agent2api 的
// autoclaw-models.mjs / adapter）。
//
// 上游代理用两个不同的标识定位模型（缺一不可，只发对其中一个会让上游选错计费
// 通道或认不出模型，导致 400「非法模型」）：
//   - routeID → 发进 **X-Request-Model 请求头**（完整带通道前缀路由 ID）。
//     前缀决定计费通道：`zai_`→会员/按量付费、`zaicoding_`→Coding Plan、
//     `zai_auto`→服务端自动选型。
//   - bodyID  → 填进 **body.model**（剥掉路由前缀后的模型 ID）。
type routeSpec struct {
	routeID string // X-Request-Model
	bodyID  string // body.model
}

// modelRoutes 客户端模型名 → {路由ID, body模型ID}。
// ⚠️ 实测定论：AutoClaw 的 accessToken 是 acceleration 代理专用 JWT，只在
// autoglm-acceleration-api.zhipuai.cn（ProxyBase）上能过鉴权；api.z.ai /
// open.bigmodel.cn 一律 401/404。故所有模型统一走代理端点，绝不直达 api.z.ai。
var modelRoutes = map[string]routeSpec{
	"zai_auto":          {"zai_auto", "auto"},
	"auto":              {"zai_auto", "auto"},
	"zai_auto-fast":     {"zai_auto-fast", "auto-fast"},
	"auto-fast":         {"zai_auto-fast", "auto-fast"},
	"zaicoding_glm-5.3": {"zaicoding_glm-5.3", "glm-5.3"},
	"glm-5.3":           {"zaicoding_glm-5.3", "glm-5.3"},
	"glm-5.3-flash":     {"zai_glm-5.3-flash", "glm-5.3-flash"}, // ⭐ 会员/按量通道，实测 200
	"flash":             {"zai_glm-5.3-flash", "glm-5.3-flash"}, // 简写支持
}

// resolveRoute 解析客户端模型名 → 路由规约；未知模型回落默认路由 zai_auto。
func resolveRoute(m string) routeSpec {
	if v, ok := modelRoutes[m]; ok {
		return v
	}
	return routeSpec{routeID: "zai_auto", bodyID: "auto"}
}

// isFailoverable 判断某次上游响应是否需要重试。
// 忙窗亦或额度受限：主域返回 400 invalid request / 403 pay-view(810001) / 403 auto-fast quota(810000)。
// 401/405/5xx 这类 host 级错误也可触发重试；纯业务参数错误（其它 4xx）不重试直接回报。
func isFailoverable(status int, body string) bool {
	low := strings.ToLower(body)
	switch {
	case status == http.StatusOK:
		return false
	case status == http.StatusBadRequest:
		return true // 忙窗/无效请求
	case status == http.StatusForbidden:
		// 410004 账号封禁：不可恢复，重试到窗口耗尽毫无意义，立即回报
		if strings.Contains(low, "410004") || strings.Contains(low, "封禁") {
			return false
		}
		return true // 忙窗 403（pay-view/quota）
	case status == http.StatusUnauthorized:
		return true // 该 host 不认 token
	case status == http.StatusMethodNotAllowed:
		// 405 nginx 页 = 死主机（未挂代理路径），不把错误页透传给客户端，重试
		if strings.Contains(low, "<") || strings.Contains(low, "nginx") {
			return true
		}
		return false
	case status >= 500:
		return true
	}
	return false
}

// ChatStream 转发到官方代理端点并返回响应流（OpenAI SSE）。
// 统一走自动加速度代理端点（ProxyBase）——AutoClaw 的 JWT 只在那里能鉴权。
// 模型路由采用双标识：X-Request-Model 填带前缀路由 ID，body.model 填剥前缀后的
// 模型 ID（见 modelRoutes），否则上游 400「非法模型」或选错计费通道。
//
// 忙窗容灾：⚠️ 实测我们的 token 只绑定 acceleration 域，其余 host（inner-3/pre-api/api）
// 一律 401/405/nginx 死主机，跨 host 轮转救不了忙窗 400。真正的恢复 = 在能鉴权的主域上
// 周期性重试（对齐桌面端 DEFAULT_MAX_RETRIES=2）。忙窗 400/403 时短退避重试，忙窗一过即恢复。
// 重试上限 MaxFailover 次，全部失败返回中文文案 + 软错误码（502），绝不回吐上游 4xx。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	var peek struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &peek)
	route := resolveRoute(peek.Model)

	// 重写 body：模型填 bodyID + 强制 stream:true（官方代理对非流式请求返回整包 JSON，
	// 网关一侧统一用 SSE 再按需聚合，保证 Stream/Aggregate 两条路径都有数据）
	// + 注入系统 banner（2026-09-21 起必需）：作为独立的第一条 system 消息前置，
	// 不动客户端自带的 system 内容（共存姿势实测见 lesson 06）。
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil {
		obj["model"] = route.bodyID
		obj["stream"] = true
		if msgs, ok := obj["messages"].([]any); ok {
			hasBanner := false
			for _, m := range msgs {
				if mm, ok := m.(map[string]any); ok &&
					strings.EqualFold(fmt.Sprint(mm["role"]), "system") &&
					strings.Contains(fmt.Sprint(mm["content"]), AutoClawSystemBanner) {
					hasBanner = true
					break
				}
			}
			if !hasBanner {
				banner := map[string]any{"role": "system", "content": AutoClawSystemBanner}
				obj["messages"] = append([]any{banner}, msgs...)
			}
		}
		if body2, e := json.Marshal(obj); e == nil {
			body = body2
		}
	}

	tok := rawToken(a)
	deadline := time.Now().Add(retryWindow)
	backoff := 500 * time.Millisecond
	for attempt := 1; ; attempt++ {
		// 双重上限：时间窗 + 尝试次数。任一到达即放弃，绝不长时间占着上游
		// （封号事故根因，见常量处注释）。
		if time.Now().After(deadline) || attempt > retryMaxAttempts {
			log.Printf("autoclaw chat_stream uid=%s route=%s 忙窗重试放弃(attempt=%d, 窗口%ds)",
				a.UID, route.routeID, attempt-1, retryWindow/time.Second)
			// 429 而非 502：502 会被客户端当成"服务暂时故障"而继续重试，
			// 429 语义明确（上游繁忙/限流），配合 Retry-After 让客户端退避。
			return nil, http.StatusTooManyRequests,
				[]byte("AutoClaw 上游繁忙，请稍后再试（勿频繁重试，以免账号被风控）。"), nil
		}
		reqURL := c.Base + ChatPath
		headers := c.proxyHeaders(tok, route.routeID)
		log.Printf("autoclaw chat_stream uid=%s route=%s body_model=%s endpoint=%s attempt=%d",
			a.UID, route.routeID, route.bodyID, reqURL, attempt)

		req, rerr := http.NewRequest(http.MethodPost, reqURL, bytes.NewReader(body))
		if rerr != nil {
			return nil, 0, nil, rerr
		}
		for k, vs := range headers {
			for _, v := range vs {
				req.Header.Add(k, v)
			}
		}
		resp, derr := c.streamClient().Do(req)
		if derr != nil {
			log.Printf("autoclaw chat_stream uid=%s route=%s attempt=%d transport: %v",
				a.UID, route.routeID, attempt, derr)
			time.Sleep(backoff)
			if backoff < retryMaxBackoff {
				backoff *= 2
			}
			continue
		}
		if resp.StatusCode >= 400 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			kind := c.Classify(resp.StatusCode, string(raw))
			bodyText := truncate(string(raw), 200)
			log.Printf("autoclaw chat_stream uid=%s route=%s attempt=%d upstream %d %s body=%s",
				a.UID, route.routeID, attempt, resp.StatusCode, kind, bodyText)
			// 免费桶日额度墙（403 810000）：日级硬额度，重试到窗口耗尽毫无意义，
			// 立即返回明确中文提示（对齐客户端"开会员"文案，客户端 UI 会渲染成排队+VIP）。
			if strings.Contains(string(raw), "810000") {
				return nil, http.StatusBadGateway,
					[]byte("AutoClaw 免费通道（auto-fast）今日额度已用完，24 小时后重置；请改用积分通道模型（zai_auto / glm-5.3-flash 等），或开通会员。"), nil
			}
			if isFailoverable(resp.StatusCode, string(raw)) {
				// 忙窗/额度：递增退避后继续，直到窗口过去自动成功，无需人工干预。
				time.Sleep(backoff)
				if backoff < retryMaxBackoff {
					backoff *= 2
				}
				continue
			}
			// 非可重试业务错误：返回中文软错 502，交由上层按服务异常处理。
			// 关键：绝不回吐上游 4xx（401/400/403/405），否则 handler 会误判为会话失效/参数错，
			// 导致账号被永久禁用，且客户端把自己的 Key 当成失效而崩溃终止会话。
			return nil, http.StatusBadGateway,
				[]byte("AutoClaw 请求被拒绝，请稍后再试。"), nil
		}
		return resp.Body, resp.StatusCode, nil, nil
	}
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
		{Model: "zai_auto", Channel: "autoclaw", Rate: 0, Note: "服务端自动选型路由，可能落到 DeepSeek-V4 等"},
		{Model: "zai_auto-fast", Channel: "autoclaw", Rate: 0, Note: "快速自动路由 · 账号免费额度可能已用尽(403)"},
		{Model: "zaicoding_glm-5.3", Channel: "autoclaw", Rate: 0, Note: "GLM-5.3 编码模型 · Coding 通道按量计费"},
		{Model: "glm-5.3-flash", Channel: "autoclaw", Rate: 0, Note: "GLM-5.3 Flash · 会员/按量通道，实测扣积分（短请求≈1分）⭐"},
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
	log.Printf("autoclaw UserResourceDetail uid=%s remain=%d items_count=%d err=%v", a.UID, remain, len(items), err)
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
		return 0, nil, &provider.Error{Kind: Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 160)}
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
			Name:     it.WalletType + "/" + it.WalletScope,
			Total:    parseNum(it.Balance),
			Used:     0,
			Remain:   parseNum(it.Balance),
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

// DailyCheckin AutoClaw 每日签到 + 任务领取。
//
// 实测：POST /autoclaw-proxy/proxy/autoclaw-task-complete
// body={"task_id":"daily_signin"} → 200 {"data":{"success":true,"already_completed":false,
// "reward_points":200,"task_id":"daily_signin"}}。
// 请求头必须用桌面客户端指纹（taskCompleteHeaders：win32/pc/zcode），否则返回
// 400 "任务不是由客服端触发"；用旧路径拼出多余 /autoclaw 段会返回 401 Invalid token。
//
// 说明：该端点偶发因平台风控窗口临时返回 401 Invalid token（如 11128 风控），
// 调用方若拿到异常会向上抛真实错误，调度器按失败重试，绝不误报"已签到"。
func (c *Client) DailyCheckin(a *auth.Auth) error {
	tok := rawToken(a)
	claimed, anyAlready, firstErr := 0, false, error(nil)
	for _, t := range checkinTasks {
		pts, done, err := c.claimTask(tok, t.ID)
		log.Printf("autoclaw checkin_task uid=%s task_id=%s pts=%d done=%v err=%v", a.UID, t.ID, pts, done, err)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if done {
			claimed += pts
		} else {
			anyAlready = true
		}
	}
	if claimed > 0 {
		// 额外尝试 token 活动奖励（不影响签到结果判断）
		if pts, _ := c.claimTokenActivities(a.UID, tok); pts > 0 {
			claimed += pts
		}
		log.Printf("autoclaw checkin uid=%s claimed=%d points", a.UID, claimed)
		return nil
	}
	// 签到全幂等已领时也顺带跑一次 token 活动，可能有未领的新活动
	if p, _ := c.claimTokenActivities(a.UID, tok); p > 0 {
		log.Printf("autoclaw checkin token promo uid=%s points=%d", a.UID, p)
		return nil
	}
	// 只要有任何任务真实失败（如 401 风控/网络），上报真实错误而非误报成功。
	if firstErr != nil {
		return firstErr
	}
	// 全部任务幂等已领（already_completed=true）→ 今日已签到成功态。
	if anyAlready {
		return fmt.Errorf("autoclaw 已签到")
	}
	return fmt.Errorf("autoclaw 已签到")
}

// ---------------------------------------------------------------
// Token 活动（promotion）自动领取
// ---------------------------------------------------------------

// claimTokenActivities 扫描 autoclaw-promotion-config 里的 modals，对开启了
// sendReward/timingReward 且未领取的活动自动 POST promotion-reward 领取 token。
// 兼容已领取（幂等），返回本次新增积分/点数；无可用活动时静默返回。
func (c *Client) claimTokenActivities(uid, tok string) (claimed int, err error) {
	// 1) 拉活动配置
	req, rerr := http.NewRequest(http.MethodGet, c.Base+PromotionConfigPath, nil)
	if rerr != nil {
		return 0, rerr
	}
	for k, vs := range promotionConfigHeaders(tok) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, rerr := c.HTTP.Do(req)
	if rerr != nil {
		return 0, rerr
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return 0, nil // config 非 200 静默跳过，避免阻塞每日签到主流程
	}
	var cfg struct {
		Data struct {
			Modals []struct {
				ModalID string `json:"modal_id"`
				BtnText string `json:"btnText"`
			} `json:"modals"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return 0, nil
	}
	// 2) 对可领取（btnText 非"已领取"）的活动尝试 sendReward/timingReward
	for _, m := range cfg.Data.Modals {
		id := strings.TrimSpace(m.ModalID)
		if id == "" || strings.Contains(m.BtnText, "已领取") {
			continue
		}
		for _, rt := range []string{"sendReward", "timingReward"} {
			pts, done, err := c.claimPromotionReward(uid, tok, id, rt)
			if err == nil && done {
				claimed += pts
			}
		}
	}
	return claimed, nil
}

// claimPromotionReward 领取单个 token 活动的奖励；done=true 表示新增到账。
func (c *Client) claimPromotionReward(uid, tok, modalID, rewardType string) (pts int, done bool, err error) {
	body, _ := json.Marshal(map[string]string{"modal_id": modalID, "reward_type": rewardType})
	req, err := http.NewRequest(http.MethodPost, c.Base+PromotionRewardPath, bytes.NewReader(body))
	if err != nil {
		return 0, false, err
	}
	for k, vs := range promotionConfigHeaders(tok) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		// 非 200（如对不可领 modal 返回 invalid request params）：记日志但不算失败，
		// 上游用 HTTP 错误表达「该 modal 当前不可领」，属常态而非故障。
		log.Printf("autoclaw promotion_reward uid=%s modal=%s type=%s http=%d body=%s",
			uid, modalID, rewardType, resp.StatusCode, truncate(string(raw), 120))
		return 0, false, nil
	}
	var env struct {
		Code int64 `json:"code"`
		Data struct {
			Points  int  `json:"points"`
			Success bool `json:"success"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, false, nil
	}
	if env.Code != 0 || !env.Data.Success {
		return 0, false, nil
	}
	// points=0 + success=true 也是有效响应（幂等重领/0 分活动），不算新增到账
	if env.Data.Points <= 0 {
		log.Printf("autoclaw promotion_reward uid=%s modal=%s type=%s points=0 (already/none)", uid, modalID, rewardType)
		return 0, false, nil
	}
	log.Printf("autoclaw promotion_reward uid=%s modal=%s type=%s points=%d", uid, modalID, rewardType, env.Data.Points)
	return env.Data.Points, true, nil
}

// promotionConfigHeaders config/reward 专属请求头。2026-09-22 实测（app.asar
// 逆向 + 抓包）：reward 接口必须带客户端同款双鉴权头 —— authorization 之外还要
// X-Authorization（getNewbieGuideAuthHeaders），只带 authorization 会对可领取
// modal 返回 HTTP 错误 "invalid request params"，双头齐备才返回 200 信封。
func promotionConfigHeaders(rawToken string) http.Header {
	ts := fmt.Sprintf("%d", time.Now().Unix())
	h := http.Header{}
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "*/*")
	h.Set("X-Version", ClientVer)
	h.Set("X-Tm", "win")
	h.Set("X-Product", "autoclaw")
	h.Set("X-Auth-Appid", AppID)
	h.Set("X-Auth-TimeStamp", ts)
	h.Set("X-Auth-Sign", sign(ts))
	h.Set("X-Trace-Id", newTraceID())
	h.Set("X-Lang", "zh-CN")
	h.Set("X-Channel", "official")
	if rawToken != "" {
		h.Set("authorization", "Bearer "+rawToken)
		h.Set("X-Authorization", "Bearer "+rawToken)
	}
	return h
}

// checkinTasks 可自动领取的任务（每日签到 +200 等）。
// 注意：upgrade_pc_app（升级PC客户端 +300）不能自动触发，服务端返回
// 400 "任务不支持客户端触发"，只能在桌面端真实操作完成，故不在列表内。
var checkinTasks = []struct {
	ID  string
	Pts int
}{
	{"daily_signin", 200},
	{"daily_inspiration_center", 200},
	{"newbie_cloud_lobster", 500},
	{"newbie_local_lobster", 500},
}

// claimTask 向 autoclaw-task-complete 领取单个任务；done=true 表示本次新增积分。
// 头必须对齐官方 commonHeaders（taskCompleteHeaders）：X-Tm:win + 签名头 + Authorization。
// 用 X-Authorization 会 401 Invalid token；带 X-Client-Type/X-Harness-Type 会 400 "非客户端触发"。
func (c *Client) claimTask(tok, taskID string) (pts int, done bool, err error) {
	body, _ := json.Marshal(map[string]string{"task_id": taskID})
	req, err := http.NewRequest(http.MethodPost, c.Base+TaskCompletePath, bytes.NewReader(body))
	if err != nil {
		return 0, false, err
	}
	for k, vs := range taskCompleteHeaders(tok) {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return 0, false, &provider.Error{Kind: Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 120)}
	}
	var env struct {
		Code int64 `json:"code"`
		Data struct {
			Success         bool `json:"success"`
			AlreadyComplete bool `json:"already_completed"`
			RewardPoints    int  `json:"reward_points"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, false, fmt.Errorf("task-complete parse: %w", err)
	}
	if env.Code != 0 {
		return 0, false, fmt.Errorf("task-complete code=%d: %s", env.Code, truncate(string(raw), 120))
	}
	if env.Data.AlreadyComplete {
		return 0, false, nil // 今日已领，幂等跳过
	}
	if !env.Data.Success {
		return 0, false, fmt.Errorf("task %s success=false body=%s", taskID, truncate(string(raw), 120))
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

// GrowthTravel 成长中心：每日旅行（autoclaw 暂不支持，返回 nil）
func (c *Client) GrowthTravel(a *auth.Auth) error {
	return nil
}

// GrowthLottery 成长中心：幸运抽奖（autoclaw 暂不支持，返回 nil）
func (c *Client) GrowthLottery(a *auth.Auth) error {
	return nil
}

// GrowthTasksDetailed 成长中心：任务领取（autoclaw 暂不支持，返回空结果）
func (c *Client) GrowthTasksDetailed(a *auth.Auth) provider.GrowthTasksResult {
	return provider.GrowthTasksResult{Message: "暂未支持"}
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
