// Package openaicompat 通用 OpenAI 兼容 Upstream（借 Atria 模式）。
// 一个实现服务三个白嫖渠道：copilot(4141)/doubao(7868)/gptgemini(8317)。
// BaseURL + ApiKey 来自 auths/<channel>.json；不依赖登录态刷新、签到/成长体系。
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
	"zerogate/internal/upstream"
)

// Client 通用 OpenAI 兼容客户端。
type Client struct {
	BaseURL string
	ApiKey  string
	// ChatPath 聊天端点路径，缺省 /v1/chat/completions。
	// 云端渠道路径各异：智谱 /api/paas/v4/chat/completions、Groq /openai/v1/chat/completions。
	ChatPath string
	// PerAccountKey 标记该渠道支持「按号取 key」（云端多账号，如 tierflow/groq）。
	// 本地无鉴权桥（copilot/doubao/gptgemini，New 构造）为 false：它们的 auth
	// 文件 accessToken 是占位符，绝不能当作真实 Bearer 发给上游（会 401）。
	PerAccountKey bool
}

// chatEndpoint 返回聊天端点完整 URL。
func (c *Client) chatEndpoint() string {
	if c.ChatPath != "" {
		return c.BaseURL + c.ChatPath
	}
	return c.BaseURL + "/v1/chat/completions"
}

// keyFor 返回本次请求应使用的 Bearer Key。仅当渠道声明支持按号取 key
// （PerAccountKey，由 NewWithKey 置位）且账号带了非空 token 时才用按号的；
// 否则一律用 client 固化的 c.ApiKey——本地无鉴权桥用 New 构造，
// c.ApiKey 为空、按号 token 为占位符，必须走这条回退，否则会把占位符
// 当真实 Bearer 发出去导致 401。
func (c *Client) keyFor(a *auth.Auth) string {
	if c.PerAccountKey && a != nil {
		if t := strings.TrimSpace(a.AccessToken); t != "" {
			return t
		}
	}
	return c.ApiKey
}

// ChatStream POST <chatEndpoint>(stream=true)。
// 统一强制 stream:true：本实现 Stream/Aggregate 只吃上游 SSE，非流式
// 客户端由 handler 的 Aggregate 聚合回标准 JSON（与 WorkBuddy 同款约定）。
//
// 超时/取消生命周期说明：request context 的 cancel 绑定到返回的响应体上
// （cancelOnCloseReader），函数返回后流仍需持续读取——若 defer cancel()，
// SSE 流会在函数返回瞬间被 context canceled 掐断（实测 stream aborted）。
// handler 侧对 rc 有 defer Close()，读取结束即释放。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil {
		obj["stream"] = true
		if nb, merr := json.Marshal(obj); merr == nil {
			body = nb
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	req, err := http.NewRequestWithContext(ctx, "POST", c.chatEndpoint(), bytes.NewReader(body))
	if err != nil {
		cancel()
		return nil, 0, nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.keyFor(a))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		// 本地桥（127.0.0.1，copilot/doubao/gptgemini）连不上属桥进程问题，
		// 与账号凭证/额度无关 → 标记 BridgeDown，避免整池账号被误罚长冷却。
		if provider.IsLoopbackURL(c.BaseURL) {
			return nil, 0, nil, provider.BridgeDown(fmt.Errorf("do request: %w", err))
		}
		return nil, 0, nil, fmt.Errorf("do request: %w", err)
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		cancel()
		return nil, resp.StatusCode, raw, nil
	}
	// 120s 是"整条流最长生命周期"兜底（含连接建立）；每行读取由客户端驱动。
	return newCancelOnCloseReader(resp.Body, cancel), resp.StatusCode, nil, nil
}

// cancelOnCloseReader 包装上游响应体：Close 时同步释放 request context，
// 防止超时 context 泄漏。流式读取期间不触发 cancel。
type cancelOnCloseReader struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func newCancelOnCloseReader(rc io.ReadCloser, cancel context.CancelFunc) io.ReadCloser {
	return &cancelOnCloseReader{ReadCloser: rc, cancel: cancel}
}

func (r *cancelOnCloseReader) Close() error {
	err := r.ReadCloser.Close()
	r.cancel()
	return err
}

// FetchModels GET /v1/models。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.keyFor(a))

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<10))
		return nil, fmt.Errorf("models %d: %s", resp.StatusCode, raw)
	}
	var m struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		return nil, fmt.Errorf("decode models: %w", err)
	}
	out := make([]provider.ModelInfo, 0, len(m.Data))
	for _, d := range m.Data {
		out = append(out, provider.ModelInfo{ID: d.ID, Name: d.ID})
	}
	return out, nil
}

// FetchModelPricing 不支持返回空。
func (c *Client) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	return nil, nil
}

// UserResource 不支持返回 0。
func (c *Client) UserResource(a *auth.Auth) (int64, error) {
	return 0, nil
}

// UserResourceDetail 不支持返回空。
func (c *Client) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	return 0, nil, nil
}

// DailyCheckin/GrowthTravel/GrowthLottery/GrowthTasksDetailed 不支持无操作。
func (c *Client) DailyCheckin(a *auth.Auth) error  { return nil }
func (c *Client) GrowthTravel(a *auth.Auth) error  { return nil }
func (c *Client) GrowthLottery(a *auth.Auth) error { return nil }
func (c *Client) GrowthTasksDetailed(a *auth.Auth) provider.GrowthTasksResult {
	return provider.GrowthTasksResult{}
}

// RefreshToken 不支持无操作。
func (c *Client) RefreshToken(a *auth.Auth) error { return nil }

// Classify 标准 OpenAI 错误码分类。
// 云端渠道特判：403 多为地区限制/WAF 拦截（如 Groq 国内直连），按 404 同款
// 短冷却处理而不是 session_dead，避免唯一账号被一次性禁用。
func (c *Client) Classify(status int, body string) provider.ErrKind {
	switch status {
	case 429:
		return provider.ErrSoftRate
	case 402:
		return provider.ErrHardCredit
	case 401:
		return provider.ErrSessionDead
	case 403, 404:
		return provider.ErrNotFound
	case 500, 502, 503, 504:
		// body 呈网络层故障（DNS/连接/读写）→ 上游不可达，非账号问题
		if provider.LooksUpstreamUnreachable(body) {
			return provider.ErrUpstreamDown
		}
		return provider.ErrServer
	default:
		return provider.ErrClient
	}
}

// Stream 透传上游 SSE 到客户端（r = ChatStream 返回的上游响应体）。
// 委托 upstream.StreamWithThink：逐行 flush + 剥离思考字段 + 保证 [DONE]，
// 与 WorkBuddy/copilot 等渠道走同一套 SSE 处理。绝不二次 POST——
// 本实现早期版本误将上游响应当请求体重发，导致流式 400。
func (c *Client) Stream(w http.ResponseWriter, r io.Reader) error {
	return upstream.StreamWithThink(w, r, false)
}

// Aggregate 聚合上游 SSE 为非流式响应（r = 上游响应体）。
func (c *Client) Aggregate(r io.Reader) (map[string]any, error) {
	return upstream.AggregateWithThink(r, true)
}

// ThinkPassthrough 支持思考字段透传（chatui 带 think:true 时保留 reasoning_content）。
func (c *Client) StreamWithThink(w http.ResponseWriter, r io.Reader, passthrough bool) error {
	return upstream.StreamWithThink(w, r, passthrough)
}

func (c *Client) AggregateWithThink(r io.Reader, keepReasoning bool) (map[string]any, error) {
	return upstream.AggregateWithThink(r, keepReasoning)
}

// New 创建通用 OpenAI 兼容客户端（本地桥接服务，无鉴权）。
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL}
}

// NewWithKey 创建带 Bearer 鉴权的 OpenAI 兼容客户端（云端免费层渠道）。
// 置 PerAccountKey=true：运行时优先用「按号传入的 auth.AccessToken」，
// 以支持一个渠道挂多个 key 轮换（apiKey 仅作账号 token 缺失时的兜底）。
func NewWithKey(baseURL, apiKey, chatPath string) *Client {
	return &Client{BaseURL: baseURL, ApiKey: apiKey, ChatPath: chatPath, PerAccountKey: true}
}

// StaticModels 白嫖渠道静态模型列表（占位，实际由 FetchModels 动态获取）。
func StaticModels() []provider.ModelInfo {
	return []provider.ModelInfo{
		{ID: "default", Name: "Default Model"},
	}
}
