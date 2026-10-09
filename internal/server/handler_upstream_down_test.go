package server

// 「上游域名/网络不可达 ≠ 账号故障」回归测试（2026-10-02 zcode 事故）。
//
// 事故现场：19:14-19:15 本机 DNS 抖动，各 zcode 桥实例同时返回
//
//	HTTP 502 {"error":{"type":"upstream_unreachable","message":"getaddrinfo ENOTFOUND zcode.z.ai"}}
//
// handler 的 status>=400 分支把它当普通 5xx 走 default → NoteError，5 个账号各吃
// 3 次就进 10 分钟长冷却（余额 73M~98M 均健康），渠道熔断 (0/6 healthy) 反复 trip，
// 30 秒的网络抖动被放大成 10 分钟整渠道中断。
//
// 修法：zcode.Classify 对 status>=500 且 body 含"上游不可达"特征串的响应返回
// provider.ErrUpstreamDown，handler 据此只给短冷却（同 ErrBridgeDown 规格），
// 不累计 errCount。2026-10-09 整改后对照组语义升级：普通 5xx / 非网络传输
// 错误同样只短冷却不累计 —— 只有风控/封号级错误才允许长停账号。
//
// 与 handler_bridge_down_test.go 的区别：那边是「桥进程没起」（transport error），
// 这边是「桥活着、桥的上游 DNS 挂了」（HTTP 502），两者都要逃过账号级长冷却。

import (
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
)

// upstreamDownFake 返回固定 status/body，并按 kind 分类（其余方法复用 fakeUpstream）。
type upstreamDownFake struct {
	fakeUpstream
	status int
	body   string
	kind   provider.ErrKind
}

func (f *upstreamDownFake) ChatStream(a *auth.Auth, body []byte) (io.ReadCloser, int, []byte, error) {
	// 覆盖了 fakeUpstream.ChatStream，需自行补计次以复用其 chatCount()。
	f.mu.Lock()
	f.chats++
	f.mu.Unlock()
	return io.NopCloser(strings.NewReader(f.body)), f.status, []byte(f.body), nil
}

func (f *upstreamDownFake) Classify(status int, body string) provider.ErrKind { return f.kind }

// 502 上游不可达：两个账号都只吃短冷却，errCount 必须保持 0。
func TestUpstreamDownDoesNotAccumulateErrors(t *testing.T) {
	fu := &upstreamDownFake{
		status: http.StatusBadGateway,
		body:   `{"error":{"type":"upstream_unreachable","message":"getaddrinfo ENOTFOUND zcode.z.ai"}}`,
		kind:   provider.ErrUpstreamDown,
	}
	h, p := newEmptyStreamHandler(t, fu, "a1", "a2")

	postStreamChat(h, "qoder/m1")

	if got := fu.chatCount(); got != 2 {
		t.Fatalf("chat calls = %d, want 2（应轮换试完两个账号）", got)
	}
	for _, uid := range []string{"a1", "a2"} {
		st, ok := p.Status(uid)
		if !ok {
			t.Fatalf("%s 不在池中", uid)
		}
		if st.ErrCount != 0 {
			t.Fatalf("%s err_count = %d, want 0（网络故障不应计账号错误）", uid, st.ErrCount)
		}
		if st.Disabled {
			t.Fatalf("%s 不应被禁用", uid)
		}
		if !st.Cooling {
			t.Fatalf("%s 应处于短冷却中（整池不可用时熔断仍能快速失败）", uid)
		}
	}
}

// 对照组（2026-10-09 整改后）：普通 5xx（ErrServer）不再累计 errCount，改为只给
// 短冷却 —— 账号仍被暂时摘除让整池不可用时熔断可快速失败，但网络/上游侧故障
// 绝不会被放大成 3 次累计 → 10 分钟长停。只有风控/封号级错误才允许长停账号。
func TestPlain5xxGetsShortCooldownWithoutAccumulating(t *testing.T) {
	fu := &upstreamDownFake{
		status: http.StatusInternalServerError,
		body:   `{"error":{"message":"internal server error"}}`,
		kind:   provider.ErrServer,
	}
	h, p := newEmptyStreamHandler(t, fu, "a1")

	postStreamChat(h, "qoder/m1")

	st, _ := p.Status("a1")
	if st.ErrCount != 0 {
		t.Fatalf("err_count = %d, want 0（普通 5xx 不应累计账号错误）", st.ErrCount)
	}
	if !st.Cooling {
		t.Fatalf("应处于短冷却中")
	}
}

// netErrFake 在 ChatStream 直接抛传输层错误（模拟云渠道直连时本机 DNS 抖动）。
type netErrFake struct {
	fakeUpstream
	err error
}

func (f *netErrFake) ChatStream(a *auth.Auth, body []byte) (io.ReadCloser, int, []byte, error) {
	f.mu.Lock()
	f.chats++
	f.mu.Unlock()
	return nil, 0, nil, f.err
}

// 传输层 DNS 抖动（2026-10-05 workbuddy 事故现场）：
//
//	Post "https://copilot.tencent.com/v2/chat/completions": dial tcp: lookup ...: no such host
//
// 走的是 handler 的 terr 分支，两个账号都只吃短冷却，errCount 必须保持 0。
func TestTransportDNSErrorDoesNotAccumulateErrors(t *testing.T) {
	dnsErr := &url.Error{
		Op:  "Post",
		URL: "https://copilot.tencent.com/v2/chat/completions",
		Err: &net.DNSError{Err: "no such host", Name: "copilot.tencent.com", IsNotFound: true},
	}
	fu := &netErrFake{err: dnsErr}
	h, p := newEmptyStreamHandler(t, fu, "a1", "a2")

	postStreamChat(h, "qoder/m1")

	if got := fu.chatCount(); got != 2 {
		t.Fatalf("chat calls = %d, want 2（应轮换试完两个账号）", got)
	}
	for _, uid := range []string{"a1", "a2"} {
		st, ok := p.Status(uid)
		if !ok {
			t.Fatalf("%s 不在池中", uid)
		}
		if st.ErrCount != 0 {
			t.Fatalf("%s err_count = %d, want 0（DNS 抖动不应计账号错误）", uid, st.ErrCount)
		}
		if st.Disabled {
			t.Fatalf("%s 不应被禁用", uid)
		}
		if !st.Cooling {
			t.Fatalf("%s 应处于短冷却中（整池不可用时熔断仍能快速失败）", uid)
		}
	}
}

// 对照组（2026-10-09 整改后）：非网络类的传输错误（如 TLS 握手失败）同样不累计
// errCount、只给短冷却 —— 账号级问题只会以 HTTP 响应形态出现，不可能在拿到响应
// 前触发，传输层错误一律错在链路而非账号。
func TestPlainTransportErrorGetsShortCooldown(t *testing.T) {
	fu := &netErrFake{err: errors.New("tls: failed to verify certificate")}
	h, p := newEmptyStreamHandler(t, fu, "a1")

	postStreamChat(h, "qoder/m1")

	st, _ := p.Status("a1")
	if st.ErrCount != 0 {
		t.Fatalf("err_count = %d, want 0（传输层错误不应累计账号错误）", st.ErrCount)
	}
	if !st.Cooling {
		t.Fatalf("应处于短冷却中")
	}
}

// 传输层读写阶段网络错误（2026-10-09 codebuddy 事故现场）：
//
//	read tcp 172.19.51.242:7194->43.160.158.125:443: wsarecv: A connection attempt
//	failed because the connected party did not properly respond after a period of time
//
// TCP 已建连、读响应时对端（经代理链路）不回数据。10-05 的修复只覆盖拨号层特征，
// 这类错误漏网 → 3 次攒满 errThreshold → 10 分钟长冷却 → 熔断。现在必须只给短冷却。
func TestTransportRecvTimeoutDoesNotAccumulateErrors(t *testing.T) {
	recvErr := &url.Error{
		Op:  "Post",
		URL: "https://www.codebuddy.ai/v2/chat/completions",
		Err: errors.New(`read tcp 172.19.51.242:7194->43.160.158.125:443: wsarecv: A connection attempt failed because the connected party did not properly respond after a period of time, or established connection failed because connected host has failed to respond.`),
	}
	fu := &netErrFake{err: recvErr}
	h, p := newEmptyStreamHandler(t, fu, "a1", "a2")

	postStreamChat(h, "qoder/m1")

	if got := fu.chatCount(); got != 2 {
		t.Fatalf("chat calls = %d, want 2（应轮换试完两个账号）", got)
	}
	for _, uid := range []string{"a1", "a2"} {
		st, _ := p.Status(uid)
		if st.ErrCount != 0 {
			t.Fatalf("%s err_count = %d, want 0（wsarecv 读超时不应计账号错误）", uid, st.ErrCount)
		}
		if !st.Cooling {
			t.Fatalf("%s 应处于短冷却中", uid)
		}
	}
}

// 断连族（连接被对端重置）同属读写层网络故障，同样不得计账号错误。
func TestTransportConnResetDoesNotAccumulateErrors(t *testing.T) {
	resetErr := &url.Error{
		Op:  "Post",
		URL: "https://www.codebuddy.ai/v2/chat/completions",
		Err: errors.New(`write tcp 172.19.51.242:12219->43.160.158.125:443: wsasend: An existing connection was forcibly closed by the remote host.`),
	}
	fu := &netErrFake{err: resetErr}
	h, p := newEmptyStreamHandler(t, fu, "a1")

	postStreamChat(h, "qoder/m1")

	st, _ := p.Status("a1")
	if st.ErrCount != 0 {
		t.Fatalf("err_count = %d, want 0（连接被重置不应计账号错误）", st.ErrCount)
	}
}
