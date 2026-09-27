package auth

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseNested(t *testing.T) {
	raw := []byte(`{"auth":{"accessToken":"at","refreshToken":"rt","expiresAt":1753600000,"domain":""},"account":{"uid":"u1","enterpriseId":"e1","nickname":"n1"}}`)
	sa, err := Parse(raw)
	if err != nil {
		t.Fatalf("nested parse err: %v", err)
	}
	if sa.AccessToken != "at" || sa.RefreshToken != "rt" || sa.ExpiresAt != 1753600000 {
		t.Errorf("tokens: %+v", sa)
	}
	if sa.UID != "u1" || sa.EnterpriseID != "e1" || sa.Nickname != "n1" {
		t.Errorf("account: %+v", sa)
	}
	if sa.Region() != "cn" {
		t.Errorf("region want cn, got %s", sa.Region())
	}
}

func TestParseFlat(t *testing.T) {
	raw := []byte(`{"accessToken":"at","refreshToken":"rt","expiresAt":1753600000,"uid":"u2","nickname":"n2"}`)
	sa, err := Parse(raw)
	if err != nil || sa.UID != "u2" || sa.AccessToken != "at" {
		t.Fatalf("flat: %+v %v", sa, err)
	}
}

func TestParseMissingToken(t *testing.T) {
	if _, err := Parse([]byte(`{"uid":"u3"}`)); err == nil {
		t.Fatal("want error for missing accessToken")
	}
}

func TestGlobalRegion(t *testing.T) {
	for _, d := range []string{"workbuddy.ai", "www.workbuddy.ai", "api.workbuddy.ai", "WorkBuddy.AI"} {
		sa := &Auth{Domain: d}
		if sa.Region() != "global" {
			t.Errorf("domain %q want global, got %s", d, sa.Region())
		}
	}
	for _, d := range []string{"", "codebuddy.cn", "www.codebuddy.cn"} {
		sa := &Auth{Domain: d}
		if sa.Region() != "cn" {
			t.Errorf("domain %q want cn, got %s", d, sa.Region())
		}
	}
}

func TestSaveAtomicRoundtrip(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "workbuddy-u1.json")
	a := &Auth{AccessToken: "at", RefreshToken: "rt", ExpiresAt: 1753600000,
		UID: "u1", EnterpriseID: "e1", Nickname: "n1", FilePath: fp}
	if err := a.SaveAtomic(); err != nil {
		t.Fatalf("save: %v", err)
	}
	if _, err := os.Stat(fp + ".tmp"); !os.IsNotExist(err) {
		t.Error("tmp file should not remain")
	}
	raw, err := os.ReadFile(fp)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	b, err := Parse(raw)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if b.AccessToken != "at" || b.UID != "u1" || b.EnterpriseID != "e1" {
		t.Errorf("roundtrip: %+v", b)
	}
}

func TestLoadDirFiltersRegion(t *testing.T) {
	dir := t.TempDir()
	cn := `{"auth":{"accessToken":"at1","refreshToken":"r","expiresAt":1,"domain":""},"account":{"uid":"cn1"}}`
	gl := `{"auth":{"accessToken":"at2","refreshToken":"r","expiresAt":1,"domain":"www.workbuddy.ai"},"account":{"uid":"g1"}}`
	bad := `not json`
	os.WriteFile(filepath.Join(dir, "workbuddy-cn1.json"), []byte(cn), 0o600)
	os.WriteFile(filepath.Join(dir, "workbuddy-g1.json"), []byte(gl), 0o600)
	os.WriteFile(filepath.Join(dir, "workbuddy-bad.json"), []byte(bad), 0o600)

	list, err := LoadDir(dir, "cn")
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(list) != 1 || list[0].UID != "cn1" {
		t.Fatalf("want 1 cn account, got %+v", list)
	}
	if list[0].FilePath == "" {
		t.Error("FilePath not set")
	}
}

func TestNeedsRefresh(t *testing.T) {
	a := &Auth{ExpiresAt: 0}
	if !a.NeedsRefresh(0) {
		t.Error("zero expiry should need refresh")
	}
	a.ExpiresAt = 9999999999
	if a.NeedsRefresh(0) {
		t.Error("far future should not need refresh")
	}
}

func TestParseApiKey(t *testing.T) {
	raw := []byte(`{"apiKey":"atr_test_123","uid":"atria-1","nickname":"test-atria"}`)
	a, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse apiKey err: %v", err)
	}
	if a.AccessToken != "atr_test_123" {
		t.Errorf("want atr_test_123, got %s", a.AccessToken)
	}
	if a.UID != "atria-1" || a.Nickname != "test-atria" {
		t.Errorf("unexpected account: %+v", a)
	}
}

func TestLoadAtriaDir(t *testing.T) {
	dir := t.TempDir()
	f1 := `{"apiKey":"atr_1","uid":"a1"}`
	f2 := `{"accessToken":"atr_2"}`
	fEx := `{"apiKey":"sample"}`
	os.WriteFile(filepath.Join(dir, "atria-1.json"), []byte(f1), 0o600)
	os.WriteFile(filepath.Join(dir, "atria-2.json"), []byte(f2), 0o600)
	os.WriteFile(filepath.Join(dir, "atria-example.json.example"), []byte(fEx), 0o600)

	list, err := LoadAtriaDir(dir)
	if err != nil {
		t.Fatalf("load atria: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 accounts (excluding example), got %d", len(list))
	}
	for _, a := range list {
		if a.Kind != "atria" {
			t.Errorf("want kind atria, got %s", a.Kind)
		}
		if a.ExpiresAt <= 0 {
			t.Errorf("want positive expiresAt, got %d", a.ExpiresAt)
		}
	}
}

func TestParseBOM(t *testing.T) {
	raw := append([]byte("\xef\xbb\xbf"), []byte(`{"accessToken":"tok_bom","uid":"u_bom"}`)...)
	a, err := Parse(raw)
	if err != nil {
		t.Fatalf("parse with BOM failed: %v", err)
	}
	if a.UID != "u_bom" || a.AccessToken != "tok_bom" {
		t.Fatalf("unexpected auth: %+v", a)
	}
}

func TestLoadTierflowDir(t *testing.T) {
	dir := t.TempDir()
	// 嵌套形（带 uid）与扁平形（无 uid，靠文件名兜底）各一，验证多账号互不覆盖。
	f1 := `{"auth":{"accessToken":"sk-1","apiHost":"https://tierflow.cn/v1"},"account":{"uid":"tierflow-1","mode":"direct","nickname":"#1"}}`
	f2 := `{"apiKey":"sk-2"}`
	os.WriteFile(filepath.Join(dir, "tierflow-1.json"), []byte(f1), 0o600)
	os.WriteFile(filepath.Join(dir, "tierflow-2.json"), []byte(f2), 0o600)

	list, err := LoadTierflowDir(dir)
	if err != nil {
		t.Fatalf("load tierflow: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("want 2 accounts, got %d", len(list))
	}
	seen := map[string]bool{}
	for _, a := range list {
		if a.Kind != "tierflow" {
			t.Errorf("want kind tierflow, got %s", a.Kind)
		}
		if a.Mode != "direct" {
			t.Errorf("want mode direct, got %s", a.Mode)
		}
		if a.AccessToken == "" || a.UID == "" || a.FilePath == "" {
			t.Errorf("incomplete auth: %+v", a)
		}
		seen[a.UID] = true
	}
	if !seen["tierflow-1"] || !seen["tierflow-2"] || len(seen) != 2 {
		t.Errorf("uids not distinct: %+v", seen)
	}

	// 缺文件不报错、不暴露渠道。
	empty, err := LoadTierflowDir(t.TempDir())
	if err != nil || empty != nil {
		t.Errorf("missing dir: want (nil,nil), got (%v,%v)", empty, err)
	}
}
