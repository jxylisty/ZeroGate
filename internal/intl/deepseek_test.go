package intl

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"zerogate/internal/auth"
)

// requireNet 打真实上游/依赖本机网络的测试统一由此门控：
// 默认跳过（CI 无凭证无代理，上游契约变化也不应污染 go test 信号），
// 本地想跑时显式 WILDWORK_NET_TESTS=1 go test ./internal/intl
func requireNet(t *testing.T) {
	t.Helper()
	if os.Getenv("WILDWORK_NET_TESTS") != "1" {
		t.Skip("skipping network test (set WILDWORK_NET_TESTS=1 to run)")
	}
}

func TestDeepSeekV41Flash(t *testing.T) {
	requireNet(t)
	authFile := "../../auths/codebuddy-jxylisty.json"
	raw, err := os.ReadFile(authFile)
	if err != nil {
		t.Fatalf("Failed to read auth file: %v", err)
	}
	a, err := auth.Parse(raw)
	if err != nil {
		t.Fatalf("Failed to parse auth file: %v", err)
	}

	client := New()

	body := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []map[string]any{
			{"role": "user", "content": "Hello, please introduce yourself."},
		},
		"stream": false,
	}

	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost, client.Base+"/v2/chat/completions", bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	commonHeaders(req, a)
	chatTraceHeaders(req)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Model-ID", "deepseek-v4.1-flash")

	resp, err := client.HTTP.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	
	t.Logf("Status: %d", resp.StatusCode)
	t.Logf("Response body: %s", string(respBody))

	if resp.StatusCode >= 400 {
		t.Errorf("Unexpected status code: %d, body: %s", resp.StatusCode, string(respBody))
		return
	}

	var events []string
	lines := bytes.Split(respBody, []byte("\n"))
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		if bytes.HasPrefix(line, []byte("data: ")) {
			data := bytes.TrimPrefix(line, []byte("data: "))
			if bytes.Equal(data, []byte("[DONE]")) {
				break
			}
			var chunk map[string]any
			if err := json.Unmarshal(data, &chunk); err != nil {
				t.Logf("Failed to unmarshal chunk: %v", err)
				continue
			}
			events = append(events, string(data))
		}
	}

	t.Logf("Received %d SSE events", len(events))
	for i, e := range events {
		t.Logf("Event %d: %.200s...", i+1, e)
	}

	if len(events) == 0 {
		t.Error("No SSE events received")
	}
}

func TestDeepSeekDirectCurl(t *testing.T) {
	requireNet(t)

	authFile := "../../auths/codebuddy-jxylisty.json"
	raw, err := os.ReadFile(authFile)
	if err != nil {
		t.Skipf("Skipping: failed to read auth file: %v", err)
	}
	a, err := auth.Parse(raw)
	if err != nil {
		t.Skipf("Skipping: failed to parse auth file: %v", err)
	}

	url := "https://www.codebuddy.ai/v2/chat/completions"
	
	body := map[string]any{
		"model": "deepseek-v4.1-flash",
		"messages": []map[string]any{
			{"role": "system", "content": "You are a helpful assistant."},
			{"role": "user", "content": "What is the capital of France?"},
		},
		"stream": true,
	}

	bodyBytes, _ := json.Marshal(body)

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	req.Header.Set("User-Agent", "CodeBuddyIDE/3.10.0")
	req.Header.Set("X-Domain", a.Domain)
	req.Header.Set("X-Product", "SaaS")
	req.Header.Set("X-Product-Code", "codebuddy")
	req.Header.Set("X-IDE-Name", "CodeBuddyIDE")
	req.Header.Set("Authorization", "Bearer "+a.AccessToken)
	req.Header.Set("X-User-Id", a.UID)
	
	id := uuidHex()
	req.Header.Set("Trace-ID", id)
	req.Header.Set("X-Request-ID", id)
	req.Header.Set("X-Conversation-ID", id)
	req.Header.Set("X-Conversation-Request-ID", id)
	req.Header.Set("X-Conversation-Message-ID", id)
	req.Header.Set("X-Model-ID", "deepseek-v4.1-flash")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Failed to send request: %v", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 5<<20))
	
	t.Logf("=== Direct Curl Test ===")
	t.Logf("URL: %s", url)
	t.Logf("Status: %d", resp.StatusCode)
	t.Logf("Response body preview: %.500s", string(respBody))
	t.Logf("=======================")

	if resp.StatusCode >= 400 {
		t.Errorf("API call failed with status %d", resp.StatusCode)
		return
	}

	if !bytes.Contains(respBody, []byte("data:")) {
		t.Error("Response does not contain SSE data format")
	}
}

func TestMain(m *testing.M) {
	code := m.Run()
	os.Exit(code)
}
