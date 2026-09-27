// body.go 构造 agent_chat_generation 请求体（纯透传模式）。
// 移植自 qoderwork2api internal/upstream/body.go：客户端消息全量转发，
// tools 仅在客户端显式传入时注入。
package qoder

import (
	"encoding/json"
	"strings"
	"time"
)

// IsReasoningModel 判断上游模型 key 是否开启思考模式（请求体 is_reasoning=true）。
// 对齐上游 icebears111/qoderwork2api（2026-09-19 起）：所有模型统一开启思考，
// 走 qwen3.8-flash 同一条链路（is_reasoning=true + source="system"）。
// 上游对不支持的模型会忽略该字段，实测不会因此报错。
func IsReasoningModel(modelKey string) bool {
	return modelKey != ""
}

// NormalizeReasoningEffort 校验思考档位。上游 thinking_config 支持 low/medium/xhigh；
// OpenAI 常见的 "high" 映射到上游最高档 "xhigh"。非法值返回空串 = 不注入 parameters，
// 走上游默认档（medium）。
func NormalizeReasoningEffort(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return "low"
	case "medium":
		return "medium"
	case "high", "xhigh":
		return "xhigh"
	default:
		return ""
	}
}

// ChatOptions 客户端可选的采样/思考参数，最终合并进上游顶层 parameters。
// 对齐 cli2api buildPlainChatBody：采样参数必须放在 parameters 内，
// 写在其他层级会被上游静默忽略。
// 指针类型用于区分"未传"与"传了零值"（temperature=0 是合法且常用的确定性设置）。
type ChatOptions struct {
	ReasoningEffort string   // low/medium/high/xhigh，空=上游默认
	Temperature     *float64 // 采样温度
	TopP            *float64 // 核采样
	MaxTokens       *int     // 输出上限
	Stop            any      // string 或 []string
}

// buildAgentBody 构造请求体。
//   - messages：客户端原始消息列表（可含 system/assistant/tool 多轮）
//   - modelKey：上游模型 key（如 dmodel）
//   - tools：客户端传来的 OpenAI tools 数组；为空则不注入 tools 字段
//   - opts：采样/思考参数
//
// 注意：developer 角色必须改写为 system；思考统一开启（见 IsReasoningModel）。
func buildAgentBody(messages []map[string]any, modelKey string, tools []any, opts ChatOptions) ([]byte, error) {
	// developer → system（浅拷贝消息避免污染调用方数据）
	msgs := make([]map[string]any, len(messages))
	for i, m := range messages {
		cp := make(map[string]any, len(m)+1)
		for k, v := range m {
			cp[k] = v
		}
		if r, _ := cp["role"].(string); r == "developer" {
			cp["role"] = "system"
		}
		msgs[i] = cp
	}

	// 最后一条 user 消息文本（chat_context.text 上游协议要求必填）
	prompt := ""
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i]["role"] == "user" {
			if c, ok := msgs[i]["content"].(string); ok && c != "" {
				prompt = c
				break
			}
		}
	}

	now := time.Now()
	newUUID := uuid4()

	// 思考模式：所有模型统一开启（对齐上游 2026-09-19）。
	// source="system" 是上游触发思考的真正开关——实测缺它 reasoning_content 永不下发。
	isReasoning := IsReasoningModel(modelKey)
	modelConfig := map[string]any{"key": modelKey, "is_reasoning": isReasoning}
	if isReasoning {
		modelConfig["source"] = "system"
	}

	base := map[string]any{
		"request_id":       newUUID,
		"chat_record_id":   newUUID,
		"request_set_id":   uuid4(),
		"session_id":       uuid4(),
		"stream":           true,
		"aliyun_user_type": "personal_professional_trial",
		"agent_id":         "agent_common",
		"chat_task":        "FREE_INPUT",
		"is_reply":         true,
		"image_urls":       nil,
		"session_type":     "qodercli",
		"model_config":     modelConfig,
		"chat_context": map[string]any{
			"chatPrompt": "",
			"text":       map[string]any{"type": "text", "text": prompt},
			"extra": map[string]any{
				"context": []any{},
				// 注意：extra.modelConfig 不带 source（与上游一致，仅顶层 model_config 带）
				"modelConfig":     map[string]any{"key": modelKey, "is_reasoning": isReasoning},
				"originalContent": map[string]any{"type": "text", "text": prompt},
			},
			"features":  []any{},
			"imageUrls": nil,
		},
		"messages": msgs,
		"business": map[string]any{
			"id":       uuid4(),
			"begin_at": now.UnixMilli(),
			"name":     truncateRunes(prompt, 30),
		},
	}

	if len(tools) > 0 {
		base["tools"] = tools
	}

	// 采样/思考参数：全部放进顶层 parameters 才生效（上游不读取 model_config
	// 里的 reasoning_effort，写错位置会被静默忽略）。
	params := map[string]any{}
	if isReasoning {
		params["enable_thinking"] = true
		if effort := NormalizeReasoningEffort(opts.ReasoningEffort); effort != "" {
			params["reasoning_effort"] = effort
		}
	}
	if opts.Temperature != nil {
		params["temperature"] = *opts.Temperature
	}
	if opts.TopP != nil {
		params["top_p"] = *opts.TopP
	}
	if opts.MaxTokens != nil {
		params["max_tokens"] = *opts.MaxTokens
	}
	if opts.Stop != nil {
		params["stop"] = opts.Stop
	}
	if len(params) > 0 {
		base["parameters"] = params
	}

	return json.Marshal(base)
}

// truncateRunes 截断到 n 个 rune。
func truncateRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
