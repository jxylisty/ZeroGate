package zcode

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// validCaptchaParam 构造一个通过 ValidateCaptchaParam 的 param（长度 ≥ 200）。
func validCaptchaParam(t *testing.T) string {
	t.Helper()
	payload := map[string]any{
		"certifyId":     "cert-123",
		"securityToken": strings.Repeat("x", 60),
		"pad":           strings.Repeat("p", 160),
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func TestValidateCaptchaParam(t *testing.T) {
	if err := ValidateCaptchaParam("short"); err == nil {
		t.Error("短 param 应被拒")
	}
	valid := validCaptchaParam(t)
	if len(valid) < captchaMinParamLen {
		t.Fatalf("测试 param 未达最小长度：%d", len(valid))
	}
	if err := ValidateCaptchaParam(valid); err != nil {
		t.Errorf("合法 param 被拒：%v", err)
	}
	// 缺 certifyId
	raw, _ := json.Marshal(map[string]any{"certifyId": "", "securityToken": strings.Repeat("x", 60), "pad": strings.Repeat("p", 160)})
	if err := ValidateCaptchaParam(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Error("缺 certifyId 应被拒")
	}
	// securityToken 过短
	raw, _ = json.Marshal(map[string]any{"certifyId": "c", "securityToken": "x", "pad": strings.Repeat("p", 160)})
	if err := ValidateCaptchaParam(base64.StdEncoding.EncodeToString(raw)); err == nil {
		t.Error("securityToken 过短应被拒")
	}
}

func TestCarrierSessionOneShot(t *testing.T) {
	token := NewCarrierSession()
	if !CarrierPending(token) {
		t.Fatal("新会话应为 pending")
	}
	valid := validCaptchaParam(t)
	if err := DeliverCaptchaParam(token, valid); err != nil {
		t.Fatalf("投递合法 param 失败：%v", err)
	}
	if CarrierPending(token) {
		t.Error("投递后会话应被消费")
	}
	// 二次投递应失败（一次性）
	if err := DeliverCaptchaParam(token, valid); err == nil {
		t.Error("重复投递同一 token 应失败")
	}
}

func TestCarrierSessionInvalidParamKeepsSession(t *testing.T) {
	token := NewCarrierSession()
	if err := DeliverCaptchaParam(token, "too-short"); err == nil {
		t.Fatal("非法 param 应被拒")
	}
	if !CarrierPending(token) {
		t.Error("非法 param 不应消耗一次性 token（页面需可重试）")
	}
}

func TestCarrierSessionExpired(t *testing.T) {
	token, _ := registerCarrierSession(-time.Second)
	if CarrierPending(token) {
		t.Error("过期会话不应 pending")
	}
}

func TestCarrierPageHTMLContainsToken(t *testing.T) {
	seedCarrierConfigCache(CaptchaCarrierConfig{Region: "cn", Prefix: "no8xfe", SceneID: "11xygtvd"})
	token := NewCarrierSession()
	contribute := "http://127.0.0.1:7863/api/zcode/carrier/contribute"
	html, ok := CarrierPage(token, contribute)
	if !ok {
		t.Fatal("已知 token 应渲染成功")
	}
	if !strings.Contains(html, token) {
		t.Error("载体页应包含 token")
	}
	if !strings.Contains(html, contribute) {
		t.Error("载体页应包含回传地址")
	}
	if !strings.Contains(html, "11xygtvd") {
		t.Error("载体页应包含 sceneId")
	}
}

func TestCarrierPageUnknownToken(t *testing.T) {
	if _, ok := CarrierPage("does-not-exist", "http://127.0.0.1:7863/x"); ok {
		t.Error("未知 token 应返回 ok=false")
	}
}

// TestFetchCarrierConfigFallback 取消的 context 应立刻退回兜底三元组（不依赖网络）。
func TestFetchCarrierConfigFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cfg := fetchCarrierConfig(ctx)
	if cfg != captchaCarrierFallback {
		t.Errorf("抓取失败应退回兜底：got %+v", cfg)
	}
}

// seedCarrierConfigCache 预热配置缓存，避免单测触发真实网络请求。
func seedCarrierConfigCache(cfg CaptchaCarrierConfig) {
	carrierConfigCache.Lock()
	carrierConfigCache.cfg = cfg
	carrierConfigCache.expires = time.Now().Add(time.Hour)
	carrierConfigCache.loaded = true
	carrierConfigCache.Unlock()
}
