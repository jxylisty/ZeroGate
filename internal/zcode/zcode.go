// Package zcode 智谱 ZCode (Z.AI) 上游客户端，实现 provider.Upstream。
// 协议逆向自开源 zcode2api（github.com/liu5269/zcode2api）：
//
//  1. 聊天端点 https://zcode.z.ai/api/v1/zcode-plan/anthropic/v1/messages，
//     Anthropic Messages 协议（强制 stream，返回 Anthropic SSE）。
//  2. 认证方式为 JWT 模式：Authorization: Bearer {JWT}（3 段点分），
//     无 refresh 机制，登录态失效由上游 401/403 触发禁用。
//  3. JWT 模式必带 X-Aliyun-Captcha-Verify-Param（阿里云无痕验证），
//     用 captcha_node/solver.js（Node + jsdom，无浏览器）求解，缓存 45s。
//  4. 模型名大小写敏感：GLM-5.2 / GLM-5-Turbo；对外只公开这两个。
//  5. 额度：GET /api/v1/zcode-plan/billing/balance → data.balances[]，
//     字段 {show_name,total_units,used_units,remaining_units,expires_at}。
package zcode

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/platform"
	"zerogate/internal/provider"
)

// Client ZCode 上游客户端，实现 provider.Upstream。
type Client struct {
	HTTP    *http.Client
	Base    string // https://zcode.z.ai
	Channel string // 渠道名（zcode），用于费率分组

	// StreamHTTP 供流式对话使用：无总超时，思考型模型可长时间输出。
	// http.Client.Timeout 覆盖响应体读取，会把长思考的 SSE 硬掐断。nil 时回退到 HTTP。
	StreamHTTP *http.Client

	// CaptchaNodeDir 验证码求解器目录（内含 solver.js + node_modules），
	// 相对 exe 工作目录；留空则禁用验证码（AI 免费套餐可能不开风控，但仍建议开启）。
	CaptchaNodeDir string

	captcha *CaptchaManager

	// 桥接模式（ZCODE_BRIDGE_URL 非空时启用）：chat 上游转发到本地
	// zcode-proxy（TriDefender/zcode-api，进程内验证码求解 + 3.12.3 指纹），
	// 本进程不再自行求解验证码、不再直连 zcode.z.ai。
	// billing/额度查询在桥接模式下禁用——billing 端点对 API 形态常 3012，
	// 重复请求会喂养风控（2026-09-18 排查结论，详见 AGENTS.md 第 7 章）。
	BridgeURL string
	BridgeKey string
}

// New 生产默认。
func New() *Client {
	tr := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		ResponseHeaderTimeout: 120 * time.Second, // 流式无总超时，靠它兜住建连卡死
	}
	c := &Client{
		HTTP:           &http.Client{Timeout: 180 * time.Second, Transport: tr},
		StreamHTTP:     &http.Client{Transport: tr},
		Base:           "https://zcode.z.ai",
		Channel:        "zcode",
		CaptchaNodeDir: "captcha_node",
	}
	if u := strings.TrimSpace(os.Getenv("ZCODE_BRIDGE_URL")); u != "" {
		c.BridgeURL = strings.TrimRight(u, "/")
	} else {
		c.BridgeURL = "http://127.0.0.1:18083"
	}
	c.BridgeKey = strings.TrimSpace(os.Getenv("ZCODE_BRIDGE_KEY"))
	if c.BridgeKey == "" {
		c.BridgeKey = "sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4"
	}
	c.CaptchaNodeDir = "" // 桥接模式下禁用本地验证码求解器
	log.Printf("zcode: 桥接模式 → %s（验证码/指纹由 zcode-proxy 负责）", c.BridgeURL)
	return c
}

// NewWithBase 测试用：覆盖 base。
func NewWithBase(base string) *Client {
	c := New()
	c.Base = base
	return c
}

// cm 懒初始化验证码管理器。
func (c *Client) cm() *CaptchaManager {
	if c.captcha == nil {
		c.captcha = NewCaptchaManager(c.Base, c.HTTP, c.CaptchaNodeDir)
	}
	return c.captcha
}

// ---------------------------------------------------------------------------
// 错误分类
// ---------------------------------------------------------------------------

// ErrKind 错误分类（复用 upstream 语义 + zcode 专属 code）。
const (
	ErrHardCredit  = provider.ErrHardCredit
	ErrSoftRate    = provider.ErrSoftRate
	ErrSessionDead = provider.ErrSessionDead
	ErrNotFound    = provider.ErrNotFound
	ErrServer      = provider.ErrServer
	ErrClient      = provider.ErrClient
)

// Classify 按 HTTP 状态码 + body 判定错误类别。
// body 可能是 {code,msg} 或 {error:{...}}；出现 "captcha" 视为验证码失效
// （客户端请求问题，交由 ChatStream 内部重求，不禁用账号）。
func (c *Client) Classify(status int, body string) provider.ErrKind { return Classify(status, body) }

// streamClient 返回流式对话用的 HTTP 客户端（无总超时）。
func (c *Client) streamClient() *http.Client {
	if c.StreamHTTP != nil {
		return c.StreamHTTP
	}
	return c.HTTP
}

// Classify 独立分类（供测试直接调用）。
func Classify(status int, body string) provider.ErrKind {
	low := strings.ToLower(body)
	hasCaptcha := strings.Contains(low, "captcha") || strings.Contains(low, "verify")
	switch status {
	case http.StatusPaymentRequired:
		return ErrHardCredit
	case http.StatusUnauthorized:
		return ErrSessionDead
	case http.StatusForbidden:
		// 403 + captcha → 验证码失效（Client 级别短冷却，不禁用）；
		// 403 无 captcha → 鉴权失效。
		if hasCaptcha {
			return ErrClient
		}
		return ErrSessionDead
	case http.StatusTooManyRequests:
		return ErrSoftRate
	}
	if status >= 500 {
		return ErrServer
	}
	// 业务 code：1005/3009=rate_limit、1305=overloaded、3007=auth、3001=invalid
	if code := bodyCode(low); code > 0 {
		switch code {
		case 1005, 3009:
			return ErrSoftRate
		case 1305:
			return ErrServer
		case 3007:
			// 3007 = captcha verify failed：通道活着，只是要验证码（实例侧求解
			// 偶发失败）。误判 SessionDead 会把健康账号整池禁用（2026-09-19 修复）。
			return ErrClient
		}
	}
	if hasCaptcha {
		return ErrClient
	}
	if status >= 400 {
		return ErrClient
	}
	return provider.ErrNone
}

// bodyCode 从 {code:123} / {"error":{"code":123}} 提取业务错误码。
func bodyCode(low string) int64 {
	for _, pat := range []string{"\"code\":", "\"errorCode\":"} {
		if i := strings.Index(low, pat); i >= 0 {
			rest := strings.TrimSpace(low[i+len(pat):])
			end := strings.IndexAny(rest, ",}\n")
			if end < 0 {
				end = len(rest)
			}
			if n, err := strconv.ParseInt(strings.TrimSpace(rest[:end]), 10, 64); err == nil {
				return n
			}
		}
	}
	return 0
}

// ---------------------------------------------------------------------------
// 通用请求辅助
// ---------------------------------------------------------------------------

// zcodeHeaders 聊天 / 计费共享头。
func zcodeHeaders(req *http.Request, a *auth.Auth) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "ZCode/3.0.1")
	req.Header.Set("X-ZCode-App-Version", "3.0.1")
	req.Header.Set("X-ZCode-Agent", "glm")
	req.Header.Set("HTTP-Referer", "https://zcode.z.ai/")
	if a.AccessToken != "" {
		req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	}
}

// doJSON 发请求并解 {code,msg,data} 信封；HTTP 非 2xx 或 code != 0 返回 *provider.Error。
func (c *Client) doJSON(req *http.Request, label string) (json.RawMessage, error) {
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return nil, &provider.Error{Kind: c.Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: truncate(string(raw), 200)}
	}
	var env struct {
		Code int             `json:"code"`
		Msg  string          `json:"msg"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return nil, fmt.Errorf("%s parse failed: %w (body: %s)", label, err, truncate(string(raw), 120))
	}
	if env.Code != 0 {
		return nil, &provider.Error{Kind: c.Classify(resp.StatusCode, string(raw)), Status: resp.StatusCode, Msg: fmt.Sprintf("%s code=%d msg=%s", label, env.Code, truncate(env.Msg, 160))}
	}
	return env.Data, nil
}

// ---------------------------------------------------------------------------
// provider.Upstream 实现
// ---------------------------------------------------------------------------

// RefreshToken ZCode JWT 登录态无 refresh 机制：直接视为无需刷新（成功）。
// 登录态死亡由请求期 401/403 触发 Classify → ErrSessionDead → 账号禁用。
func (c *Client) RefreshToken(a *auth.Auth) error {
	log.Printf("zcode refresh: uid=%s jwt 无刷新机制，跳过", a.UID)
	return nil
}

// modelCanonical 模型名大小写归一（上游大小写敏感）。
var modelCanonical = map[string]string{
	"glm-4.7":     "GLM-4.7",
	"glm-5.1":     "GLM-5.1",
	"glm-5.2":     "GLM-5.2",
	"glm-5-turbo": "GLM-5-Turbo",
	"glm-turbo":   "GLM-5-Turbo",
}

// PrepareBody 改写发往上游的 chat 请求体：
//  1. 强制 stream:true（Anthropic 端点回 SSE）
//  2. messages content 字符串 → [{type:"text",text:...}]（Anthropic 协议格式）
//  3. 模型名大小写归一（glm-5.2 → GLM-5.2）
func PrepareBody(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if err := json.Unmarshal(src, &obj); err != nil {
		return src
	}
	obj["stream"] = true
	if m, ok := obj["model"].(string); ok {
		if canonical, hit := modelCanonical[strings.ToLower(m)]; hit {
			obj["model"] = canonical
		} else if !strings.HasPrefix(m, "GLM-") && !strings.HasPrefix(m, "glm-") {
			// 未知模型名保持原样，交由上游校验
		}
	}
	if msgs, ok := obj["messages"].([]any); ok {
		for i, mv := range msgs {
			mm, ok := mv.(map[string]any)
			if !ok {
				continue
			}
			if content, ok := mm["content"].(string); ok {
				mm["content"] = []any{map[string]any{"type": "text", "text": content}}
				msgs[i] = mm
			}
		}
		obj["messages"] = msgs
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// ChatStream 发 chat 请求并返回原始 Anthropic SSE body 流（调用方负责 Close）。
// 直连模式内部处理验证码：首次请求前求解 verifyParam，遇 403+captcha 自动失效重求一次。
// 桥接模式（BridgeURL 非空）转发到本地 zcode-proxy，验证码/指纹由其负责。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	var peek struct {
		Model string `json:"model"`
	}
	_ = json.Unmarshal(body, &peek)
	prepared := PrepareBody(body)

	if c.BridgeURL != "" || (a != nil && a.BridgeURL != "") {
		return c.bridgeChatStream(a, peek.Model, body)
	}

	for attempt := 0; attempt < 2; attempt++ {
		verifyParam := ""
		if c.CaptchaNodeDir != "" {
			vp, verr := c.cm().Get()
			if verr != nil {
				log.Printf("zcode chat_stream uid=%s model=%s: captcha solve failed: %v", a.UID, peek.Model, verr)
				return nil, 0, nil, verr
			}
			verifyParam = vp
		}
		req, err := http.NewRequest(http.MethodPost, c.Base+"/api/v1/zcode-plan/anthropic/v1/messages", bytes.NewReader(prepared))
		if err != nil {
			return nil, 0, nil, err
		}
		zcodeHeaders(req, a)
		req.Header.Set("anthropic-version", "2023-06-01")
		req.Header.Set("Accept", "text/event-stream")
		if verifyParam != "" {
			req.Header.Set("X-Aliyun-Captcha-Verify-Param", verifyParam)
		}
		resp, err := c.streamClient().Do(req)
		if err != nil {
			log.Printf("zcode chat_stream uid=%s model=%s: transport error: %v", a.UID, peek.Model, err)
			return nil, 0, nil, err
		}
		if resp.StatusCode >= 400 {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			kind := c.Classify(resp.StatusCode, string(raw))
			// 403 + captcha → 验证码已失效：作废缓存重求一次
			if resp.StatusCode == http.StatusForbidden && kind == ErrClient && attempt == 0 {
				c.cm().Invalidate()
				log.Printf("zcode chat_stream uid=%s model=%s: captcha invalidated, retry", a.UID, peek.Model)
				continue
			}
			log.Printf("zcode chat_stream uid=%s model=%s: upstream %d %s body=%s",
				a.UID, peek.Model, resp.StatusCode, kind, truncate(string(raw), 200))
			return nil, resp.StatusCode, raw, nil
		}
		return resp.Body, resp.StatusCode, nil, nil
	}
	return nil, 0, nil, fmt.Errorf("zcode captcha retry exhausted")
}

// bridgeChatStream 桥接模式：转发到本地 zcode-proxy 的 OpenAI 端点
// （/v1/chat/completions），支持标准 OpenAI 多工具调用与完整流式 SSE。
// 目标实例：账号级 auth.BridgeURL 优先（每账号一个 zcode-proxy 实例），
// 缺省回退全局 c.BridgeURL。
func (c *Client) bridgeChatStream(a *auth.Auth, model string, rawBody []byte) (io.ReadCloser, int, []byte, error) {
	bridge := c.BridgeURL
	if a != nil && strings.TrimSpace(a.BridgeURL) != "" {
		bridge = strings.TrimRight(a.BridgeURL, "/")
	}
	var reqMap map[string]any
	if err := json.Unmarshal(rawBody, &reqMap); err == nil {
		if m, ok := reqMap["model"].(string); ok && strings.HasPrefix(m, "zcode/") {
			reqMap["model"] = strings.TrimPrefix(m, "zcode/")
			if b, err := json.Marshal(reqMap); err == nil {
				rawBody = b
			}
		}
	}
	req, err := http.NewRequest(http.MethodPost, bridge+"/v1/chat/completions", bytes.NewReader(rawBody))
	if err != nil {
		return nil, 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	if c.BridgeKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.BridgeKey)
	}
	resp, err := c.streamClient().Do(req)
	if err != nil {
		log.Printf("zcode bridge uid=%s model=%s: transport error: %v", a.UID, model, err)
		return nil, 0, nil, friendlyBridgeErr(err)
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		kind := c.Classify(resp.StatusCode, string(raw))
		log.Printf("zcode bridge uid=%s model=%s: upstream %d %s body=%s",
			a.UID, model, resp.StatusCode, kind, truncate(string(raw), 200))
		return nil, resp.StatusCode, raw, nil
	}
	return resp.Body, resp.StatusCode, nil, nil
}

// zcodeModels 静态模型目录（zcode2api AVAILABLE_MODELS；上下文/输出上限来自官方）。
var zcodeModels = []provider.ModelInfo{
	{ID: "GLM-5.2", Name: "GLM-5.2", ContextWindow: 1000000, MaxTokens: 96000},
	{ID: "GLM-5-Turbo", Name: "GLM-5-Turbo", ContextWindow: 136000, MaxTokens: 65024},
}

// bridgeModels 桥接模式（zcode-proxy）可用模型，与 zcode-proxy config.yaml 的
// models 列表一致（小写；zcode-proxy 侧负责模型名映射）。
var bridgeModels = []provider.ModelInfo{
	{ID: "glm-5.3-flash", Name: "GLM-5.3-Flash (ZCode)", ContextWindow: 1000000, MaxTokens: 96000},
	{ID: "glm-5.3", Name: "GLM-5.3 (ZCode)", ContextWindow: 1000000, MaxTokens: 96000},
}

// FetchModels 返回 ZCode 可用模型：桥接模式走 bridgeModels，直连模式静态目录。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	models := zcodeModels
	if c.BridgeURL != "" {
		models = bridgeModels
	}
	out := make([]provider.ModelInfo, len(models))
	copy(out, models)
	return out, nil
}

// FetchModelPricing ZCode 官方无公开积分倍率，返回空列表并由前端静态费率兜底。
func (c *Client) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	return nil, nil
}

// UserResource 剩余额度汇总，复用 UserResourceDetail。
func (c *Client) UserResource(a *auth.Auth) (int64, error) {
	remain, _, err := c.UserResourceDetail(a)
	return remain, err
}

// UserResourceDetail 额度明细：GET /api/v1/zcode-plan/billing/balance。
// 桥接模式下禁用：billing 端点对 API 形态常 3012，定时刷新的失败请求
// 会持续喂养账号级风控（2026-09-18 排查结论）。
// 桥接模式改为查询本地实例 /quota（实例带完整身份头查 billing，安全且真实：
// 返回 balances 的 token 数；2026-09-18 实测通过）。
func (c *Client) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	if c.BridgeURL != "" {
		return c.bridgeUserResource(a)
	}
	resp, err := c.authDo(a, c.Base+"/api/v1/zcode-plan/billing/balance")
	if err != nil {
		return 0, nil, err
	}
	data, err := c.doJSON(resp, "zcode billing")
	if err != nil {
		return 0, nil, err
	}
	var env struct {
		Balances []struct {
			ShowName       string `json:"show_name"`
			Model          string `json:"model"`
			TotalUnits     int64  `json:"total_units"`
			UsedUnits      int64  `json:"used_units"`
			RemainingUnits int64  `json:"remaining_units"`
			ExpiresAt      any    `json:"expires_at"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		return 0, nil, fmt.Errorf("zcode billing parse failed: %w (body: %s)", err, truncate(string(data), 160))
	}
	var remain int64
	items := make([]provider.ResourceItem, 0, len(env.Balances))
	for _, b := range env.Balances {
		name := b.ShowName
		if name == "" {
			name = b.Model
		}
		if name == "" {
			name = "model"
		}
		if b.RemainingUnits < 0 {
			b.RemainingUnits = 0
		}
		remain += b.RemainingUnits
		items = append(items, provider.ResourceItem{
			Name:     strings.ReplaceAll(name, " ", "-"),
			Total:    b.TotalUnits,
			Used:     b.UsedUnits,
			Remain:   b.RemainingUnits,
			ExpireAt: parseExpire(b.ExpiresAt),
		})
	}
	return remain, items, nil
}

// zcodeInstanceNames 本地桥端口 → 实例显示名（与 bridge.Manager 的实例清单对应，
// 用于把连接错误翻译成用户能看懂的提示）。
var zcodeInstanceNames = map[string]string{
	"18083": "主号bigmodel acc-big",
	"18084": "副卡gmail acc-fuka",
	"18085": "副卡bigmodel acc-cn",
	"18086": "主号gmail acc-fuka2",
}

// friendlyBridgeErr 把本地桥实例的连接失败翻译成可操作的中文提示。
// 原始错误形如 `Get "http://127.0.0.1:18085/quota": dial tcp ... connectex:
// No connection could be made ...`，用户读不出"桥没启动、面板可一键拉起、
// 账号没坏"这个事实（2026-09-27 用户体验修复）。
func friendlyBridgeErr(err error) error {
	if err == nil {
		return nil
	}
	msg := err.Error()
	i := strings.Index(msg, "127.0.0.1:")
	if i < 0 {
		return err // 不是本机地址（如自建桥指向远程）→ 原样返回
	}
	port := msg[i+len("127.0.0.1:"):]
	if j := strings.IndexAny(port, "/\"' :"); j >= 0 {
		port = port[:j]
	}
	name := zcodeInstanceNames[port]
	if name == "" {
		name = "zcode 桥实例"
	}
	if strings.Contains(msg, "refused") || strings.Contains(msg, "connectex") {
		return provider.BridgeDown(fmt.Errorf("zcode 桥实例未启动（%s，端口 %s）——账号本身没坏，打开面板「本地桥」页点启动即可恢复", name, port))
	}
	if strings.Contains(msg, "context deadline exceeded") || strings.Contains(msg, "Client.Timeout") {
		return provider.BridgeDown(fmt.Errorf("zcode 桥实例无响应（%s，端口 %s）——可到面板「本地桥」页重启该实例（原始错误: %v）", name, port, err))
	}
	return err
}

// bridgeUserResource 桥接模式额度：GET {账号实例}/quota（Bearer bridgeKey），
// 返回 balances 的 token 数（showName/remainingUnits/totalUnits/usedUnits/
// unitType/expiresAt，camelCase，见 routes-quota.ts）。
func (c *Client) bridgeUserResource(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	bridge := c.BridgeURL
	if a != nil && strings.TrimSpace(a.BridgeURL) != "" {
		bridge = strings.TrimRight(a.BridgeURL, "/")
	}
	req, err := http.NewRequest(http.MethodGet, bridge+"/quota", nil)
	if err != nil {
		return 0, nil, err
	}
	if c.BridgeKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.BridgeKey)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, nil, friendlyBridgeErr(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 400 {
		return 0, nil, fmt.Errorf("zcode bridge quota: HTTP %d %s", resp.StatusCode, truncate(string(raw), 160))
	}
	var env struct {
		Balances []struct {
			ShowName       string `json:"showName"`
			TotalUnits     int64  `json:"totalUnits"`
			UsedUnits      int64  `json:"usedUnits"`
			RemainingUnits int64  `json:"remainingUnits"`
			UnitType       string `json:"unitType"`
			ExpiresAt      any    `json:"expiresAt"`
		} `json:"balances"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return 0, nil, fmt.Errorf("zcode bridge quota parse failed: %w (body: %s)", err, truncate(string(raw), 160))
	}
	var remain int64
	items := make([]provider.ResourceItem, 0, len(env.Balances))
	for _, b := range env.Balances {
		name := b.ShowName
		if name == "" {
			name = "model"
		}
		if b.RemainingUnits < 0 {
			b.RemainingUnits = 0
		}
		remain += b.RemainingUnits
		items = append(items, provider.ResourceItem{
			Name:     strings.ReplaceAll(name, " ", "-"),
			Total:    b.TotalUnits,
			Used:     b.UsedUnits,
			Remain:   b.RemainingUnits,
			ExpireAt: parseExpire(b.ExpiresAt),
		})
	}
	return remain, items, nil
}

// authDo 构造带 Bearer 的 GET 请求。
func (c *Client) authDo(a *auth.Auth, url string) (*http.Request, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	zcodeHeaders(req, a)
	return req, nil
}

// parseExpire 解析 expires_at（Unix 秒 / 毫秒 / UnixNano / ISO 字符串 → Unix 秒）。
func parseExpire(v any) int64 {
	switch t := v.(type) {
	case float64:
		return normalizeExpire(int64(t))
	case string:
		if n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64); err == nil {
			return normalizeExpire(n)
		}
		if tv, err := time.Parse(time.RFC3339, t); err == nil {
			return tv.Unix()
		}
	}
	return 0
}

func normalizeExpire(n int64) int64 {
	switch {
	case n <= 0:
		return 0
	case n > 9999999999999: // ns
		return n / 1e9
	case n > 9999999999: // ms
		return n / 1000
	default: // s
		return n
	}
}

// DailyCheckin ZCode 无签到活动。
func (c *Client) DailyCheckin(a *auth.Auth) error { return fmt.Errorf("zcode 暂无签到活动") }

// ---------------------------------------------------------------------------
// Anthropic SSE → OpenAI SSE 翻译
// ---------------------------------------------------------------------------

// anthropicEvent 上游 SSE 数据行。
type anthropicEvent struct {
	Type  string `json:"type"`
	Index int    `json:"index"`
	ID    string `json:"id"`
	Model string `json:"model"`
	Delta struct {
		Type       string `json:"type"`
		Text       string `json:"text"`
		Thinking   string `json:"thinking"`
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage struct {
			InputTokens  int64 `json:"input_tokens"`
			OutputTokens int64 `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`
	ContentBlock struct {
		Type string `json:"type"`
	} `json:"content_block"`
	Usage struct {
		OutputTokens int64 `json:"output_tokens"`
	} `json:"usage"`
}

// Stream 将上游 Anthropic SSE 翻译为 OpenAI 流式 SSE 后透传给 w。
// 逐事件转换：content_block_delta → choices[].delta.content；
// message_delta → finish_reason + usage 尾包；末尾补 [DONE]。
func (c *Client) Stream(w http.ResponseWriter, r io.Reader) error {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)

	writeChunk := func(chunk map[string]any) error {
		raw, _ := json.Marshal(chunk)
		if _, err := io.WriteString(w, "data: "+string(raw)+"\n\n"); err != nil {
			return err
		}
		if fl != nil {
			fl.Flush()
		}
		return nil
	}

	var (
		id, model string
		created   = time.Now().Unix()
		inputTok  int64
		outputTok int64
		streamed  bool
		doneSent  bool
	)

	br := bufio.NewReaderSize(r, 64*1024)
	for {
		line, err := br.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			if strings.TrimSpace(payload) == "[DONE]" {
				fmt.Fprint(w, "data: [DONE]\n\n")
				if fl != nil {
					fl.Flush()
				}
				doneSent = true
				continue
			}
			// 若已经是 OpenAI 格式 chunk（来自 zcode-proxy 桥接通道），直接透传给客户端
			if strings.Contains(payload, "chat.completion.chunk") || strings.Contains(payload, `"choices":`) {
				fmt.Fprintf(w, "data: %s\n\n", payload)
				if fl != nil {
					fl.Flush()
				}
				streamed = true
				continue
			}
			var ev anthropicEvent
			if json.Unmarshal([]byte(payload), &ev) == nil {
				switch ev.Type {
				case "message_start":
					if ev.Message.ID != "" {
						id = ev.Message.ID
					}
					if ev.Message.Model != "" {
						model = ev.Message.Model
					}
					inputTok = ev.Message.Usage.InputTokens
					streamed = true
				case "content_block_start":
					// 跳过（Anthropic 结构）；thinking/text 均从 delta 出
				case "content_block_delta":
					// ZCode 3.12.3 上游：thinking_delta（字段 thinking）先行，
					// text_delta（字段 text）在后；两者都要正确转换（2026-09-18 修复）。
					d := map[string]any{}
					switch ev.Delta.Type {
					case "thinking_delta":
						if ev.Delta.Thinking == "" {
							continue
						}
						d["reasoning_content"] = ev.Delta.Thinking
					default: // text_delta 等
						if ev.Delta.Text == "" {
							continue
						}
						d["content"] = ev.Delta.Text
					}
					chunk := map[string]any{
						"id":      idOrSynth(id),
						"object":  "chat.completion.chunk",
						"created": created,
						"model":   model,
						"choices": []any{
							map[string]any{
								"index":         0,
								"delta":         d,
								"finish_reason": nil,
							},
						},
					}
					if err := writeChunk(chunk); err != nil {
						return err
					}
					streamed = true
				case "message_delta":
					outputTok = ev.Usage.OutputTokens
					// 终包：空内容 + finish_reason
					chunk := map[string]any{
						"id":      idOrSynth(id),
						"object":  "chat.completion.chunk",
						"created": created,
						"model":   model,
						"choices": []any{
							map[string]any{
								"index":         0,
								"delta":         map[string]any{},
								"finish_reason": "stop",
							},
						},
					}
					if err := writeChunk(chunk); err != nil {
						return err
					}
					// 用量尾包（OpenAI 风格，供 token 统计抓取）
					if inputTok > 0 || outputTok > 0 {
						usage := map[string]any{
							"prompt_tokens":     inputTok,
							"completion_tokens": outputTok,
							"total_tokens":      inputTok + outputTok,
						}
						uChunk := map[string]any{
							"id":      idOrSynth(id),
							"object":  "chat.completion.chunk",
							"created": created,
							"model":   model,
							"choices": []any{},
							"usage":   usage,
						}
						if err := writeChunk(uChunk); err != nil {
							return err
						}
					}
					streamed = true
				}
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	if !streamed {
		// 空流兜底：给客户端一个空内容终包，避免挂起
		if err := writeChunk(map[string]any{
			"id": idOrSynth(id), "object": "chat.completion.chunk", "created": created,
			"model":   model,
			"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
		}); err != nil {
			return err
		}
	}
	if !doneSent {
		if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
			return err
		}
		if fl != nil {
			fl.Flush()
		}
	}
	return nil
}

// Aggregate 聚合上游响应为单个 OpenAI chat.completion 响应。
func (c *Client) Aggregate(r io.Reader) (map[string]any, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	// 1. 若上游直接返回了标准 OpenAI JSON 响应（如 bridge 模式且非流式请求）
	var direct map[string]any
	if err := json.Unmarshal(raw, &direct); err == nil {
		if _, ok := direct["choices"]; ok {
			return direct, nil
		}
	}

	// 2. 否则按 SSE 逐行解析（支持 Anthropic SSE 与 OpenAI SSE 聚合）
	var (
		id, model string
		content   strings.Builder
		inputTok  int64
		outputTok int64
		gotText   bool
	)
	scanner := bufio.NewScanner(bytes.NewReader(raw))
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r\n")
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimPrefix(line, "data: ")
			if strings.TrimSpace(payload) == "[DONE]" {
				continue
			}

			// 尝试 OpenAI chunk 格式
			var chunk struct {
				ID      string `json:"id"`
				Model   string `json:"model"`
				Choices []struct {
					Delta struct {
						Content          string `json:"content"`
						ReasoningContent string `json:"reasoning_content"`
					} `json:"delta"`
				} `json:"choices"`
				Usage struct {
					PromptTokens     int64 `json:"prompt_tokens"`
					CompletionTokens int64 `json:"completion_tokens"`
				} `json:"usage"`
			}
			if json.Unmarshal([]byte(payload), &chunk) == nil && len(chunk.Choices) > 0 {
				if chunk.ID != "" {
					id = chunk.ID
				}
				if chunk.Model != "" {
					model = chunk.Model
				}
				if chunk.Usage.PromptTokens > 0 || chunk.Usage.CompletionTokens > 0 {
					inputTok = chunk.Usage.PromptTokens
					outputTok = chunk.Usage.CompletionTokens
				}
				for _, ch := range chunk.Choices {
					if ch.Delta.Content != "" {
						content.WriteString(ch.Delta.Content)
						gotText = true
					}
				}
				continue
			}

			// 尝试 Anthropic 格式
			var ev anthropicEvent
			if json.Unmarshal([]byte(payload), &ev) == nil {
				switch ev.Type {
				case "message_start":
					if ev.Message.ID != "" {
						id = ev.Message.ID
					}
					if ev.Message.Model != "" {
						model = ev.Message.Model
					}
					inputTok = ev.Message.Usage.InputTokens
				case "content_block_delta":
					text := ev.Delta.Text
					if text != "" {
						content.WriteString(text)
						gotText = true
					}
				case "message_delta":
					outputTok = ev.Usage.OutputTokens
				}
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	if !gotText {
		return nil, fmt.Errorf("zcode aggregate: upstream returned no text content")
	}
	resp := map[string]any{
		"id":      idOrSynth(id),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       map[string]any{"role": "assistant", "content": content.String()},
				"finish_reason": "stop",
			},
		},
	}
	if inputTok > 0 || outputTok > 0 {
		resp["usage"] = map[string]any{
			"prompt_tokens":     inputTok,
			"completion_tokens": outputTok,
			"total_tokens":      inputTok + outputTok,
		}
	}
	return resp, nil
}

func idOrSynth(id string) string {
	if id == "" {
		return fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	return id
}

// StaticModels 静态模型兜底。
func StaticModels() []provider.ModelInfo {
	out := make([]provider.ModelInfo, len(zcodeModels))
	copy(out, zcodeModels)
	return out
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// GrowthTravel / GrowthLottery / GrowthTasksDetailed 成长中心功能暂未支持。
func (c *Client) GrowthTravel(a *auth.Auth) error  { return nil }
func (c *Client) GrowthLottery(a *auth.Auth) error { return nil }
func (c *Client) GrowthTasksDetailed(a *auth.Auth) provider.GrowthTasksResult {
	return provider.GrowthTasksResult{Message: "暂未支持"}
}

// ---------------------------------------------------------------------------
// 验证码管理器（Node + jsdom 无浏览器求解，照抄 zcode2api/captcha.py）
// ---------------------------------------------------------------------------

const (
	captchaVerifyTTL = 45 * time.Second // verifyParam 有效 45s
	captchaCfgTTL    = 10 * time.Minute // configs 缓存
	captchaSolveTTL  = 40 * time.Second // 单次求解超时
	captchaMaxRetry  = 4                // 求解重试次数
)

// CaptchaManager 阿里云无痕验证码管理器。
type CaptchaManager struct {
	mu       sync.Mutex
	base     string
	http     *http.Client
	dir      string
	cached   string
	cachedAt time.Time
	cfgRaw   struct {
		Enabled bool
		Prefix  string
		Region  string
		SceneID string
	}
	cfgAt time.Time
}

// NewCaptchaManager 构造验证码管理器。
func NewCaptchaManager(base string, hc *http.Client, dir string) *CaptchaManager {
	if dir == "" {
		dir = "captcha_node"
	}
	return &CaptchaManager{base: base, http: hc, dir: dir}
}

// Get 返回可用 verifyParam（缓存 TTL 内直接复用；并发锁只跑一个求解进程）。
func (m *CaptchaManager) Get() (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cached != "" && time.Since(m.cachedAt) < captchaVerifyTTL {
		return m.cached, nil
	}
	enabled, prefix, region, scene := m.configLocked()
	param, err := m.solve(enabled, prefix, region, scene)
	if err != nil {
		return "", err
	}
	m.cached = param
	m.cachedAt = time.Now()
	return param, nil
}

// Invalidate 作废缓存（403+captcha 时调用，强制下次重求）。
func (m *CaptchaManager) Invalidate() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cached = ""
	m.cachedAt = time.Time{}
}

// configLocked 获取验证码配置（fetch_configs 缓存 10min；缺省走 zcode2api 默认值）。
func (m *CaptchaManager) configLocked() (enabled bool, prefix, region, scene string) {
	if time.Since(m.cfgAt) < captchaCfgTTL {
		return m.cfgRaw.Enabled, m.cfgRaw.Prefix, m.cfgRaw.Region, m.cfgRaw.SceneID
	}
	url := m.base + "/api/v1/client/configs?app_version=3.0.0&platform=win32"
	if resp, err := m.http.Get(url); err == nil {
		defer resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			var env struct {
				Data struct {
					Configs struct {
						Captcha struct {
							Enabled *bool  `json:"enabled"`
							Prefix  string `json:"prefix"`
							Region  string `json:"region"`
							SceneID string `json:"sceneId"`
						} `json:"captcha"`
					} `json:"configs"`
				} `json:"data"`
			}
			if json.Unmarshal(raw, &env) == nil {
				if env.Data.Configs.Captcha.SceneID != "" {
					ok := env.Data.Configs.Captcha.Enabled == nil || *env.Data.Configs.Captcha.Enabled
					m.cfgRaw = struct {
						Enabled bool
						Prefix  string
						Region  string
						SceneID string
					}{ok, env.Data.Configs.Captcha.Prefix, env.Data.Configs.Captcha.Region, env.Data.Configs.Captcha.SceneID}
					m.cfgAt = time.Now()
				}
			}
		}
	}
	// 缺省与 zcode2api 一致
	if m.cfgRaw.SceneID == "" {
		m.cfgRaw = struct {
			Enabled bool
			Prefix  string
			Region  string
			SceneID string
		}{true, "no8xfe", "sgp", "11xygtvd"}
	}
	return m.cfgRaw.Enabled, m.cfgRaw.Prefix, m.cfgRaw.Region, m.cfgRaw.SceneID
}

// solve 运行 node solver.js scene region prefix，解析 stdout 中的 VERIFY_PARAM= 行。
func (m *CaptchaManager) solve(enabled bool, prefix, region, scene string) (string, error) {
	if !enabled {
		return "", fmt.Errorf("captcha disabled")
	}
	if prefix == "" {
		prefix = "no8xfe"
	}
	if region == "" {
		region = "sgp"
	}
	if scene == "" {
		scene = "11xygtvd"
	}
	var lastErr error
	for i := 0; i < captchaMaxRetry; i++ {
		param, err := m.runSolver(scene, region, prefix)
		if err == nil {
			if i > 0 {
				log.Printf("zcode captcha solve success on attempt %d", i+1)
			}
			return param, nil
		}
		lastErr = err
		log.Printf("zcode captcha solve attempt %d/%d failed: %v", i+1, captchaMaxRetry, err)
	}
	return "", fmt.Errorf("captcha solve failed: %v", lastErr)
}

// runSolver 执行一次求解进程，超时 40s。
func (m *CaptchaManager) runSolver(scene, region, prefix string) (string, error) {
	solver := m.dir + "/solver.js"
	ctx, cancel := context.WithTimeout(context.Background(), captchaSolveTTL)
	defer cancel()
	cmd := exec.CommandContext(ctx, "node", solver, scene, region, prefix)
	cmd.Dir = m.dir
	platform.HideWindow(cmd)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return "", fmt.Errorf("solver timeout (40s)")
		}
		return "", fmt.Errorf("solver run: %v (stderr: %s)", err, truncate(errb.String(), 200))
	}
	for _, line := range strings.Split(out.String(), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "VERIFY_PARAM=") {
			v := strings.TrimSpace(strings.TrimPrefix(line, "VERIFY_PARAM="))
			if v != "" {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("no VERIFY_PARAM in solver output")
}
