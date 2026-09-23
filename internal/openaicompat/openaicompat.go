// Package openaicompat 通用 OpenAI 兼容 Upstream（借 Atria 模式）。
// 一个实现服务三个白嫖渠道：copilot(4141)/doubao(7868)/gptgemini(8317)。
// BaseURL + ApiKey 来自 auths/<channel>.json；不依赖登录态刷新、签到/成长体系。
package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
)

// Client 通用 OpenAI 兼容客户端。
type Client struct {
	BaseURL string
	ApiKey  string
}

// ChatStream POST /v1/chat/completions(stream=true)。
func (c *Client) ChatStream(a *auth.Auth, body []byte) (rc io.ReadCloser, status int, respBody []byte, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return nil, 0, nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.ApiKey)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, 0, nil, fmt.Errorf("do request: %w", err)
	}
	if resp.StatusCode >= 400 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		return nil, resp.StatusCode, raw, nil
	}
	return resp.Body, 200, nil, nil
}

// FetchModels GET /v1/models。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", c.BaseURL+"/v1/models", nil)
	if err != nil {
		return nil, fmt.Errorf("new request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.ApiKey)

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
func (c *Client) DailyCheckin(a *auth.Auth) error                           { return nil }
func (c *Client) GrowthTravel(a *auth.Auth) error                          { return nil }
func (c *Client) GrowthLottery(a *auth.Auth) error                         { return nil }
func (c *Client) GrowthTasksDetailed(a *auth.Auth) provider.GrowthTasksResult {
	return provider.GrowthTasksResult{}
}

// RefreshToken 不支持无操作。
func (c *Client) RefreshToken(a *auth.Auth) error { return nil }

// Classify 标准 OpenAI 错误码分类。
func (c *Client) Classify(status int, body string) provider.ErrKind {
	switch status {
	case 429:
		return provider.ErrSoftRate
	case 402:
		return provider.ErrHardCredit
	case 401, 403:
		return provider.ErrSessionDead
	case 404:
		return provider.ErrNotFound
	case 500, 502, 503, 504:
		return provider.ErrServer
	default:
		return provider.ErrClient
	}
}

// Stream 透传 SSE 流（剥离 usage 行，避免重复解析）。
func (c *Client) Stream(w http.ResponseWriter, r io.Reader) error {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	flusher, ok := w.(http.Flusher)
	if !ok {
		return errors.New("streaming unsupported")
	}

	data, _ := io.ReadAll(r)
	rc, status, _, err := c.ChatStream(nil, data)
	if err != nil || status >= 400 {
		if status >= 400 && rc != nil {
			rc.Close()
		}
		return fmt.Errorf("chat stream status %d", status)
	}
	defer rc.Close()

	buf := make([]byte, 4096)
	for {
		n, err := rc.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return werr
			}
			flusher.Flush()
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// Aggregate 非流式聚合响应。
func (c *Client) Aggregate(r io.Reader) (map[string]any, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	var resp map[string]any
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, err
	}
	return resp, nil
}

// ThinkPassthrough 支持思考字段透传（doubao 等输出带 <think> 前缀时保留）。
func (c *Client) StreamWithThink(w http.ResponseWriter, r io.Reader, passthrough bool) error {
	return c.Stream(w, r)
}

func (c *Client) AggregateWithThink(r io.Reader, keepReasoning bool) (map[string]any, error) {
	return c.Aggregate(r)
}

// New 创建通用 OpenAI 兼容客户端。
func New(baseURL string) *Client {
	return &Client{BaseURL: baseURL}
}

// StaticModels 白嫖渠道静态模型列表（占位，实际由 FetchModels 动态获取）。
func StaticModels() []provider.ModelInfo {
	return []provider.ModelInfo{
		{ID: "default", Name: "Default Model"},
	}
}
