// models.go 动态模型获取：COSY 签名 GET /algo/api/v2/model/list?Encode=1，
// 拿 chat scene 的 key 列表 → provider.ModelInfo。
// 移植自 qoderwork2api internal/upstream/models.go。
package qoder

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
)

// DynamicModel 上游 chat scene 单个模型。
type DynamicModel struct {
	Key            string  `json:"key"`
	DisplayName    string  `json:"display_name"`
	Enable         bool    `json:"enable"`
	IsReasoning    bool    `json:"is_reasoning"`
	IsVL           bool    `json:"is_vl"`
	MaxInputTokens int64   `json:"max_input_tokens"`
	PriceFactor    float64 `json:"price_factor"`
	// context_config 官方档位表：200K(默认)/400K/1M 可选（max_input_tokens
	// 只是基值，实测 180K 非硬限）。空 = 无档位选择。
	ContextConfig map[string]struct {
		TokenCount int64 `json:"token_count"`
		IsDefault  bool  `json:"is_default"`
	} `json:"context_config"`
}

// contextCeiling 广播的上下文上限。上游档位表最大 1M，但阿里 SLB 对大请求
// 有约 80s 超时：实测 350K tokens 稳定通过（17s）、450K 超时（79s），
// 故广播到官方 400K 档为止。需要更高时改这个常量（风险自负）。
const contextCeiling int64 = 400_000

// qoderContextWindow 计算对外广播的上下文窗口。
// 优先级：context_config 中 ≤ ceiling 的最大档位 > max_input_tokens（上游对
// 个别模型的明示值，如 GLM-5.3-Flash=1M，此时尊重上游）> 默认 180000。
func qoderContextWindow(m DynamicModel) int64 {
	tier := int64(0)
	for _, t := range m.ContextConfig {
		if t.TokenCount > tier && t.TokenCount <= contextCeiling {
			tier = t.TokenCount
		}
	}
	if m.MaxInputTokens > tier {
		tier = m.MaxInputTokens
	}
	if tier > 0 {
		return tier
	}
	return 180_000
}

// fetchModels 调上游动态模型接口。
// GET 无 body，签名用空串 ""（非 "{}"，后者 403 Signature invalid）。
func (c *Client) fetchModels(a *auth.Auth) ([]DynamicModel, error) {
	dt := a.JWT()
	if dt == "" {
		return nil, fmt.Errorf("no dt- available")
	}
	rawURL := c.gatewayBase() + EpModels
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	sess, err := NewCosySession(a.MachineID, a.MachineToken, a.MachineType, a.Nickname, a.UID, dt, a.RefreshToken)
	if err != nil {
		return nil, fmt.Errorf("cosy session: %w", err)
	}
	if err := sess.ApplyHeaders(req, "", rawURL, a.UID, false, ""); err != nil {
		return nil, fmt.Errorf("cosy headers: %w", err)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return nil, fmt.Errorf("models api status %d: %s", resp.StatusCode, truncate(string(raw), 300))
	}
	var apiResp map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&apiResp); err != nil {
		return nil, fmt.Errorf("models parse: %w", err)
	}
	chatRaw, ok := apiResp["chat"]
	if !ok {
		return nil, fmt.Errorf("no chat scene in models response")
	}
	var models []DynamicModel
	if err := json.Unmarshal(chatRaw, &models); err != nil {
		return nil, fmt.Errorf("chat scene parse: %w", err)
	}
	enabled := make([]DynamicModel, 0, len(models))
	for _, m := range models {
		if m.Enable && m.Key != "" {
			enabled = append(enabled, m)
		}
	}
	if len(enabled) == 0 {
		return nil, fmt.Errorf("no enabled chat models")
	}
	return enabled, nil
}

// FetchModels 实现 provider.Upstream：动态模型 → provider.ModelInfo。
// 客户端名 = display_name 规范化（无 display_name 用 key 兜底）。
// 同时把 客户端名→key 映射缓存到 Client，供 ChatStream 路由。
func (c *Client) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	dyn, err := c.fetchModels(a)
	if err != nil {
		return nil, err
	}
	mm := make(map[string]string, len(dyn))
	out := make([]provider.ModelInfo, 0, len(dyn))
	for _, m := range dyn {
		name := NormalizeModelName(m.DisplayName)
		if name == "" {
			name = m.Key
		}
		mm[name] = m.Key
		mi := provider.ModelInfo{
			ID:            name,
			Name:          m.DisplayName,
			ContextWindow: qoderContextWindow(m),
		}
		out = append(out, mi)
	}
	c.setModelMap(mm)
	if len(out) == 0 {
		return nil, fmt.Errorf("models api returned empty list")
	}
	return out, nil
}

// FetchModelPricing 实现 provider.Upstream：Qoder 模型带 price_factor，
// 直接复用 FetchModels 的倍率字段。
func (c *Client) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	dyn, err := c.fetchModels(a)
	if err != nil {
		return nil, err
	}
	out := make([]provider.ModelPricing, 0, len(dyn))
	for _, m := range dyn {
		name := NormalizeModelName(m.DisplayName)
		if name == "" {
			name = m.Key
		}
		out = append(out, provider.ModelPricing{
			Model:   name,
			Channel: "qoder",
			Rate:    m.PriceFactor,
		})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("pricing api returned empty models")
	}
	return out, nil
}

// NormalizeModelName 把 display_name 转成 OpenAI 风格客户端名：
// 小写、空格/下划线转连字符、保留点号（版本号）、去重连字符。
// "Qwen3.8-Max" → "qwen3.8-max"；"GLM-5.3" → "glm-5.3"。
func NormalizeModelName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	b.Grow(len(s))
	prevDash := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.':
			b.WriteRune(r)
			prevDash = false
		case r == ' ' || r == '_' || r == '-':
			if !prevDash && b.Len() > 0 {
				b.WriteByte('-')
				prevDash = true
			}
		default:
			b.WriteRune(r)
			prevDash = false
		}
	}
	return strings.Trim(b.String(), "-")
}
