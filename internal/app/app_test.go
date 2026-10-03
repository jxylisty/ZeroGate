package app

import (
	"fmt"
	"net"
	"testing"
)

// bridgePortUp 决定面板是否给 zcode 账号打「桥未启动」标注，
// 必须：监听中→true，没人听→false，非法/无端口→false（不误报）。
func TestBridgePortUp(t *testing.T) {
	// 监听中的端口
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("起监听失败: %v", err)
	}
	up := fmt.Sprintf("http://127.0.0.1:%d", ln.Addr().(*net.TCPAddr).Port)
	if !bridgePortUp(up) {
		t.Fatalf("监听中的端口应判定可连: %s", up)
	}
	_ = ln.Close()

	// 关闭后的同一端口（无进程监听）
	if bridgePortUp(up) {
		t.Fatalf("已关闭的端口应判定不可连: %s", up)
	}

	// 非法 / 缺端口：一律按不可连处理，避免误标
	for _, bad := range []string{"", "   ", "http://127.0.0.1", "not a url", "://x"} {
		if bridgePortUp(bad) {
			t.Fatalf("非法 bridgeUrl 应返回 false: %q", bad)
		}
	}
}
