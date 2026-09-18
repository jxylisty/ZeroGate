// Package atria 实现上海人工智能实验室 Atria Dawn Preview 模型 API 的上游客户端。
//
// 官方服务说明：
//   - 接口格式：OpenAI 兼容端点 POST https://api.atria-asi.ai/v1/chat/completions
//   - 鉴权方式：Authorization: Bearer <atr_xxx>
//   - 模型标识：Atria-Dawn-Preview（区分大小写，256K 上下文）
//   - 活动配额：新账号赠送 1 亿（100M）Token 体验额度
package atria

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
	"wild-work/internal/upstream"
)

const (
	DefaultBaseURL = "https://api.atria-asi.ai/v1"
	ChatPath       = "/chat/completions"
	ModelID        = "Atria-Dawn-Preview"
	DefaultQuota   = 100000000 // 新用户赠送 1 亿 Token
)

// Client Atria 上游客户端。
type Client struct {
	HTTP    *http.Client
	Base    string // 基础 URL，默认 https://api.atria-asi.ai/v1
	Channel string // "atria"
}

// New 实例化默认 Atria 客户端。
func New() *Client {
	base := os.Getenv("ATRIA_BASE_URL")
	if strings.TrimSpace(base) == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")

	tr := &http.Transport{
		MaxIdleConns:        100,
		MaxIdleConnsPerHost: 20,
		IdleConnTimeout:     90 * time.Second,
	}
	return &Client{
		HTTP:    &http.Client{Timeout: 180 * time.Second, Transport: tr},
		Base:    base,
		Channel: "atria",
	}
}

// ---------------------------------------------------------------------------
// 错误分类
// ---------------------------------------------------------------------------

// Classify 按 HTTP 状态码 + body 判定错误类别，驱动 pool 状态机。
func (c *Client) Classify(status int, body string) provider.ErrKind {
	low := strings.ToLower(body)
	switch {
	case status == http.StatusUnauthorized || strings.Contains(low, "invalid api key") || strings.Contains(low, "unauthorized"):
		return provider.ErrSessionDead
	case status == http.StatusTooManyRequests:
		if strings.Contains(low, "quota") || strings.Contains(low, "insufficient") || strings.Contains(low, "balance") {
			return provider.ErrHardCredit
		}
		return provider.ErrSoftRate
	case status == http.StatusNotFound:
		return provider.ErrNotFound
	case status >= 500:
		return provider.ErrServer
	case status >= 400:
		return provider.ErrClient
	}
	return provider.ErrNone
}

// ---------------------------------------------------------------------------
// provider.Upstream 接口实现
// ---------------------------------------------------------------------------

// RefreshToken API Key 为持久凭证，无需动态刷新。
func (c *Client) RefreshToken(a *auth.Auth) error {
	return nil
}

// ChatStream 转发到 Atria 官方 ChatCompletions 接口。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	// 归一化 body：
	// 1. 强制 stream: true
	// 2. 移除 max_tokens（GLM 系思考模型会将 max_tokens 当作思考+正文总预算，容易出现 empty_model_response）
	// 3. 确保 model 字段为 Atria-Dawn-Preview
	var obj map[string]any
	if json.Unmarshal(body, &obj) == nil {
		obj["stream"] = true
		delete(obj, "max_tokens")
		obj["model"] = ModelID
		if body2, e := json.Marshal(obj); e == nil {
			body = body2
		}
	}

	url := c.Base + ChatPath
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, err
	}

	token := strings.TrimSpace(a.AccessToken)
	if !strings.HasPrefix(strings.ToLower(token), "bearer ") {
		token = "Bearer " + token
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Authorization", token)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		log.Printf("atria chat_stream uid=%s: transport error: %v", a.UID, err)
		return nil, 0, nil, err
	}

	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		kind := c.Classify(resp.StatusCode, string(raw))
		log.Printf("atria chat_stream uid=%s: upstream %d %s body=%s",
			a.UID, resp.StatusCode, kind, truncate(string(raw), 200))
		return nil, resp.StatusCode, raw, nil
	}

	return resp.Body, resp.StatusCode, nil, nil
}

// StaticModels 静态模型目录。
func StaticModels() []provider.ModelInfo {
	return []provider.ModelInfo{
		{
			ID:            ModelID,
			Name:          "Atria Dawn Preview (GLM-5.2 744B MoE)",
			ContextWindow: 262144, // 256K
			MaxTokens:     65536,
		},
	}
}

// FetchModels 返回 Atria 可用模型。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	return StaticModels(), nil
}

// FetchModelPricing 模型积分定价标签。
func (c *Client) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	return []provider.ModelPricing{
		{
			Model:   ModelID,
			Channel: "atria",
			Rate:    0,
			Note:    "官方限免体验 · 赠送 1 亿 Token · 256K 上下文",
		},
	}, nil
}

// UserResource 返回当前可用 Token 额度。
func (c *Client) UserResource(a *auth.Auth) (int64, error) {
	return DefaultQuota, nil
}

// UserResourceDetail 积分与套餐明细展示。
func (c *Client) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	items := []provider.ResourceItem{
		{
			Name:     "新用户体验包",
			Total:    DefaultQuota,
			Used:     0,
			Remain:   DefaultQuota,
			ExpireAt: 0, // 长期有效
		},
	}
	return DefaultQuota, items, nil
}

// DailyCheckin Atria 渠道无每日签到活动。
func (c *Client) DailyCheckin(a *auth.Auth) error {
	return fmt.Errorf("atria 渠道无签到活动")
}

// Stream 透传 OpenAI SSE 流。
func (c *Client) Stream(w http.ResponseWriter, r io.Reader) error {
	return upstream.Stream(w, r)
}

// Aggregate 聚合 SSE 为非流式 JSON。
func (c *Client) Aggregate(r io.Reader) (map[string]any, error) {
	return upstream.Aggregate(r)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
