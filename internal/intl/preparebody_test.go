package intl

import (
	"encoding/json"
	"strings"
	"testing"
)

// gitStatus 段（Current branch / Git user / Recent commits）在发往 intl 上游时
// 触发 11128 "unapproved channel"，PrepareBody 必须从 system 提示词剥离它。
func TestPrepareBodyStripsGitStatus(t *testing.T) {
	body := []byte(`{
		"model": "glm-5.3-flash",
		"messages": [
			{"role":"system","content":"You are ZCode, an interactive coding agent...\n\ngitStatus: This is the git status at the start of the conversation.\n\nCurrent branch: main\n\nGit user: jxylisty\n\nRecent commits:\n831f14a fix(intel): something\n"},
			{"role":"user","content":"hello"}
		]
	}`)
	out := PrepareBody(body)

	var obj map[string]any
	if err := json.Unmarshal(out, &obj); err != nil {
		t.Fatalf("bad prepared: %v", err)
	}
	msgs := obj["messages"].([]any)
	sys := msgs[0].(map[string]any)["content"].(string)
	if strings.Contains(sys, "gitStatus") {
		t.Fatalf("gitStatus 未被剥离: %q", sys)
	}
	if strings.Contains(sys, "Git user") {
		t.Fatalf("Git user 未被剥离: %q", sys)
	}
	if !strings.Contains(sys, "You are ZCode") {
		t.Fatalf("system 正常内容被误删: %q", sys)
	}
	// 首条仍为 system
	if msgs[0].(map[string]any)["role"] != "system" {
		t.Fatalf("首条应为 system")
	}
	// stream 必须 true
	if obj["stream"] != true {
		t.Fatalf("stream 应为 true")
	}
}

// system 无 gitStatus 时不做任何改动（回归）。
func TestPrepareBodyKeepsSystemWhenNoGitStatus(t *testing.T) {
	body := []byte(`{
		"model": "m",
		"messages": [
			{"role":"system","content":"You are ZCode, an interactive coding agent\n\n# Environment\n- Platform: win32"},
			{"role":"user","content":"hi"}
		]
	}`)
	out := PrepareBody(body)
	var obj map[string]any
	_ = json.Unmarshal(out, &obj)
	msgs := obj["messages"].([]any)
	sys := msgs[0].(map[string]any)["content"].(string)
	if !strings.Contains(sys, "# Environment") {
		t.Fatalf("无 gitStatus 时 system 不应被改: %q", sys)
	}
	if strings.Contains(sys, "gitStatus") {
		t.Fatalf("不应出现 gitStatus")
	}
	if len(msgs) != 2 {
		t.Fatalf("消息数不应变, got %d", len(msgs))
	}
}