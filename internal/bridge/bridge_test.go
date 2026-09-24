package bridge

import (
	"strings"
	"testing"
)

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
