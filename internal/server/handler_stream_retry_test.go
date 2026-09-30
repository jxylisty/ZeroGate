package server

import (
	"errors"
	"io"
	"testing"
)

// TestIsStreamEOFOnlyMatchesTransientErrors 确保零 token 自动重试只认"连接瞬断"类错误；
// 认证/风控类错误（401/403/405/10605/token 过期等）一律不得匹配，防止重试风暴加深风控标记
// （AutoClaw 封号教训）。
func TestIsStreamEOFOnlyMatchesTransientErrors(t *testing.T) {
	transient := []error{
		io.EOF,
		io.ErrUnexpectedEOF,
		errors.New("unexpected EOF"),
		errors.New("readfrom tcp 127.0.0.1:6623: connection reset by peer"),
		errors.New("write tcp: broken pipe"),
	}
	for _, err := range transient {
		if !isStreamEOF(err) {
			t.Errorf("transient error should match: %v", err)
		}
	}

	authRisk := []error{
		errors.New("401 Invalid token"),
		errors.New("upstream 403 Signature invalid"),
		errors.New("405 Not Allowed"),
		errors.New("code 10605: queue full"),
		errors.New("429 rate limit exceeded"),
		errors.New("access token expired"),
		errors.New("auth_failed: retryable=false"),
	}
	for _, err := range authRisk {
		if isStreamEOF(err) {
			t.Errorf("auth/risk-control error must NOT match: %v", err)
		}
	}
	if isStreamEOF(nil) {
		t.Error("nil should not match")
	}
}
