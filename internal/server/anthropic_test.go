package server

// Anthropic Messages API 兼容端点测试：请求转换 / 非流式响应转换 / 流式 SSE 转换。
// 验证 Claude Code 走 /v1/messages 时请求体被正确翻译为 OpenAI 兼容格式，
// 上游 OpenAI SSE 响应被翻译回 Anthropic 协议事件序列。

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"zerogate/internal/provider"
)

// ---------- 请求转换：Anthropic → OpenAI ----------

func TestAnthropicRequestToOpenAI(t *testing.T) {
	raw := `{
	  "model": "claude-3-5-sonnet",
	  "max_tokens": 512,
	  "system": "你是助手",
	  "messages": [
	    {"role":"user","content":"你好"},
	    {"role":"assistant","content":[{"type":"text","text":"好的"}]},
	    {"role":"user","content":[{"type":"text","text":"看这张图"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgo="}}]},
	    {"role":"assistant","content":[{"type":"text","text":"用工具"},{"type":"tool_use","id":"toolu_01","name":"get_weather","input":{"city":"北京"}}]},
	    {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_01","content":"晴，25度"}]}
	  ],
	  "tools": [{"name":"get_weather","description":"查天气","input_schema":{"type":"object","properties":{"city":{"type":"string"}}}}],
	  "stream": true
	}`
	oai, err := anthropicRequestToOpenAI([]byte(raw))
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	var obj map[string]any
	if err := json.Unmarshal(oai, &obj); err != nil {
		t.Fatalf("invalid JSON out: %v", err)
	}
	if obj["model"] != "claude-3-5-sonnet" {
		t.Fatalf("model = %v", obj["model"])
	}
	if obj["max_tokens"] != float64(512) {
		t.Fatalf("max_tokens = %v", obj["max_tokens"])
	}
	if obj["stream"] != true {
		t.Fatalf("stream = %v", obj["stream"])
	}
	msgs, _ := obj["messages"].([]any)
	if len(msgs) != 6 {
		t.Fatalf("messages = %d, want 6（system+user+assistant+user(image)+assistant(tool)+tool_result）", len(msgs))
	}
	// 第一条是 system
	if m0 := msgs[0].(map[string]any); m0["role"] != "system" {
		t.Fatalf("msg[0].role = %v, want system", m0["role"])
	}
	// 最后一条是 tool_result → role=tool
	last := msgs[len(msgs)-1].(map[string]any)
	if last["role"] != "tool" {
		t.Fatalf("msg[-1].role = %v, want tool", last["role"])
	}
	if last["tool_call_id"] != "toolu_01" {
		t.Fatalf("tool_call_id = %v", last["tool_call_id"])
	}
	// image block → image_url data: URL
	for _, mi := range msgs {
		m := mi.(map[string]any)
		if m["role"] != "user" {
			continue
		}
		if parts, ok := m["content"].([]any); ok {
			for _, p := range parts {
				pm, _ := p.(map[string]any)
				if pm["type"] == "image_url" {
					iu, _ := pm["image_url"].(map[string]any)
					url, _ := iu["url"].(string)
					if !strings.HasPrefix(url, "data:image/png;base64,") {
						t.Fatalf("image_url = %q", url)
					}
				}
			}
		}
	}
	// tools → function 格式
	tools, _ := obj["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("tools = %d", len(tools))
	}
	t0 := tools[0].(map[string]any)
	fn, _ := t0["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Fatalf("tool[0].function.name = %v", fn["name"])
	}
	if _, ok := fn["parameters"]; !ok {
		t.Fatal("tool parameters 丢失")
	}
}

func TestAnthropicRequestToOpenAIErrors(t *testing.T) {
	if _, err := anthropicRequestToOpenAI([]byte(`{}`)); err == nil {
		t.Fatal("空请求（无 model）应报错")
	}
	if _, err := anthropicRequestToOpenAI([]byte(`{`)); err == nil {
		t.Fatal("非法 JSON 应报错")
	}
}

// ---------- 非流式响应转换：OpenAI → Anthropic ----------

func TestOpenAICompletionToAnthropic(t *testing.T) {
	resp := map[string]any{
		"id":      "chatcmpl-abc",
		"model":   "zcode/glm-5.3",
		"choices": []any{
			map[string]any{
				"index": 0,
				"message": map[string]any{
					"role":    "assistant",
					"content": "你好",
					"tool_calls": []any{
						map[string]any{
							"id":   "call_1",
							"type": "function",
							"function": map[string]any{
								"name":      "get_weather",
								"arguments": `{"city":"北京"}`,
							},
						},
					},
				},
				"finish_reason": "tool_calls",
			},
		},
		"usage": map[string]any{"prompt_tokens": float64(10), "completion_tokens": float64(20)},
	}
	msg := openAICompletionToAnthropic(resp)
	if msg["type"] != "message" {
		t.Fatalf("type = %v", msg["type"])
	}
	if msg["role"] != "assistant" {
		t.Fatalf("role = %v", msg["role"])
	}
	if msg["stop_reason"] != "tool_use" {
		t.Fatalf("stop_reason = %v（finish_reason=tool_calls 应映射为 tool_use）", msg["stop_reason"])
	}
	content, _ := msg["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content blocks = %d, want 2（text + tool_use）", len(content))
	}
	var toolBlock map[string]any
	for _, c := range content {
		cm, _ := c.(map[string]any)
		if cm["type"] == "tool_use" {
			toolBlock = cm
		}
	}
	if toolBlock == nil {
		t.Fatal("缺少 tool_use block")
	}
	if toolBlock["name"] != "get_weather" || toolBlock["id"] != "call_1" {
		t.Fatalf("tool_use = %v", toolBlock)
	}
	input, _ := toolBlock["input"].(map[string]any)
	if input["city"] != "北京" {
		t.Fatalf("tool_use.input = %v", input)
	}
	usage, _ := msg["usage"].(map[string]any)
	if it, ok := usage["input_tokens"].(int64); !ok || it != 10 {
		t.Fatalf("usage.input_tokens = %v, want 10", usage["input_tokens"])
	}
	if ot, ok := usage["output_tokens"].(int64); !ok || ot != 20 {
		t.Fatalf("usage.output_tokens = %v, want 20", usage["output_tokens"])
	}
	if !strings.HasPrefix(msg["id"].(string), "msg_") {
		t.Fatalf("id = %v（应带 msg_ 前缀）", msg["id"])
	}
}

// anthropicHandler 构造带 model_redirects 的 handler：Claude Code 发 Anthropic 模型名
// （claude-3-5-sonnet），网关需经 redirects 映射到渠道模型（qoder/m1）——与真实
// config.json 中 claude-3-5-sonnet → traework/claude-3-5-sonnet 等配置同构。
func anthropicHandler(t *testing.T, fu provider.Upstream) *Handler {
	t.Helper()
	h, _ := newEmptyStreamHandler(t, fu, "a1")
	h.SetModelRedirects(map[string][]string{
		"claude-3-5-sonnet": {"qoder/m1"},
	})
	return h
}

// 流式转换端到端：/v1/messages 返回 Anthropic SSE ----------

func TestAnthropicMessagesStream(t *testing.T) {
	upstreamSSE := "" +
		"data: {\"id\":\"chatcmpl-x\",\"model\":\"zcode/glm-5.3\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"你好\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"，世界\"},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"\"},\"finish_reason\":\"stop\"}]}\n\n" +
		"data: {\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7}}\n\n" +
		"data: [DONE]\n\n"
	fu := &fakeUpstream{bodies: []string{upstreamSSE}}
	h := anthropicHandler(t, fu)

	reqBody := `{"model":"claude-3-5-sonnet","max_tokens":256,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	// 必须包含 Anthropic 事件序列关键字
	for _, want := range []string{
		"event: message_start",
		"\"type\":\"message_start\"",
		"event: content_block_start",
		"\"type\":\"text\"",
		"event: content_block_delta",
		"\"type\":\"text_delta\"",
		"\"text\":\"你好\"",
		"\"text\":\"，世界\"",
		"event: content_block_stop",
		"event: message_delta",
		"\"stop_reason\":\"end_turn\"",
		"event: message_stop",
		"\"output_tokens\":7",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE 缺少 %q:\n%s", want, body)
		}
	}
}

// 流式工具调用：OpenAI tool_calls delta → Anthropic tool_use content block。
func TestAnthropicMessagesStreamToolUse(t *testing.T) {
	upstreamSSE := "" +
		"data: {\"id\":\"chatcmpl-t\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_weather\",\"arguments\":\"\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"city\\\":\\\"北京\\\"}\"}}]},\"finish_reason\":null}]}\n\n" +
		"data: {\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"
	fu := &fakeUpstream{bodies: []string{upstreamSSE}}
	h := anthropicHandler(t, fu)

	reqBody := `{"model":"claude-3-5-sonnet","max_tokens":256,"stream":true,"messages":[{"role":"user","content":"天气?"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	body := rec.Body.String()
	for _, want := range []string{
		"\"type\":\"tool_use\"",
		"\"name\":\"get_weather\"",
		"\"id\":\"call_1\"",
		"input_json_delta",
		`"partial_json":"{\"city\":\"北京\"}"`,
		"\"stop_reason\":\"tool_use\"",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("SSE 缺少 %q:\n%s", want, body)
		}
	}
}

// 非流式 Anthropic 请求（stream:false）应返回 Anthropic message JSON 而非 OpenAI JSON。
func TestAnthropicMessagesNonStream(t *testing.T) {
	upstreamSSE := "data: {\"id\":\"chatcmpl-n\",\"model\":\"zcode/glm-5.3\",\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"回答\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":4}}\n\ndata: [DONE]\n\n"
	fu := &fakeUpstream{bodies: []string{upstreamSSE}}
	h := anthropicHandler(t, fu)

	reqBody := `{"model":"claude-3-5-sonnet","max_tokens":256,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var msg map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &msg); err != nil {
		t.Fatalf("响应不是 JSON: %v\n%s", err, rec.Body.String())
	}
	if msg["type"] != "message" {
		t.Fatalf("type = %v", msg["type"])
	}
	content, _ := msg["content"].([]any)
	if len(content) != 1 {
		t.Fatalf("content = %v", content)
	}
	c0 := content[0].(map[string]any)
	if c0["type"] != "text" || c0["text"] != "回答" {
		t.Fatalf("content[0] = %v", c0)
	}
	if msg["stop_reason"] != "end_turn" {
		t.Fatalf("stop_reason = %v", msg["stop_reason"])
	}
	// OpenAI 专有字段不应泄漏到 Anthropic 响应
	if _, ok := msg["choices"]; ok {
		t.Fatal("响应不应包含 OpenAI 的 choices 字段")
	}
}

// 鉴权兼容：Claude Code 用 x-api-key 头而非 Authorization Bearer。
func TestAnthropicAuthViaXAPIKey(t *testing.T) {
	fu := &fakeUpstream{bodies: []string{"data: {\"choices\":[{\"index\":0,\"message\":{\"role\":\"assistant\",\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"}}
	h := anthropicHandler(t, fu)
	h.SetAPIKey("WildWorkAPI")
	reqBody := `{"model":"claude-3-5-sonnet","max_tokens":16,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	req.Header.Set("x-api-key", "WildWorkAPI")
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code == http.StatusUnauthorized {
		t.Fatalf("x-api-key 鉴权被拒: %s", rec.Body.String())
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
}

// count_tokens 端点应返回 input_tokens 估算（Claude Code 启动时依赖）。
func TestAnthropicCountTokens(t *testing.T) {
	h := NewHandler(Config{APIKey: "WildWorkAPI"})
	reqBody := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"你好"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages/count_tokens", strings.NewReader(reqBody))
	req.Header.Set("Authorization", "Bearer WildWorkAPI")
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("非 JSON: %v", err)
	}
	if _, ok := out["input_tokens"]; !ok {
		t.Fatalf("缺少 input_tokens: %v", out)
	}
}

// writeAnthropicSSE 对上游中途断流应补 finish 事件而非裸断。
func TestAnthropicSSEAbortedStillFinishes(t *testing.T) {
	// 上游返回流中途 EOF（无 [DONE]），转换器必须在结尾补 message_stop
	upstreamSSE := "data: {\"id\":\"chatcmpl-a\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"
	fu := &fakeUpstream{bodies: []string{upstreamSSE}}
	h := anthropicHandler(t, fu)

	reqBody := `{"model":"claude-3-5-sonnet","max_tokens":64,"stream":true,"messages":[{"role":"user","content":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(reqBody))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)

	body := rec.Body.String()
	for _, want := range []string{"event: message_start", "event: content_block_stop", "event: message_stop"} {
		if !strings.Contains(body, want) {
			t.Fatalf("断流响应缺少 %q:\n%s", want, body)
		}
	}
}

func TestWriteAnthropicSSEHelpers(t *testing.T) {
	// 空输入也应产出 message_start/stop 骨架（无 panic）
	rec := httptest.NewRecorder()
	err := writeAnthropicSSE(rec, io.NopCloser(strings.NewReader("")))
	if err != nil {
		t.Fatalf("writeAnthropicSSE 空输入 err=%v", err)
	}
	if !strings.Contains(rec.Body.String(), "event: message_stop") {
		t.Fatalf("空输入缺 message_stop: %q", rec.Body.String())
	}
}
