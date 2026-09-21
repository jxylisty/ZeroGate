package autoclaw

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"wild-work/internal/auth"
)

// lesson 06 回归（2026-09-21 契约反转）：
//   - X-Harness-Type: zcode 带上即 400，严禁回潮；
//   - 请求体第一条 system 消息必须含 AutoClawSystemBanner（幂等注入）；
//   - 双标识：X-Request-Model 发路由 ID，body.model 发剥前缀模型；
//   - 403 810000（免费桶日额度墙）必须短路秒回，不得进入忙窗重试循环。

func newTestClient(srv *httptest.Server) *Client {
	return &Client{
		HTTP:       srv.Client(),
		StreamHTTP: srv.Client(),
		Base:       srv.URL,
		Channel:    "autoclaw",
	}
}

func testAuth() *auth.Auth {
	return &auth.Auth{UID: "u1", AccessToken: "tok-abc"}
}

func okSSE(t *testing.T, w http.ResponseWriter, r *http.Request, out *http.Header, body *map[string]any) {
	t.Helper()
	*out = r.Header.Clone()
	raw, _ := io.ReadAll(r.Body)
	if err := json.Unmarshal(raw, body); err != nil {
		t.Errorf("上游收到非 JSON body: %v", err)
	}
	w.Header().Set("Content-Type", "text/event-stream")
	io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"pong\"}}]}\n\n")
	io.WriteString(w, "data: [DONE]\n\n")
}

func TestChatStreamBannerAndContract(t *testing.T) {
	var gotHeader http.Header
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		okSSE(t, w, r, &gotHeader, &gotBody)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	rc, status, respBody, err := c.ChatStream(testAuth(),
		[]byte(`{"model":"zai_auto","messages":[{"role":"user","content":"hi"}]}`))
	if err != nil {
		t.Fatalf("ChatStream err: %v", err)
	}
	defer rc.Close()
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, respBody)
	}

	// banner 作为独立第一条 system 消息前置，客户端消息原样跟在后面
	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("messages 长度 = %d, want 2（banner + 原消息）: %v", len(msgs), gotBody["messages"])
	}
	first, _ := msgs[0].(map[string]any)
	if !strings.EqualFold(fmt.Sprint(first["role"]), "system") ||
		!strings.Contains(fmt.Sprint(first["content"]), AutoClawSystemBanner) {
		t.Fatalf("第一条消息不是 banner system: %v", first)
	}

	// X-Harness-Type 反转成"带上即 400"，严禁再加回
	if gotHeader.Get("X-Harness-Type") != "" {
		t.Fatalf("X-Harness-Type 不应出现（lesson 06：带上即 400）")
	}

	// 双标识：X-Request-Model=路由 ID，body.model=剥前缀模型
	if got, want := gotHeader.Get("X-Request-Model"), "zai_auto"; got != want {
		t.Fatalf("X-Request-Model = %q, want %q", got, want)
	}
	if got, want := fmt.Sprint(gotBody["model"]), "auto"; got != want {
		t.Fatalf("body.model = %q, want %q", got, want)
	}
	if gotBody["stream"] != true {
		t.Fatalf("body.stream 应被强制为 true，got %v", gotBody["stream"])
	}
}

func TestChatStreamBannerNotDuplicated(t *testing.T) {
	var gotBody map[string]any
	var out http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		okSSE(t, w, r, &out, &gotBody)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	reqObj := map[string]any{
		"model": "zai_auto",
		"messages": []any{
			map[string]any{"role": "system", "content": AutoClawSystemBanner + " tail"},
			map[string]any{"role": "user", "content": "hi"},
		},
	}
	reqBody, _ := json.Marshal(reqObj)
	rc, _, _, err := c.ChatStream(testAuth(), reqBody)
	if err != nil {
		t.Fatalf("ChatStream err: %v", err)
	}
	defer rc.Close()

	msgs, _ := gotBody["messages"].([]any)
	if len(msgs) != 2 {
		t.Fatalf("已有 banner 时不应重复注入，messages 长度 = %d: %v", len(msgs), gotBody["messages"])
	}
}

func TestChatStream810000ShortCircuit(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"code":810000,"message":"Auto-Fast 模型免费额度已用完，24 小时后会进行重置"}`)
	}))
	defer srv.Close()

	c := newTestClient(srv)
	start := time.Now()
	_, status, respBody, err := c.ChatStream(testAuth(),
		[]byte(`{"model":"zai_auto-fast","messages":[{"role":"user","content":"hi"}]}`))
	elapsed := time.Since(start)

	if n := atomic.LoadInt32(&hits); n != 1 {
		t.Fatalf("上游被请求 %d 次, want 1（810000 日额度墙必须短路，不得重试）", n)
	}
	if status != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502 软错", status)
	}
	if !strings.Contains(string(respBody), "额度") {
		t.Fatalf("应返回额度提示文案, got: %s", respBody)
	}
	if elapsed > 3*time.Second {
		t.Fatalf("810000 短路耗时 %v，疑似落入忙窗重试循环（客户端会挂死）", elapsed)
	}
	if err != nil {
		t.Fatalf("短路路径 err 应为 nil（软错文案走 respBody）: %v", err)
	}
}
