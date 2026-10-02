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
// 不累计 errCount。本文件同时锁定对照组：普通 5xx 仍必须累计错误。
//
// 与 handler_bridge_down_test.go 的区别：那边是「桥进程没起」（transport error），
// 这边是「桥活着、桥的上游 DNS 挂了」（HTTP 502），两者都要逃过账号级长冷却。

import (
	"io"
	"net/http"
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

// 对照组：普通 5xx 仍按上游故障计错，否则真故障将永远不触发熔断。
func TestPlain5xxStillCountsAsError(t *testing.T) {
	fu := &upstreamDownFake{
		status: http.StatusInternalServerError,
		body:   `{"error":{"message":"internal server error"}}`,
		kind:   provider.ErrServer,
	}
	h, p := newEmptyStreamHandler(t, fu, "a1")

	postStreamChat(h, "qoder/m1")

	st, _ := p.Status("a1")
	if st.ErrCount != 1 {
		t.Fatalf("err_count = %d, want 1（普通 5xx 应计错）", st.ErrCount)
	}
}
