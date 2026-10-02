package zcode

import (
	"testing"

	"zerogate/internal/provider"
)

// 「上游域名/网络不可达」分类回归（2026-10-02 事故）。
//
// 现场：本机 DNS 抖动，各 zcode 桥实例同时返回
//
//	HTTP 502 {"error":{"type":"upstream_unreachable","message":"getaddrinfo ENOTFOUND zcode.z.ai"}}
//
// 旧逻辑按 status>=500 一律判 ErrServer → handler default 分支 NoteError，
// 每个账号连吃 3 次就进 10 分钟长冷却，整池被冻 + 渠道熔断。
func TestClassifyUpstreamUnreachable(t *testing.T) {
	cases := []struct {
		name string
		body string
		want provider.ErrKind
	}{
		{
			name: "实测 502 上游不可达",
			body: `{"error":{"type":"upstream_unreachable","message":"getaddrinfo ENOTFOUND zcode.z.ai"}}`,
			want: ErrUpstreamDown,
		},
		{
			name: "EAI_AGAIN 临时解析失败",
			body: `{"error":{"message":"getaddrinfo EAI_AGAIN zcode.z.ai"}}`,
			want: ErrUpstreamDown,
		},
		{
			name: "dial tcp 连接失败",
			body: `{"error":{"message":"dial tcp 1.2.3.4:443: connect: connection refused"}}`,
			want: ErrUpstreamDown,
		},
		{
			name: "普通 500 仍按上游故障计（对照组）",
			body: `{"error":{"message":"internal server error"}}`,
			want: ErrServer,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Classify(502, tc.body); got != tc.want {
				t.Fatalf("Classify(502) = %v, want %v", got, tc.want)
			}
		})
	}
}

// 普通 500（非网络类）不得被误判为 ErrUpstreamDown——否则真正的上游故障
// 将不再累计错误、也就永远不熔断。
func TestClassifyPlain500NotUpstreamDown(t *testing.T) {
	if got := Classify(500, `{"code":1305,"msg":"overloaded"}`); got == ErrUpstreamDown {
		t.Fatalf("普通 500 不应判为 ErrUpstreamDown")
	}
}
