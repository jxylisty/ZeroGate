package openaicompat

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"zerogate/internal/auth"
)

// newCaptureServer 起一个本地 SSE 服务，记录收到的 Authorization 头。
func newCaptureServer(t *testing.T, got *string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// TestChatStreamUsesPerAccountKey 校验按号传入的 a.AccessToken 优先于 client 固化的 key。
func TestChatStreamUsesPerAccountKey(t *testing.T) {
	var got string
	srv := newCaptureServer(t, &got)
	c := NewWithKey(srv.URL, "client-key", "/chat/completions")

	rc, status, _, err := c.ChatStream(&auth.Auth{AccessToken: "acct-token"}, []byte(`{"model":"m","messages":[]}`))
	if err != nil {
		t.Fatalf("ChatStream err: %v", err)
	}
	io.Copy(io.Discard, rc)
	rc.Close()
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if got != "Bearer acct-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer acct-token")
	}
}

// TestChatStreamFallsBackToClientKey 校验账号无 token 时回退 client 固化的 key（单账号渠道兼容）。
func TestChatStreamFallsBackToClientKey(t *testing.T) {
	var got string
	srv := newCaptureServer(t, &got)
	c := NewWithKey(srv.URL, "client-key", "/chat/completions")

	rc, _, _, err := c.ChatStream(&auth.Auth{}, []byte(`{"model":"m","messages":[]}`))
	if err != nil {
		t.Fatalf("ChatStream err: %v", err)
	}
	io.Copy(io.Discard, rc)
	rc.Close()
	if got != "Bearer client-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer client-key")
	}
}

// TestChatStreamNilAuthUsesClientKey 校验 a=nil 时不 panic 且回退 client key。
func TestChatStreamNilAuthUsesClientKey(t *testing.T) {
	var got string
	srv := newCaptureServer(t, &got)
	c := NewWithKey(srv.URL, "client-key", "/chat/completions")

	rc, _, _, err := c.ChatStream(nil, []byte(`{"model":"m","messages":[]}`))
	if err != nil {
		t.Fatalf("ChatStream err: %v", err)
	}
	io.Copy(io.Discard, rc)
	rc.Close()
	if got != "Bearer client-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer client-key")
	}
}

// TestFetchModelsUsesPerAccountKey 校验 FetchModels 同样按号取 key。
func TestFetchModelsUsesPerAccountKey(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":[{"id":"m1"}]}`))
	}))
	defer srv.Close()

	c := NewWithKey(srv.URL, "client-key", "/chat/completions")
	models, err := c.FetchModels(&auth.Auth{AccessToken: "acct-token"})
	if err != nil {
		t.Fatalf("FetchModels err: %v", err)
	}
	if len(models) != 1 || models[0].ID != "m1" {
		t.Fatalf("models = %+v", models)
	}
	if got != "Bearer acct-token" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer acct-token")
	}
}

// TestKeyForPrecedence 直接覆盖 keyFor 的取值优先级。
func TestKeyForPrecedence(t *testing.T) {
	c := NewWithKey("http://x", "client-key", "")
	if k := c.keyFor(&auth.Auth{AccessToken: "acct-token"}); k != "acct-token" {
		t.Errorf("keyFor(acct) = %q, want acct-token", k)
	}
	if k := c.keyFor(&auth.Auth{AccessToken: "   "}); k != "client-key" {
		t.Errorf("keyFor(blank) = %q, want client-key", k)
	}
	if k := c.keyFor(nil); k != "client-key" {
		t.Errorf("keyFor(nil) = %q, want client-key", k)
	}
}

// TestLocalBridgeIgnoresAccountToken 校验本地无鉴权桥（New 构造，PerAccountKey=false）
// 绝不把 auth 文件里的占位符 accessToken 当 Bearer 发出去。
func TestLocalBridgeIgnoresAccountToken(t *testing.T) {
	var got string
	srv := newCaptureServer(t, &got)
	c := New(srv.URL)

	rc, _, _, err := c.ChatStream(&auth.Auth{AccessToken: "copilot-local-api-key"}, []byte(`{"model":"auto","messages":[]}`))
	if err != nil {
		t.Fatalf("ChatStream err: %v", err)
	}
	io.Copy(io.Discard, rc)
	rc.Close()
	// Header.Set 会去掉尾部空格，实际发出的值就是 "Bearer"（旧行为一致）。
	if got != "Bearer" {
		t.Errorf("Authorization = %q, want %q (占位符不得外发)", got, "Bearer")
	}
}
