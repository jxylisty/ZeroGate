package qoder

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
)

func TestNormalizeModelName(t *testing.T) {
	cases := map[string]string{
		"Qwen3.8-Max":      "qwen3.8-max",
		"DeepSeek-V4-Pro":  "deepseek-v4-pro",
		"GLM-5.3":          "glm-5.3",
		"Kimi-K2.7-Code":   "kimi-k2.7-code",
		"MiniMax-M2.7":     "minimax-m2.7",
		"Auto":             "auto",
		"Qwen3.8 Max Test": "qwen3.8-max-test",
	}
	for in, want := range cases {
		if got := NormalizeModelName(in); got != want {
			t.Errorf("NormalizeModelName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestModelKeyStatic(t *testing.T) {
	cases := map[string]string{
		"deepseek-v4-pro":  "dmodel",
		"glm-5.3":          "gmodel",
		"glm-5.2":          "gm51model",
		"qwen3.8-max":      "qmodel_38max",
		"kimi-k2.7-code":   "kmodel",
		"unknown-model":    "",
	}
	for name, want := range cases {
		if got := ModelKey(name); got != want {
			t.Errorf("ModelKey(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestModelKeyDynamicPrecedence(t *testing.T) {
	c := New()
	c.setModelMap(map[string]string{"glm-5.3": "gmodel-new", "custom": "ckey"})
	if got := c.modelKey("glm-5.3"); got != "gmodel-new" {
		t.Errorf("dynamic should win, got %q", got)
	}
	if got := c.modelKey("custom"); got != "ckey" {
		t.Errorf("dynamic custom got %q", got)
	}
	if got := c.modelKey("deepseek-v4-pro"); got != "dmodel" { // 动态缺失 → 静态兜底
		t.Errorf("static fallback got %q", got)
	}
}

func TestEncodeDecodeRoundtrip(t *testing.T) {
	plain := []byte(`{"a":1,"b":"中文内容 😀","c":[true,null,3.14]}`)
	enc := qoderEncode(plain)
	if enc == "" {
		t.Fatal("empty encode")
	}
	dec, err := qoderDecode(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(dec) != string(plain) {
		t.Errorf("roundtrip mismatch:\n in=%s\nout=%s", plain, dec)
	}
}

func TestDecodeInvalidChar(t *testing.T) {
	if _, err := qoderDecode("!!!"); err == nil {
		t.Error("expected error for invalid char")
	}
}

func TestBuildAgentBodyDeveloperToSystem(t *testing.T) {
	// developer 角色应改写为 system；其余消息不受影响；原数据不被污染。
	msgs := []map[string]any{
		{"role": "developer", "content": "你是助手"},
		{"role": "system", "content": "保持简洁"},
		{"role": "user", "content": "你好"},
	}
	raw, err := buildAgentBody(msgs, "dmodel", nil, ChatOptions{})
	if err != nil {
		t.Fatalf("buildAgentBody: %v", err)
	}
	var body struct {
		Messages []map[string]any `json:"messages"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(body.Messages) != 3 {
		t.Fatalf("message count = %d, want 3", len(body.Messages))
	}
	if body.Messages[0]["role"] != "system" {
		t.Errorf("developer should become system, got %v", body.Messages[0]["role"])
	}
	if body.Messages[1]["role"] != "system" {
		t.Errorf("system unchanged, got %v", body.Messages[1]["role"])
	}
	if body.Messages[2]["role"] != "user" {
		t.Errorf("user unchanged, got %v", body.Messages[2]["role"])
	}
	// 原数据不被污染
	if msgs[0]["role"] != "developer" {
		t.Errorf("source mutated: role=%v", msgs[0]["role"])
	}
	// prompt 取最后一条 user 内容
	var prompt struct {
		ChatContext struct {
			Text struct {
				Text string `json:"text"`
			} `json:"text"`
		} `json:"chat_context"`
	}
	_ = json.Unmarshal(raw, &prompt)
	if prompt.ChatContext.Text.Text != "你好" {
		t.Errorf("prompt = %q, want 你好", prompt.ChatContext.Text.Text)
	}
}

func TestBuildAgentBodySamplingParams(t *testing.T) {
	// 采样/思考参数必须落在顶层 parameters；source=system 必须落在顶层 model_config。
	temp := 0.0 // 零值也要透传，靠指针区分"未传"
	topP := 0.9
	maxTok := 4096
	raw, err := buildAgentBody(
		[]map[string]any{{"role": "user", "content": "hi"}},
		"dmodel", nil,
		ChatOptions{
			ReasoningEffort: "high",
			Temperature:     &temp,
			TopP:            &topP,
			MaxTokens:       &maxTok,
		},
	)
	if err != nil {
		t.Fatalf("buildAgentBody: %v", err)
	}
	var body struct {
		ModelConfig map[string]any `json:"model_config"`
		Parameters  map[string]any `json:"parameters"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if body.ModelConfig["source"] != "system" {
		t.Errorf("model_config.source = %v, want system", body.ModelConfig["source"])
	}
	if body.ModelConfig["is_reasoning"] != true {
		t.Errorf("model_config.is_reasoning = %v, want true", body.ModelConfig["is_reasoning"])
	}
	p := body.Parameters
	if p["temperature"] != 0.0 {
		t.Errorf("parameters.temperature = %v, want 0", p["temperature"])
	}
	if p["top_p"] != 0.9 {
		t.Errorf("parameters.top_p = %v, want 0.9", p["top_p"])
	}
	if p["max_tokens"] != 4096.0 {
		t.Errorf("parameters.max_tokens = %v, want 4096", p["max_tokens"])
	}
	if p["enable_thinking"] != true {
		t.Errorf("parameters.enable_thinking = %v, want true", p["enable_thinking"])
	}
	if p["reasoning_effort"] != "xhigh" {
		t.Errorf("parameters.reasoning_effort = %v, want xhigh (high→xhigh)", p["reasoning_effort"])
	}
}

func TestQoder10605ClassifyAndParse(t *testing.T) {
	sample10605 := []byte(`{"code":"10605","message":"{\"isQueued\":false,\"modelKey\":\"gfmodel\",\"queueCount\":0,\"queueType\":\"p3\",\"retryAfterSeconds\":2,\"serviceAvailable\":true,\"waitTime\":0}"}`)
	retryAfter, ok := parse10605(sample10605)
	if !ok || retryAfter != 2 {
		t.Fatalf("parse10605 failed: ok=%v retryAfter=%d (want 2)", ok, retryAfter)
	}

	kind := Classify(403, string(sample10605))
	if kind != provider.ErrSoftRate {
		t.Fatalf("Classify 10605 failed: got %v, want ErrSoftRate", kind)
	}
}

// campaignsResp 构造 GET campaigns 的响应体。
func campaignsResp(items ...map[string]string) string {
	cps := make([]string, 0, len(items))
	for _, it := range items {
		b, _ := json.Marshal(map[string]string{
			"campaignId":  it["id"],
			"campaignKey": it["key"],
			"actionType":  it["type"],
			"claimStatus": it["status"],
		})
		cps = append(cps, string(b))
	}
	return `{"campaigns":[` + strings.Join(cps, ",") + `]}`
}

func checkinTestAuth(t *testing.T) *auth.Auth {
	t.Helper()
	a := &auth.Auth{UID: "u-test", AccessToken: "dt-token-xyz", ExpiresAt: 99999999999}
	if a.JWT() != "dt-token-xyz" {
		t.Fatal("JWT snapshot mismatch")
	}
	return a
}

func TestDailyCheckinClaimsWhenClaimable(t *testing.T) {
	var getCalls, postCalls atomic.Int32
	var claimedPath atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 鉴权头断言：dt- Bearer + 客户端类型
		if got := r.Header.Get("Authorization"); got != "Bearer dt-token-xyz" {
			t.Errorf("Authorization = %q", got)
		}
		if got := r.Header.Get("cosy-clienttype"); got != "10" {
			t.Errorf("cosy-clienttype = %q", got)
		}
		if got := r.Header.Get("user-agent"); got != "Qoder" {
			t.Errorf("user-agent = %q", got)
		}
		switch r.URL.Path {
		case EpCampaigns:
			getCalls.Add(1)
			if r.Method != http.MethodGet {
				t.Errorf("campaigns method = %s", r.Method)
			}
			w.Write([]byte(campaignsResp(map[string]string{
				"id": "camp-1", "key": "daily-checkin", "type": "CLAIM_BENEFIT", "status": "CLAIMABLE",
			})))
		case "/sash/api/v1/me/campaigns/camp-1/claim":
			postCalls.Add(1)
			if r.Method != http.MethodPost {
				t.Errorf("claim method = %s", r.Method)
			}
			if got := r.Header.Get("origin"); got != "https://openapi.qoder.com.cn" {
				t.Errorf("claim origin = %q", got)
			}
			claimedPath.Store(r.URL.Path)
			w.Write([]byte(`{"status":"CLAIMED","replayed":false,"benefit":{"kind":"CREDIT","amount":100}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, srv.URL)
	err := c.DailyCheckin(checkinTestAuth(t))
	if err != nil {
		t.Fatalf("DailyCheckin: %v", err)
	}
	if got := claimedPath.Load(); got != "/sash/api/v1/me/campaigns/camp-1/claim" {
		t.Errorf("claim path = %v", got)
	}
	if getCalls.Load() != 1 || postCalls.Load() != 1 {
		t.Errorf("calls: get=%d post=%d", getCalls.Load(), postCalls.Load())
	}
}

func TestDailyCheckinAlreadyClaimedInList(t *testing.T) {
	var postCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == EpCampaigns {
			w.Write([]byte(campaignsResp(map[string]string{
				"id": "camp-1", "key": "daily-checkin", "type": "CLAIM_BENEFIT", "status": "CLAIMED",
			})))
			return
		}
		postCalls.Add(1) // 不应走到 claim
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, srv.URL)
	err := c.DailyCheckin(checkinTestAuth(t))
	if err == nil || !strings.Contains(err.Error(), "已签到") {
		t.Fatalf("want 已签到, got %v", err)
	}
	if postCalls.Load() != 0 {
		t.Errorf("claim should not be called, got %d", postCalls.Load())
	}
}

func TestDailyCheckinReplayedIsAlready(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case EpCampaigns:
			w.Write([]byte(campaignsResp(map[string]string{
				"id": "camp-1", "key": "daily-checkin", "type": "CLAIM_BENEFIT", "status": "CLAIMABLE",
			})))
		case "/sash/api/v1/me/campaigns/camp-1/claim":
			// 服务端幂等：重复领取返回 replayed=true
			w.Write([]byte(`{"status":"CLAIMED","replayed":true}`))
		}
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, srv.URL)
	err := c.DailyCheckin(checkinTestAuth(t))
	if err == nil || !strings.Contains(err.Error(), "已签到") {
		t.Fatalf("want 已签到, got %v", err)
	}
}

func TestDailyCheckinNoCampaign(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 只有非 CLAIM_BENEFIT 的活动
		w.Write([]byte(campaignsResp(map[string]string{
			"id": "camp-2", "key": "growth-task", "type": "FINISH_TASK", "status": "UNFINISHED",
		})))
	}))
	defer srv.Close()

	c := NewWithBase(srv.URL, srv.URL)
	err := c.DailyCheckin(checkinTestAuth(t))
	if err == nil || !strings.Contains(err.Error(), "无可用签到活动") {
		t.Fatalf("want 无可用签到活动, got %v", err)
	}
}

