package bridge

import (
	"net"
	"os"
	"strings"
	"testing"
)

// TestPidListeningOnPort 用本进程临时监听的端口验证原生 TCP 表查询：
// 返回的属主 PID 必须是自己（而非 netstat 子进程），且未监听端口返回 0。
func TestPidListeningOnPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	if pid := pidListeningOnPort(port); pid != os.Getpid() {
		t.Fatalf("pidListeningOnPort(%d) = %d, want %d", port, pid, os.Getpid())
	}
}

func TestPidListeningOnPortNotFound(t *testing.T) {
	// 取一个确定没人监听的端口（先占用拿到号再释放）
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	if pid := pidListeningOnPort(port); pid != 0 {
		t.Fatalf("pidListeningOnPort(%d) = %d, want 0", port, pid)
	}
}

func TestResolveWorkspacePath(t *testing.T) {
	// 验证对 tools/chatui 的相对定位
	p := resolveWorkspacePath("tools/chatui")
	if p == "" || !strings.Contains(p, "chatui") {
		t.Fatalf("resolveWorkspacePath(tools/chatui) failed, got %q", p)
	}
}

func TestResolvePython(t *testing.T) {
	py := resolvePython(false)
	if py == "" {
		t.Fatal("resolvePython(false) returned empty")
	}
	pyw := resolvePython(true)
	if pyw == "" {
		t.Fatal("resolvePython(true) returned empty")
	}
}
