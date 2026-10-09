package intl

import (
	"testing"

	"zerogate/internal/provider"
)

// 网关/代理链路层的 5xx 若 body 带网络特征，应归为 ErrUpstreamDown（短冷却），
// 而非 ErrServer（累计 errCount → 10 分钟长冷却）。
func TestClassify5xxNetworkBodyIsUpstreamDown(t *testing.T) {
	cases := []string{
		`{"error":{"type":"upstream_unreachable","message":"getaddrinfo ENOTFOUND www.codebuddy.ai"}}`,
		`<html><title>502 Bad Gateway</title>connection reset by peer</html>`,
		`upstream connect error or disconnect/reset before headers. reset reason: connection timeout`,
	}
	for _, body := range cases {
		if got := Classify(502, body); got != provider.ErrUpstreamDown {
			t.Fatalf("Classify(502, %q) = %v, want ErrUpstreamDown", body, got)
		}
	}
}

// 普通 5xx（真·上游服务器错误）仍必须是 ErrServer，累计账号错误。
func TestClassifyPlain5xxStillServer(t *testing.T) {
	if got := Classify(500, `{"code":10000,"msg":"internal error"}`); got != ErrServer {
		t.Fatalf("Classify(500) = %v, want ErrServer", got)
	}
}
