package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"unicode/utf8"
)

// TestBatConventions 守护 .bat 的文件约定：CRLF 行尾 + 非 UTF-8（GBK/ANSI）编码。
//
// 背景（详见 AGENTS.md 第 4 节第 12 条）：本机 ACP=936，cmd.exe 按控制台代码页
// 逐行解析批处理。若 .bat 被存成 UTF-8 或 LF 行尾，cmd 会把中文/行读错位，
// 产生看似随机的 "'xxx' 不是内部或外部命令"、"此时不应有 &"，往往在真正要用
// 的当口才炸。这里把约定变成 go test 的硬断言，违规即失败——让错误响亮。
func TestBatConventions(t *testing.T) {
	root, err := repoRoot()
	if err != nil {
		t.Fatal(err)
	}
	files := []string{
		filepath.Join(root, "restart-gate.bat"), // 真正脚本：restart.bat 只是单行 ASCII 委派
		filepath.Join(root, "restart.bat"),      // 委派行（无中文/无换行，被重存也字节不变）
		filepath.Join(root, "..", "启动.bat"),     // 根目录入口，不在 git 仓库内
		filepath.Join(root, "..", "启动全部服务.bat"), // 同上
	}
	for _, f := range files {
		f = filepath.Clean(f)
		raw, err := os.ReadFile(f)
		if os.IsNotExist(err) {
			continue // 根目录脚本缺失不算失败（不入库）
		}
		if err != nil {
			t.Fatalf("读取 %s 失败: %v", f, err)
		}
		if bytes.HasPrefix(raw, []byte{0xEF, 0xBB, 0xBF}) {
			t.Errorf("%s: 带 UTF-8 BOM，cmd 会把 BOM 当命令执行", f)
		}
		for i, b := range raw {
			if b == '\n' && (i == 0 || raw[i-1] != '\r') {
				t.Errorf("%s: 第 %d 字节是裸 LF —— .bat 必须 CRLF（cmd 会读行错位）", f, i)
				break
			}
		}
		hasNonASCII := false
		for _, b := range raw {
			if b >= 0x80 {
				hasNonASCII = true
				break
			}
		}
		if hasNonASCII && utf8.Valid(raw) {
			t.Errorf("%s: 含非 ASCII 且为合法 UTF-8 —— .bat 必须存成 GBK/ANSI（见 AGENTS.md 第 4 节第 12 条）", f)
		}
	}
}

// repoRoot 从当前包目录向上查找含 go.mod 的目录（即 src-true）。
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", os.ErrNotExist
		}
		dir = parent
	}
}
