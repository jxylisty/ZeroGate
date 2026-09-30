package zcode

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"zerogate/internal/auth"
)

// 桥实例僵死（响应头已返回、之后 SSE 彻底静默）时必须由空闲超时断开，
// 不能无限等 —— 2026-10-01 00:32 zcode 请求挂满 7 分钟的回归测试。
func TestBridgeChatStreamIdleTimeout(t *testing.T) {
	old := streamIdleTimeout
	streamIdleTimeout = 150 * time.Millisecond
	defer func() { streamIdleTimeout = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		w.(http.Flusher).Flush()
		<-r.Context().Done() // 之后永久静默，模拟卡死实例
	}))
	defer srv.Close()

	c := &Client{BridgeURL: srv.URL, HTTP: srv.Client(), StreamHTTP: srv.Client()}
	rc, status, _, err := c.ChatStream(&auth.Auth{UID: "zcode-test"}, []byte(`{"model":"glm-5.3-flash"}`))
	if err != nil {
		t.Fatalf("首包应成功，实际: %v", err)
	}
	if status != http.StatusOK {
		t.Fatalf("status=%d want 200", status)
	}
	defer rc.Close()

	start := time.Now()
	buf := make([]byte, 256)
	var lastErr error
	for time.Now().Before(start.Add(3 * time.Second)) {
		if _, err := rc.Read(buf); err != nil {
			lastErr = err
			break
		}
	}
	if lastErr == nil {
		t.Fatal("静默后必须报错，否则客户端会一直等到超时（本次 7 分钟事故）")
	}
	if !strings.Contains(lastErr.Error(), "idle") {
		t.Fatalf("错误应指明空闲超时，实际: %v", lastErr)
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("应在空闲超时附近断开，实际等了 %v", el)
	}
}
