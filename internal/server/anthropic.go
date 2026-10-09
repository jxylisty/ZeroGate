// anthropic.go 实现 Anthropic Messages API 兼容端点（POST /v1/messages）。
//
// 目标：Claude Code 等 Anthropic 协议客户端通过 ANTHROPIC_BASE_URL 指到本网关，
// 复用 ZeroGate 全部上游调度能力（选号/熔断/冷却/空流窥探/零token重试/审计），
// 只在协议边界做转换：Anthropic 请求 → OpenAI 请求体，上游 OpenAI 响应 → Anthropic
// 响应体（非流式 JSON 或流式 SSE 事件序列）。
//
// 设计：为 chatCompletions 注入 chatRenderer 渲染器。OpenAI 端点用 openAIRenderer
// （透传原格式），Anthropic 端点用 anthropicRenderer（转换格式）。转发核心只改
// 输出点，路由/重试/熔断逻辑零改动，避免复制加固逻辑产生两处漂移。
package server

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"zerogate/internal/provider"
)

// ---------------------------------------------------------------------------
// chatRenderer：一次 chat 请求的输出渲染器
// ---------------------------------------------------------------------------

// chatRenderer 定义一次 chat 请求的输出渲染能力。转发核心（serveChat）负责
// 选号/熔断/冷却/重试并拿到上游响应后，所有「写给客户端」的动作都经渲染器，
// 因此 OpenAI 与 Anthropic 端点共享同一套转发逻辑、仅替换渲染实现。
type chatRenderer interface {
	// writeError 写通用错误（参数/无可用账号等）。
	writeError(w http.ResponseWriter, status int, code, msg string)
	// writeUpstreamError 写上游 ≥400 错误（透传标准体或包装非标准体）。
	writeUpstreamError(w http.ResponseWriter, status int, raw []byte)
	// stream 把上游 SSE 流渲染给客户端。keepThinking 表示请求显式索取思考流
	// （仅 OpenAI 端点透传 think:true 时有意义；Anthropic 端点忽略）。
	stream(w http.ResponseWriter, rt *Runtime, rc io.Reader, keepThinking bool) error
	// writeCompletion 写非流式响应（resp 为聚合后的 OpenAI chat.completion 结构）。
	writeCompletion(w http.ResponseWriter, resp map[string]any)
	// streamError 流中途断时向客户端补发错误事件。
	streamError(w http.ResponseWriter, msg string)
}

// ---------------------------------------------------------------------------
// openAIRenderer：OpenAI 兼容端点渲染（即原有 chatCompletions 行为，原样保留）
// ---------------------------------------------------------------------------

type openAIRenderer struct{}

func (openAIRenderer) writeError(w http.ResponseWriter, status int, code, msg string) {
	writeOpenAIError(w, status, code, msg)
}

func (openAIRenderer) writeUpstreamError(w http.ResponseWriter, status int, raw []byte) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if isOpenAIError(raw) {
		_, _ = w.Write(raw)
	} else {
		_, _ = w.Write(wrapUpstreamError(status, raw))
	}
}

func (openAIRenderer) stream(w http.ResponseWriter, rt *Runtime, rc io.Reader, keepThinking bool) error {
	if tp, ok := rt.Upstream.(provider.ThinkPassthrough); ok && keepThinking {
		return tp.StreamWithThink(w, rc, true)
	}
	return rt.Upstream.Stream(w, rc)
}

func (openAIRenderer) writeCompletion(w http.ResponseWriter, resp map[string]any) {
	writeJSON(w, http.StatusOK, resp)
}

func (openAIRenderer) streamError(w http.ResponseWriter, msg string) {
	payload, jerr := json.Marshal(map[string]any{
		"error": map[string]any{
			"message": msg,
			"type":    "upstream_error",
			"code":    "stream_aborted",
		},
	})
	if jerr != nil {
		return
	}
	if _, werr := io.WriteString(w, "data: "+string(payload)+"\n\n"); werr == nil {
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}
}

// ---------------------------------------------------------------------------
// anthropicRenderer：Anthropic Messages 端点渲染
// ---------------------------------------------------------------------------

type anthropicRenderer struct{}

// anthropicErrorType 把内部 code 映射为 Anthropic 错误类型。
func anthropicErrorType(code string) string {
	switch code {
	case "invalid_request":
		return "invalid_request_error"
	case "no_healthy_account", "upstream_error", "stream_aborted":
		return "api_error"
	default:
		return "api_error"
	}
}

func (anthropicRenderer) writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    anthropicErrorType(code),
			"message": msg,
			"code":    code,
		},
	})
}

func (anthropicRenderer) writeUpstreamError(w http.ResponseWriter, status int, raw []byte) {
	// 上游标准 OpenAI 错误体 → 抽取 message 转为 Anthropic 错误；非标准体用原文。
	msg := string(raw)
	var obj map[string]any
	if json.Unmarshal(raw, &obj) == nil {
		if e, ok := obj["error"].(map[string]any); ok {
			if m, _ := e["message"].(string); m != "" {
				msg = m
			}
		}
	}
	writeJSON(w, status, map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "api_error",
			"message": msg,
			"code":    fmt.Sprintf("http_%d", status),
		},
	})
}

func (anthropicRenderer) stream(w http.ResponseWriter, _ *Runtime, rc io.Reader, _ bool) error {
	return writeAnthropicSSE(w, rc)
}

func (anthropicRenderer) writeCompletion(w http.ResponseWriter, resp map[string]any) {
	writeJSON(w, http.StatusOK, openAICompletionToAnthropic(resp))
}

func (anthropicRenderer) streamError(w http.ResponseWriter, msg string) {
	payload, jerr := json.Marshal(map[string]any{
		"type": "error",
		"error": map[string]any{
			"type":    "api_error",
			"message": msg,
			"code":    "stream_aborted",
		},
	})
	if jerr != nil {
		return
	}
	if _, werr := io.WriteString(w, "event: error\ndata: "+string(payload)+"\n\n"); werr == nil {
		if fl, ok := w.(http.Flusher); ok {
			fl.Flush()
		}
	}
}

// ---------------------------------------------------------------------------
// 请求转换：Anthropic Messages API → OpenAI chat.completions
// ---------------------------------------------------------------------------

// anthropicRequestToOpenAI 把 Anthropic 请求体转换为 OpenAI chat.completions 请求体。
// 转换边界：system/messages(含 image/tool_use/tool_result)、tools、max_tokens、
// stream、temperature/top_p。Anthropic 私有字段（thinking/metadata/cache_control
// 等）忽略——上游免费层模型多数不支持提示缓存，忽略不影响正确性。
func anthropicRequestToOpenAI(raw []byte) ([]byte, error) {
	var req struct {
		Model       string            `json:"model"`
		MaxTokens   int               `json:"max_tokens"`
		Stream      bool              `json:"stream"`
		System      json.RawMessage   `json:"system"`
		Messages    []json.RawMessage `json:"messages"`
		Tools       []json.RawMessage `json:"tools"`
		Temperature *float64          `json:"temperature"`
		TopP        *float64          `json:"top_p"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return nil, fmt.Errorf("parse anthropic request: %w", err)
	}
	if req.Model == "" {
		return nil, fmt.Errorf("anthropic request missing model")
	}
	// 兜底默认：Claude 协议要求 max_tokens，OpenAI 侧可选，缺省不强制。

	// system：字符串或 [{"type":"text","text":...}] 合并为单条 system 消息
	var msgs []map[string]any
	if len(req.System) > 0 && string(req.System) != "null" {
		sys := anthropicSystemText(req.System)
		if sys != "" {
			msgs = append(msgs, map[string]any{"role": "system", "content": sys})
		}
	}
	// messages：逐条转换
	for _, mraw := range req.Messages {
		converted, err := anthropicMessageToOpenAI(mraw)
		if err != nil {
			return nil, err
		}
		msgs = append(msgs, converted...)
	}
	if len(msgs) == 0 {
		return nil, fmt.Errorf("anthropic request has no messages")
	}

	out := map[string]any{
		"model":    req.Model,
		"messages": msgs,
		"stream":   req.Stream,
	}
	if req.MaxTokens > 0 {
		out["max_tokens"] = req.MaxTokens
	}
	if req.Temperature != nil {
		out["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		out["top_p"] = *req.TopP
	}
	// tools：Anthropic {name,description,input_schema} → OpenAI {function:{...}}
	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, traw := range req.Tools {
			var t struct {
				Name        string          `json:"name"`
				Description string          `json:"description"`
				InputSchema json.RawMessage `json:"input_schema"`
			}
			if err := json.Unmarshal(traw, &t); err != nil {
				continue
			}
			fn := map[string]any{"name": t.Name, "description": t.Description}
			if len(t.InputSchema) > 0 && string(t.InputSchema) != "null" {
				fn["parameters"] = json.RawMessage(t.InputSchema)
			}
			tools = append(tools, map[string]any{"type": "function", "function": fn})
		}
		if len(tools) > 0 {
			out["tools"] = tools
		}
	}
	return json.Marshal(out)
}

// anthropicSystemText 解析 system（字符串或 blocks 数组）为纯文本。
func anthropicSystemText(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text)
			}
		}
		return sb.String()
	}
	return ""
}

// anthropicMessageToOpenAI 转换一条 Anthropic message 为一条或多条 OpenAI message。
// 返回多条的情形：user 消息里的 tool_result 需拆成独立的 role=tool 消息。
func anthropicMessageToOpenAI(raw json.RawMessage) ([]map[string]any, error) {
	var m struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parse anthropic message: %w", err)
	}
	// content 为字符串（纯文本）
	var plain string
	if json.Unmarshal(m.Content, &plain) == nil {
		return []map[string]any{{"role": m.Role, "content": plain}}, nil
	}
	// content 为 blocks 数组
	var blocks []json.RawMessage
	if err := json.Unmarshal(m.Content, &blocks); err != nil {
		// 空 content 视为空文本
		return []map[string]any{{"role": m.Role, "content": ""}}, nil
	}
	switch m.Role {
	case "user":
		var parts []any
		var toolMsgs []map[string]any
		for _, braw := range blocks {
			var b struct {
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				Source    json.RawMessage `json:"source"`
				ToolUseID string          `json:"tool_use_id"`
				Content   json.RawMessage `json:"content"`
			}
			if err := json.Unmarshal(braw, &b); err != nil {
				continue
			}
			switch b.Type {
			case "text":
				if b.Text != "" {
					parts = append(parts, map[string]any{"type": "text", "text": b.Text})
				}
			case "image":
				// Anthropic {type:base64,media_type,data} → OpenAI image_url(data: URL)
				var src struct {
					Type      string `json:"type"`
					MediaType string `json:"media_type"`
					Data      string `json:"data"`
				}
				if json.Unmarshal(b.Source, &src) == nil && src.Type == "base64" && src.Data != "" {
					parts = append(parts, map[string]any{
						"type": "image_url",
						"image_url": map[string]any{
							"url": "data:" + src.MediaType + ";base64," + src.Data,
						},
					})
				}
			case "tool_result":
				content := string(b.Content)
				if json.Unmarshal(b.Content, &plain) == nil {
					content = plain
				}
				toolMsgs = append(toolMsgs, map[string]any{
					"role":         "tool",
					"tool_call_id": b.ToolUseID,
					"content":      content,
				})
			}
		}
		out := make([]map[string]any, 0, 1+len(toolMsgs))
		if len(parts) > 0 {
			out = append(out, map[string]any{"role": "user", "content": parts})
		}
		out = append(out, toolMsgs...)
		if len(out) == 0 {
			out = append(out, map[string]any{"role": "user", "content": ""})
		}
		return out, nil
	case "assistant":
		var textParts []string
		var toolCalls []map[string]any
		for _, braw := range blocks {
			var b struct {
				Type  string          `json:"type"`
				Text  string          `json:"text"`
				ID    string          `json:"id"`
				Name  string          `json:"name"`
				Input json.RawMessage `json:"input"`
			}
			if err := json.Unmarshal(braw, &b); err != nil {
				continue
			}
			switch b.Type {
			case "text":
				textParts = append(textParts, b.Text)
			case "tool_use":
				args := ""
				if len(b.Input) > 0 && string(b.Input) != "null" {
					if a, err := json.Marshal(b.Input); err == nil {
						args = string(a)
					}
				}
				toolCalls = append(toolCalls, map[string]any{
					"id":   b.ID,
					"type": "function",
					"function": map[string]any{
						"name":      b.Name,
						"arguments": args,
					},
				})
			}
		}
		msg := map[string]any{
			"role":    "assistant",
			"content": strings.Join(textParts, ""),
		}
		if len(toolCalls) > 0 {
			msg["tool_calls"] = toolCalls
		}
		return []map[string]any{msg}, nil
	default:
		return []map[string]any{{"role": m.Role, "content": string(m.Content)}}, nil
	}
}

// ---------------------------------------------------------------------------
// 非流式响应转换：OpenAI chat.completion → Anthropic message
// ---------------------------------------------------------------------------

// openAICompletionToAnthropic 把聚合后的 OpenAI 响应转换为 Anthropic message 结构。
func openAICompletionToAnthropic(resp map[string]any) map[string]any {
	id, _ := resp["id"].(string)
	model, _ := resp["model"].(string)

	var content []any
	stopReason := "end_turn"
	var usageIn, usageOut int64

	choices, _ := resp["choices"].([]any)
	if len(choices) > 0 {
		c, _ := choices[0].(map[string]any)
		msg, _ := c["message"].(map[string]any)
		if txt, _ := msg["content"].(string); txt != "" {
			content = append(content, map[string]any{"type": "text", "text": txt})
		}
		if tcs, _ := msg["tool_calls"].([]any); len(tcs) > 0 {
			for _, tc := range tcs {
				t, _ := tc.(map[string]any)
				fn, _ := t["function"].(map[string]any)
				name, _ := fn["name"].(string)
				args, _ := fn["arguments"].(string)
				var input any = map[string]any{}
				if args != "" {
					if err := json.Unmarshal([]byte(args), &input); err != nil {
						input = map[string]any{"raw": args}
					}
				}
				tid, _ := t["id"].(string)
				content = append(content, map[string]any{
					"type":  "tool_use",
					"id":    tid,
					"name":  name,
					"input": input,
				})
			}
		}
		if fr, _ := c["finish_reason"].(string); fr != "" {
			switch fr {
			case "tool_calls":
				stopReason = "tool_use"
			case "length":
				stopReason = "max_tokens"
			default:
				stopReason = "end_turn"
			}
		}
	}
	if u, ok := resp["usage"].(map[string]any); ok {
		usageIn, _ = asInt(u["prompt_tokens"])
		usageOut, _ = asInt(u["completion_tokens"])
		if usageIn == 0 && usageOut == 0 {
			if t, ok := asInt(u["total_tokens"]); ok {
				usageOut = t
			}
		}
	}

	return map[string]any{
		"id":            "msg_" + id,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       content,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  usageIn,
			"output_tokens": usageOut,
		},
	}
}

// ---------------------------------------------------------------------------
// 流式响应转换：OpenAI SSE → Anthropic SSE 事件序列
// ---------------------------------------------------------------------------

// writeAnthropicSSE 读取上游 OpenAI SSE 流，逐行转换为 Anthropic SSE 事件写出。
// 事件序列：message_start → content_block_start → content_block_delta* →
// content_block_stop → message_delta → message_stop（按 Anthropic 协议顺序）。
func writeAnthropicSSE(w http.ResponseWriter, rc io.Reader) error {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	fl, _ := w.(http.Flusher)

	s := &anthropicStream{w: w, fl: fl}
	br := bufio.NewReaderSize(rc, 64*1024)
	for {
		line, err := br.ReadString('\n')
		if strings.HasPrefix(line, "data: ") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data: "))
			if payload == "[DONE]" {
				s.finish()
				break
			}
			var chunk map[string]any
			if json.Unmarshal([]byte(payload), &chunk) == nil {
				s.handleChunk(chunk)
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			s.finish()
			return err
		}
	}
	s.finish()
	return nil
}

// anthropicStream 转换器状态机。
type anthropicStream struct {
	w          http.ResponseWriter
	fl         http.Flusher
	started    bool
	id         string
	model      string
	textOpen   bool
	textIdx    int // text block 的 Anthropic content index
	toolIdx    map[int]int // OpenAI tool_call index → Anthropic content index
	nextIdx    int // 下一个待分配的 content index
	stopReason string
	usage      map[string]any
	finished   bool
}

func (s *anthropicStream) send(event string, data any) {
	raw, _ := json.Marshal(data)
	_, _ = io.WriteString(s.w, "event: "+event+"\ndata: "+string(raw)+"\n\n")
	if s.fl != nil {
		s.fl.Flush()
	}
}

func (s *anthropicStream) ensureStart() {
	if s.started {
		return
	}
	s.started = true
	if s.id == "" {
		s.id = fmt.Sprintf("msg_%d", time.Now().UnixNano())
	}
	s.send("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id":            s.id,
			"type":          "message",
			"role":          "assistant",
			"model":         s.model,
			"content":       []any{},
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]any{
				"input_tokens":  0,
				"output_tokens": 0,
			},
		},
	})
}

func (s *anthropicStream) handleChunk(chunk map[string]any) {
	if v, ok := chunk["id"].(string); ok && v != "" {
		s.id = "msg_" + v
	}
	if v, ok := chunk["model"].(string); ok && v != "" {
		s.model = v
	}
	if u, ok := chunk["usage"].(map[string]any); ok {
		s.usage = u
	}
	choices, _ := chunk["choices"].([]any)
	if len(choices) == 0 {
		return
	}
	c, _ := choices[0].(map[string]any)
	if c == nil {
		return
	}
	if fr, ok := c["finish_reason"].(string); ok && fr != "" {
		switch fr {
		case "tool_calls":
			s.stopReason = "tool_use"
		case "length":
			s.stopReason = "max_tokens"
		default:
			s.stopReason = "end_turn"
		}
	}
	delta, _ := c["delta"].(map[string]any)
	if delta == nil {
		return
	}
	// 文本增量
	if txt, ok := delta["content"].(string); ok && txt != "" {
		s.ensureStart()
		if !s.textOpen {
			s.textIdx = s.nextIdx
			s.nextIdx++
			s.textOpen = true
			s.send("content_block_start", map[string]any{
				"type": "content_block_start",
				"index": s.textIdx,
				"content_block": map[string]any{
					"type": "text",
					"text": "",
				},
			})
		}
		s.send("content_block_delta", map[string]any{
			"type":  "content_block_delta",
			"index": s.textIdx,
			"delta": map[string]any{"type": "text_delta", "text": txt},
		})
	}
	// 工具调用增量
	if tcs, ok := delta["tool_calls"].([]any); ok && len(tcs) > 0 {
		for _, tc := range tcs {
			t, _ := tc.(map[string]any)
			if t == nil {
				continue
			}
			oidx := 0
			if v, ok := t["index"].(float64); ok {
				oidx = int(v)
			}
			if s.toolIdx == nil {
				s.toolIdx = map[int]int{}
			}
			if _, seen := s.toolIdx[oidx]; !seen {
				s.ensureStart()
				s.toolIdx[oidx] = s.nextIdx
				s.nextIdx++
				fn, _ := t["function"].(map[string]any)
				name, _ := fn["name"].(string)
				tid, _ := t["id"].(string)
				s.send("content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": s.toolIdx[oidx],
					"content_block": map[string]any{
						"type":  "tool_use",
						"id":    tid,
						"name":  name,
						"input": map[string]any{},
					},
				})
			}
			fn, _ := t["function"].(map[string]any)
			if fn == nil {
				continue
			}
			if args, ok := fn["arguments"].(string); ok && args != "" {
				s.send("content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": s.toolIdx[oidx],
					"delta": map[string]any{"type": "input_json_delta", "partial_json": args},
				})
			}
		}
	}
}

func (s *anthropicStream) finish() {
	if s.finished {
		return
	}
	s.finished = true
	s.ensureStart()
	// 关闭已打开的文本块
	if s.textOpen {
		s.send("content_block_stop", map[string]any{"type": "content_block_stop", "index": s.textIdx})
		s.textOpen = false
	}
	// 关闭工具块（按 content index 升序）
	if len(s.toolIdx) > 0 {
		idxList := make([]int, 0, len(s.toolIdx))
		for _, anIdx := range s.toolIdx {
			idxList = append(idxList, anIdx)
		}
		sort.Ints(idxList)
		for _, anIdx := range idxList {
			s.send("content_block_stop", map[string]any{"type": "content_block_stop", "index": anIdx})
		}
	}
	if s.stopReason == "" {
		s.stopReason = "end_turn"
	}
	var outTokens int64
	if s.usage != nil {
		outTokens, _ = asInt(s.usage["completion_tokens"])
	}
	s.send("message_delta", map[string]any{
		"type": "message_delta",
		"delta": map[string]any{
			"stop_reason":   s.stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]any{"output_tokens": outTokens},
	})
	s.send("message_stop", map[string]any{"type": "message_stop"})
}
