package zcode

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Done() 成功后必须保留编排状态，供随后的 Finalize() 落库。
// 回归：曾经 Done 里 p.cur = nil，导致 Finalize 恒失败「无进行中的 zcode 登录」，
// 面板自助加号永远加不进去账号（日志停在「zcode 登录落库失败」）。
func TestProvisionDoneThenFinalize(t *testing.T) {
	dir := t.TempDir()
	creds := filepath.Join(dir, "creds")
	if err := os.MkdirAll(creds, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(creds, "credentials.json"), []byte(`{"encrypted":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	ch := make(chan struct{})
	close(ch) // 登录进程已退出
	p := &Provision{authDir: dir, instancesDir: dir, basePort: 18086}
	p.cur = &ProvisionState{Name: "acc-logn9999", Port: 18099, Provider: "zai", credsDir: creds, doneCh: ch}

	ok, err := p.Done(time.Second)
	if !ok || err != nil {
		t.Fatalf("Done = (%v, %v)，期望 (true, nil)", ok, err)
	}

	fp, err := p.Finalize()
	if err != nil {
		t.Fatalf("Done 之后 Finalize 失败: %v", err)
	}
	if got := filepath.Base(fp); got != "zcode-logn9999.json" {
		t.Fatalf("auth 文件名 = %s，期望 zcode-logn9999.json（去掉 acc- 前缀）", got)
	}
	if p.Busy() {
		t.Fatal("Finalize 之后编排状态仍为 busy，会卡住后续加号")
	}
	raw, err := os.ReadFile(fp)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"uid": "zcode-logn9999"`) {
		t.Fatalf("uid 不符预期：%s", raw)
	}
	if !strings.Contains(string(raw), `"bridgeUrl": "http://127.0.0.1:18099"`) {
		t.Fatalf("bridgeUrl 不符预期：%s", raw)
	}
}

// Done 失败（登录进程未产出凭证）后必须释放编排状态，避免 Busy 卡死。
func TestProvisionDoneFailureReleasesState(t *testing.T) {
	dir := t.TempDir()
	ch := make(chan struct{})
	close(ch)
	p := &Provision{authDir: dir, instancesDir: dir, basePort: 18086}
	p.cur = &ProvisionState{Name: "acc-logn8888", Port: 18098, credsDir: filepath.Join(dir, "creds"), doneCh: ch}

	if ok, err := p.Done(time.Second); ok || err == nil {
		t.Fatalf("Done = (%v, %v)，期望失败", ok, err)
	}
	if p.Busy() {
		t.Fatal("Done 失败后编排状态未释放")
	}
}
