package app

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIsAllowedOrigin(t *testing.T) {
	tests := []struct {
		origin string
		host   string
		want   bool
	}{
		// 本地回环与同源
		{"http://127.0.0.1:7863", "127.0.0.1:7863", true},
		{"http://localhost:7863", "127.0.0.1:7863", true},
		{"http://localhost:7870", "localhost:7863", true},
		// 私有局域网
		{"http://192.168.1.100:7863", "192.168.1.100:7863", true},
		{"http://192.168.1.50:7870", "192.168.1.100:7863", true},
		{"http://10.0.0.5:7863", "10.0.0.5:7863", true},
		// 自有域名与隧道
		{"https://chatui.my123.bond", "127.0.0.1:7863", true},
		{"https://sub.my123.bond:8080", "127.0.0.1:7863", true},
		// 外部恶意域名
		{"http://evil.com", "127.0.0.1:7863", false},
		{"https://attacker.org:8080", "127.0.0.1:7863", false},
		{"http://fake-my123.bond.evil.com", "127.0.0.1:7863", false},
	}

	for _, tt := range tests {
		got := isAllowedOrigin(tt.origin, tt.host)
		if got != tt.want {
			t.Errorf("isAllowedOrigin(%q, %q) = %v; want %v", tt.origin, tt.host, got, tt.want)
		}
	}
}

func TestCsrfGuardBlocksForeignOriginPost(t *testing.T) {
	app := &App{}
	okHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	guard := app.csrfGuard(okHandler)

	// 1. 外部恶意域名 POST -> 403 Forbidden
	req1 := httptest.NewRequest(http.MethodPost, "/api/account/remove", nil)
	req1.Header.Set("Origin", "http://malicious-site.com")
	w1 := httptest.NewRecorder()
	guard.ServeHTTP(w1, req1)
	if w1.Code != http.StatusForbidden {
		t.Fatalf("expected 403 for malicious origin, got %d", w1.Code)
	}

	// 2. 本地同源 POST -> 200 OK
	req2 := httptest.NewRequest(http.MethodPost, "/api/account/remove", nil)
	req2.Header.Set("Origin", "http://127.0.0.1:7863")
	w2 := httptest.NewRecorder()
	guard.ServeHTTP(w2, req2)
	if w2.Code != http.StatusOK {
		t.Fatalf("expected 200 for local origin, got %d", w2.Code)
	}

	// 3. 无 Origin 头（非浏览器/手机/脚本）POST -> 200 OK
	req3 := httptest.NewRequest(http.MethodPost, "/api/account/remove", nil)
	w3 := httptest.NewRecorder()
	guard.ServeHTTP(w3, req3)
	if w3.Code != http.StatusOK {
		t.Fatalf("expected 200 for empty origin, got %d", w3.Code)
	}

	// 4. GET 请求带外部 Origin 依然放行（只读） -> 200 OK
	req4 := httptest.NewRequest(http.MethodGet, "/api/state", nil)
	req4.Header.Set("Origin", "http://some-site.com")
	w4 := httptest.NewRecorder()
	guard.ServeHTTP(w4, req4)
	if w4.Code != http.StatusOK {
		t.Fatalf("expected 200 for GET request, got %d", w4.Code)
	}
}
