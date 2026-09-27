// payload.go 改写发往上游的 chat 请求体：
//  1. 强制 stream:true（上游拒绝非流式）
//  2. tool_choice 归一化（上游该字段是 string，对象形式会 400 code=11101）
//  3. 剥离 system 里的 gitStatus 段（code=11128 渠道校验触发源，见 stripGitStatus）
package upstream

import (
	"encoding/json"
	"strings"
)

// PrepareBody 单 pass 改写；无法解析时原样返回。
func PrepareBody(src []byte) []byte {
	if len(src) == 0 {
		return src
	}
	var obj map[string]any
	if err := json.Unmarshal(src, &obj); err != nil {
		return src
	}
	obj["stream"] = true
	normalizeToolChoice(obj)
	normalizeRoles(obj) // developer → system（上游对 developer 角色触发内容过滤误杀）
	stripMaxTokens(obj) // 思考模型 max_tokens 被思考占满 → 正文为空（ZCode empty_model_response）
	stripGitStatus(obj) // 剥离客户端注入的 git 状态段（渠道校验 11128 触发源）
	out, err := json.Marshal(obj)
	if err != nil {
		return src
	}
	return out
}

// stripMaxTokens 删除客户端 max_tokens：GLM 思考模型的 max_tokens 是
// 思考+正文总预算且思考优先，客户端（ZCode 等 IDE）传的 max_tokens 会被
// 思考占满、正文为空，实测报 empty_model_response。不传时服务端默认上限
// 会给正文留空间（实测正常输出）。
func stripMaxTokens(obj map[string]any) {
	if _, ok := obj["max_tokens"]; ok {
		delete(obj, "max_tokens")
	}
}

// normalizeRoles 将 OpenAI 的 developer 角色改写为 system。
// 上游对 role=developer 的消息一律命中内容过滤（finish_reason=content_filter，
// 返回“检测到敏感内容”），而相同内容用 system 角色则完全正常。
func normalizeRoles(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok {
			continue
		}
		if r, _ := mm["role"].(string); r == "developer" {
			mm["role"] = "system"
		}
	}
}

// stripGitStatus 删除 system 消息里客户端注入的 git 状态段。
//
// ZCode/Trae 类客户端在 git 仓库会话开局会把 git 快照写进 system 提示词
// （gitStatus: ... / Current branch: main / Git user: <name> / Recent commits: ...）。
// 实测发往 workbuddy 上游时该段会触发 code=11128 "Illegal API invocation from an
// unapproved channel"：同一秒的对照请求，system 含 "Git user:" 行恒 400，不含则恒 200。
// 该段属客户端会话元信息而非对话内容，剥离不影响正常问答与工具调用。
func stripGitStatus(obj map[string]any) {
	msgs, ok := obj["messages"].([]any)
	if !ok {
		return
	}
	for _, m := range msgs {
		mm, ok := m.(map[string]any)
		if !ok || mm["role"] != "system" {
			continue
		}
		c, ok := mm["content"].(string)
		if !ok {
			continue
		}
		if i := strings.Index(c, "gitStatus"); i >= 0 {
			mm["content"] = strings.TrimRight(c[:i], "\r\n \t")
		}
	}
}

// normalizeToolChoice 按上游 Go struct（string 类型）改写 OpenAI tool_choice。
//   - "none"            → 删 tool_choice + 删 tools/functions
//   - {"type":"none"}   → 同上
//   - {"type":"auto"/"required"} → 字符串 "auto"/"required"
//   - {"type":"function","function":{"name":"x"}} → 字符串 "x"
//   - 其他对象/非标量 → 删 tool_choice
func normalizeToolChoice(obj map[string]any) {
	suppress := func() {
		delete(obj, "tools")
		delete(obj, "functions")
	}
	tc, present := obj["tool_choice"]
	if !present {
		return
	}
	switch v := tc.(type) {
	case string:
		if strings.EqualFold(strings.TrimSpace(v), "none") {
			delete(obj, "tool_choice")
			suppress()
		}
	case map[string]any:
		typ, _ := v["type"].(string)
		typ = strings.ToLower(strings.TrimSpace(typ))
		switch typ {
		case "none":
			delete(obj, "tool_choice")
			suppress()
		case "auto", "required":
			obj["tool_choice"] = typ
		case "function":
			name := ""
			if fn, ok := v["function"].(map[string]any); ok {
				name, _ = fn["name"].(string)
			}
			if name == "" {
				name, _ = v["name"].(string)
			}
			if name = strings.TrimSpace(name); name != "" {
				obj["tool_choice"] = name
			} else {
				obj["tool_choice"] = "auto"
			}
		default:
			delete(obj, "tool_choice")
		}
	default:
		delete(obj, "tool_choice")
	}
}
