// sse.go 处理 QoderWork 嵌套 SSE：每行 data:{...,"body":"<json-string>"}，
// body 字段需二次解析得到标准 OpenAI chunk。
// 移植自 qoderwork2api internal/upstream/sse.go，含聚合与流式转写。
package qoder

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"zerogate/internal/provider"
)

// parseNestedSSE 逐行解析嵌套 SSE，每个有效 chunk 调 onChunk。
//   - body == "[DONE]" → 正常结束
//   - "event:finish" 行 → 忽略
//   - body 二次 unmarshal 失败 → 跳过该行（不致命）
func parseNestedSSE(r io.Reader, onChunk func(map[string]any) error) error {
	br := bufio.NewReaderSize(r, 256*1024)
	for {
		line, err := br.ReadString('\n')
		if err != nil && err != io.EOF {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimPrefix(line, "data:")
			var env struct {
				Body string `json:"body"`
			}
			if json.Unmarshal([]byte(payload), &env) == nil && env.Body != "" {
				if env.Body == "[DONE]" {
					return nil
				}
				var chunk map[string]any
				if json.Unmarshal([]byte(env.Body), &chunk) == nil {
					if err := onChunk(chunk); err != nil {
						return err
					}
				}
			} else if strings.TrimSpace(payload) != "[DONE]" {
				var direct map[string]any
				if json.Unmarshal([]byte(payload), &direct) == nil {
					if err := onChunk(direct); err != nil {
						return err
					}
				}
			}
		}
		if err == io.EOF {
			return nil
		}
	}
}

// aggregate 聚合完整 SSE 为单个 OpenAI chat.completion 响应。
// model 参数覆盖响应中的 model 字段（上游恒为 "auto"）。
// tool_calls 以流式 delta 到达（按 index 合并）。
func aggregate(r io.Reader, model string) (map[string]any, error) {
	var (
		id           string
		created      float64
		content      strings.Builder
		reasoning    strings.Builder
		role         = "assistant"
		finishReason = "stop"
		usage        map[string]any
		toolCalls    = map[int]map[string]any{}
		toolOrder    []int
	)
	err := parseNestedSSE(r, func(chunk map[string]any) error {
		if v, ok := chunk["id"].(string); ok && id == "" {
			id = v
		}
		if v, ok := chunk["created"].(float64); ok && created == 0 {
			created = v
		}
		if u, ok := chunk["usage"].(map[string]any); ok {
			usage = u
		}
		ch, _ := chunk["choices"].([]any)
		for _, ci := range ch {
			c, _ := ci.(map[string]any)
			if c == nil {
				continue
			}
			if fr, ok := c["finish_reason"].(string); ok && fr != "" {
				finishReason = fr
			}
			if delta, ok := c["delta"].(map[string]any); ok {
				if r2, ok := delta["role"].(string); ok && r2 != "" {
					role = r2
				}
				if txt, ok := delta["content"].(string); ok {
					content.WriteString(txt)
				}
				if rc, ok := delta["reasoning_content"].(string); ok {
					reasoning.WriteString(rc)
				}
				if tcs, ok := delta["tool_calls"].([]any); ok {
					for _, tc := range tcs {
						call, ok := tc.(map[string]any)
						if !ok {
							continue
						}
						idx := 0
						if v, ok := call["index"].(float64); ok {
							idx = int(v)
						}
						merged, seen := toolCalls[idx]
						if !seen {
							merged = map[string]any{"index": idx}
							toolCalls[idx] = merged
							toolOrder = append(toolOrder, idx)
						}
						mergeToolCallDelta(merged, call)
					}
				}
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano())
	}
	if created == 0 {
		created = float64(time.Now().Unix())
	}
	message := map[string]any{
		"role":    role,
		"content": content.String(),
	}
	if reasoning.Len() > 0 {
		message["reasoning_content"] = reasoning.String()
	}
	if len(toolOrder) > 0 {
		sortInts(toolOrder)
		calls := make([]map[string]any, 0, len(toolOrder))
		for _, idx := range toolOrder {
			calls = append(calls, toolCalls[idx])
		}
		message["tool_calls"] = calls
	}
	resp := map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": int64(created),
		"model":   model,
		"choices": []any{
			map[string]any{
				"index":         0,
				"message":       message,
				"finish_reason": finishReason,
			},
		},
	}
	if usage != nil {
		resp["usage"] = usage
	}
	return resp, nil
}

// mergeToolCallDelta 把流式 tool_call 片段合并到累计对象：
// id/type/function.name 直覆盖，function.arguments 拼接。
func mergeToolCallDelta(merged, delta map[string]any) {
	if v, ok := delta["id"].(string); ok && v != "" {
		merged["id"] = v
	}
	if v, ok := delta["type"].(string); ok && v != "" {
		merged["type"] = v
	}
	df, _ := delta["function"].(map[string]any)
	if df == nil {
		return
	}
	mf, _ := merged["function"].(map[string]any)
	if mf == nil {
		mf = map[string]any{}
		merged["function"] = mf
	}
	if v, ok := df["name"].(string); ok && v != "" {
		mf["name"] = v
	}
	if v, ok := df["arguments"].(string); ok && v != "" {
		if prev, _ := mf["arguments"].(string); prev != "" {
			mf["arguments"] = prev + v
		} else {
			mf["arguments"] = v
		}
	}
}

// sortInts 升序排序（避免引 sort 包只为三行）。
func sortInts(a []int) {
	for i := 0; i < len(a)-1; i++ {
		for j := i + 1; j < len(a); j++ {
			if a[j] < a[i] {
				a[i], a[j] = a[j], a[i]
			}
		}
	}
}

// streamAsOpenAI 把嵌套 SSE 流边读边转写为标准 OpenAI SSE 给客户端。
// 每个 chunk 重写 model 字段为客户端模型名；末尾补 data: [DONE]。
func streamAsOpenAI(w io.Writer, r io.Reader, model string, flush func()) error {
	sawDone := false
	var autoCallID string
	err := parseNestedSSE(r, func(chunk map[string]any) error {
		// 1. 防 403 崩溃：若 chunk 本身包含上游错误码（如 403 或 10605），绝不可把 code: 403 透传给客户端，
		// 否则 ZCode 等客户端 AI SDK 会识别为客户端自身的 API Key 失效（reason: auth_failed, retryable: false）导致 Turn 崩溃。
		if codeVal, hasCode := chunk["code"]; hasCode {
			codeStr := fmt.Sprintf("%v", codeVal)
			if codeStr == "403" || codeStr == "10605" || strings.Contains(codeStr, "403") {
				chunk["code"] = "429"
				chunk["retryable"] = true
			}
		}

		// 1b. 每日配额超限（qoder 110：Billing daily count exceeded）错误帧：
		// 绝不透传给客户端（retryable=false 会让 Turn 直接失败），返回可分类错误，
		// 由上层（handler）冷却该号并换号重试——账号级"当天额度用完"，次日 UTC+8
		// 恢复，不是封禁。
		if codeVal, hasCode := chunk["code"]; hasCode {
			if IsBillingCode(codeVal) {
				return &provider.Error{Kind: provider.ErrHardCredit, Msg: "qoder 每日配额超限（Billing daily count exceeded, code 110）"}
			}
		}
		if msg, ok := chunk["message"].(string); ok && isBillingError(msg) {
			return &provider.Error{Kind: provider.ErrHardCredit, Msg: "qoder 每日配额超限：" + truncate(msg, 120)}
		}

		// 2. 补齐 tool_call id：Qoder 流式输出工具调用时 id 常为空字符串，导致 ZCode AI SDK 无法关联工具调用
		if ch, ok := chunk["choices"].([]any); ok {
			for _, ci := range ch {
				if c, ok := ci.(map[string]any); ok {
					if delta, ok := c["delta"].(map[string]any); ok {
						if tcs, ok := delta["tool_calls"].([]any); ok {
							for _, tc := range tcs {
								if call, ok := tc.(map[string]any); ok {
									id, _ := call["id"].(string)
									if id == "" {
										if autoCallID == "" {
											autoCallID = fmt.Sprintf("call_qd_%d", time.Now().UnixNano())
										}
										call["id"] = autoCallID
									} else {
										autoCallID = id
									}
								}
							}
						}
					}
				}
			}
		}

		chunk["model"] = model
		raw, _ := json.Marshal(chunk)
		if _, err := fmt.Fprintf(w, "data: %s\n\n", raw); err != nil {
			return err
		}
		if flush != nil {
			flush()
		}
		return nil
	})
	if err != nil {
		return err
	}
	if !sawDone {
		if _, err := io.WriteString(w, "data: [DONE]\n\n"); err != nil {
			return err
		}
		if flush != nil {
			flush()
		}
	}
	return nil
}

// Stream 实现 provider.Upstream：嵌套 SSE → 标准 OpenAI SSE 透传。
func Stream(w http.ResponseWriter, r io.Reader, model string) error {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)
	flush := func() {
		if fl != nil {
			fl.Flush()
		}
	}
	return streamAsOpenAI(w, r, model, flush)
}
