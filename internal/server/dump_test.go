package server

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// resetDump 重置诊断模块的单次初始化状态，供测试反复配置环境变量。
func resetDump() {
	dumpOnce = sync.Once{}
	dumpOn = false
	dumpDir = ""
}

// dump 诊断不得泄漏凭证：请求体中的 key/token 字段必须被掩码，
// 上游响应文本里的 sk- 串必须被打码。
func TestDumpRedactsSecrets(t *testing.T) {
	reqBody := []byte(`{
		"model": "workbuddy/glm-5.3-flash",
		"api_key": "sk-zg-abcdef123456",
		"authorization": "Bearer supersecret",
		"messages": [
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "hello"}
		]
	}`)

	out := redactJSON(reqBody)
	raw, _ := json.Marshal(out)
	s := string(raw)

	if strings.Contains(s, "sk-zg-abcdef123456") {
		t.Fatalf("api_key 未被脱敏: %s", s)
	}
	if strings.Contains(s, "supersecret") {
		t.Fatalf("authorization 未被打码: %s", s)
	}
	// 非敏感字段必须保留，否则诊断失去意义
	if !strings.Contains(s, "You are a helpful assistant") {
		t.Fatalf("业务内容被误删: %s", s)
	}
	if !strings.Contains(s, "workbuddy/glm-5.3-flash") {
		t.Fatalf("模型名被误删: %s", s)
	}

	// 上游响应里的凭证串同样要打码
	masked := maskSecrets(`{"error":"bad key sk-zg-9f8e7d6c5b4a","msg":"x"}`)
	if strings.Contains(masked, "sk-zg-9f8e7d6c5b4a") {
		t.Fatalf("响应中的 sk 串未打码: %s", masked)
	}
}

// 解析失败的请求体不应导致 panic，退化为截断文本。
func TestDumpHandlesNonJSON(t *testing.T) {
	if got := redactJSON([]byte("not a json {")); got == nil {
		t.Fatal("非 JSON 输入不应返回 nil")
	}
	if got := redactJSON(nil); got != "" {
		t.Fatalf("空输入应返回空串, got %v", got)
	}
}

// 关闭开关时不得产生任何文件。
func TestDumpDisabledByDefault(t *testing.T) {
	os.Unsetenv("ZG_DUMP_BODY")
	dir := t.TempDir()
	os.Setenv("ZG_DUMP_DIR", dir)
	defer os.Unsetenv("ZG_DUMP_DIR")

	// 重置单次初始化，模拟进程首次调用
	resetDump()

	dumpRequestBody("workbuddy", "m", 400, []byte("{}"), []byte("{}"))

	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("未开启开关却写入了 %d 个 dump 文件", len(entries))
	}
}

// 开启开关时写入文件，且文件名/内容可读。
func TestDumpWritesWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	os.Setenv("ZG_DUMP_BODY", "1")
	os.Setenv("ZG_DUMP_DIR", dir)
	defer func() {
		os.Unsetenv("ZG_DUMP_BODY")
		os.Unsetenv("ZG_DUMP_DIR")
	}()

	dumpOnce = sync.Once{}
	dumpInit()

	dumpRequestBody("workbuddy", "glm-5.3-flash", 400,
		[]byte(`{"code":11128}`),
		[]byte(`{"model":"glm-5.3-flash","messages":[]}`))

	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		t.Fatalf("应写入 1 个 dump 文件, got %d", len(entries))
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var rec map[string]any
	if err := json.Unmarshal(raw, &rec); err != nil {
		t.Fatalf("dump 文件不是合法 JSON: %v", err)
	}
	if rec["channel"] != "workbuddy" || rec["status"].(float64) != 400 {
		t.Fatalf("dump 元信息不符: %v", rec)
	}
}
