package traework

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
)

func TestDailyCheckinClaimsWhenNotCheckedIn(t *testing.T) {
	var statusCalls, claimCalls atomic.Int32
	var checked atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Cloud-IDE-JWT at" || r.Header.Get("X-User-Region") != "CN" {
			t.Errorf("missing Trae UG headers: auth=%q region=%q", r.Header.Get("Authorization"), r.Header.Get("X-User-Region"))
		}
		switch r.URL.Path {
		case EpCheckinStatus:
			statusCalls.Add(1)
			_, _ = w.Write([]byte(fmt.Sprintf(`{"checked_in":%t,"credits":200,"enable":true}`, checked.Load())))
		case EpCheckinClaim:
			claimCalls.Add(1)
			checked.Store(true)
			_, _ = w.Write([]byte(`{"code":0,"message":"success"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	c := New()
	c.CheckinRetryDelay = 0
	c.HTTP = srv.Client()
	c.UgHost = srv.URL
	if err := c.DailyCheckin(&auth.Auth{AccessToken: "at", DeviceID: "device"}); err != nil {
		t.Fatalf("daily checkin: %v", err)
	}
	if statusCalls.Load() != 2 || claimCalls.Load() != 1 {
		t.Fatalf("status calls=%d claim calls=%d", statusCalls.Load(), claimCalls.Load())
	}
}

func TestDailyCheckinSkipsClaimWhenAlreadyCheckedIn(t *testing.T) {
	var claimCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == EpCheckinStatus {
			_, _ = w.Write([]byte(`{"checked_in":true,"credits":200,"enable":true}`))
			return
		}
		if r.URL.Path == EpCheckinClaim {
			claimCalls.Add(1)
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New()
	c.HTTP = srv.Client()
	c.UgHost = srv.URL
	if err := c.DailyCheckin(&auth.Auth{AccessToken: "at"}); err == nil || err.Error() != "已签到" {
		t.Fatalf("err=%v, want 已签到", err)
	}
	if claimCalls.Load() != 0 {
		t.Fatalf("claim calls=%d", claimCalls.Load())
	}
}

func TestCheckinClaimBusinessError(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == EpCheckinClaim {
			calls.Add(1)
			_, _ = w.Write([]byte(`{"code":9074,"message":"operation too frequent"}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	c := New()
	c.CheckinRetryDelay = 0
	c.HTTP = srv.Client()
	c.UgHost = srv.URL
	if err := c.CheckinClaim(&auth.Auth{AccessToken: "at"}); err == nil || !strings.Contains(err.Error(), "9074") {
		t.Fatalf("err=%v, want business 9074 error", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("claim calls=%d, want retry", calls.Load())
	}
}

func TestCheckinClaimRetriesRateLimit(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EpCheckinClaim {
			http.NotFound(w, r)
			return
		}
		if calls.Add(1) == 1 {
			_, _ = w.Write([]byte(`{"code":9074,"message":"operation too frequent"}`))
			return
		}
		_, _ = w.Write([]byte(`{"code":0,"message":"success"}`))
	}))
	defer srv.Close()

	c := New()
	c.CheckinRetryDelay = 0
	c.HTTP = srv.Client()
	c.UgHost = srv.URL
	if err := c.CheckinClaim(&auth.Auth{AccessToken: "at"}); err != nil {
		t.Fatalf("retry claim: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("claim calls=%d", calls.Load())
	}
}

// TestFetchModelsParsesContextWindow 校验 get_detail_param 的上下文窗口/输出上限
// 被解析进 ModelInfo（此前漏解析 → /v1/models 缺 context_length → 客户端误判小窗口过早压缩）。
func TestFetchModelsParsesContextWindow(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != EpModels {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(`{"config_info_list":[
			{"config_name":"glm-5.2","context_window_tokens":{"dev":200000},
			 "model_detail_list":[{"max_tokens":32000,"prompt_max_tokens":168000}],
			 "display_config":{"display_name":"GLM-5.2","is_custom_model":false}},
			{"config_name":"aquila","context_window_tokens":{"dev":272000},
			 "model_detail_list":[{"max_tokens":32000}],
			 "display_config":{"display_name":"Aquila","is_custom_model":false}},
			{"config_name":"custom_model_gemini","context_window_tokens":{"dev":300000,"max":300000},
			 "model_detail_list":[{"max_tokens":32000}],
			 "display_config":{"display_name":"Gemini","is_custom_model":false}},
			{"config_name":"custom_model_placeholder","context_window_tokens":{"dev":128000},
			 "model_detail_list":[{"max_tokens":16000}],
			 "display_config":{"display_name":"Placeholder","is_custom_model":true}}
		]}`))
	}))
	defer srv.Close()

	c := New()
	c.HTTP = srv.Client()
	c.AgentHost = srv.URL

	infos, err := c.FetchModels(&auth.Auth{AccessToken: "at"})
	if err != nil {
		t.Fatalf("fetch models: %v", err)
	}
	byID := map[string]provider.ModelInfo{}
	for _, mi := range infos {
		byID[mi.ID] = mi
	}
	// 自定义模型（is_custom_model=true / custom_model_ 前缀）应被过滤
	if _, ok := byID["custom_model_placeholder"]; ok {
		t.Fatalf("custom model should be filtered out")
	}
	if _, ok := byID["custom_model_gemini"]; ok {
		t.Fatalf("custom_model_ prefixed model should be filtered out")
	}
	cases := []struct {
		id, name   string
		contextWin int64
		maxTokens  int64
	}{
		{"glm-5.2", "GLM-5.2", 200000, 32000},
		{"aquila", "Aquila", 272000, 32000},
	}
	for _, tc := range cases {
		mi, ok := byID[tc.id]
		if !ok {
			t.Fatalf("model %s missing", tc.id)
		}
		if mi.Name != tc.name {
			t.Errorf("%s name=%q want %q", tc.id, mi.Name, tc.name)
		}
		if mi.ContextWindow != tc.contextWin {
			t.Errorf("%s context_window=%d want %d", tc.id, mi.ContextWindow, tc.contextWin)
		}
		if mi.MaxTokens != tc.maxTokens {
			t.Errorf("%s max_tokens=%d want %d", tc.id, mi.MaxTokens, tc.maxTokens)
		}
	}
}

// TestPickContextWindow 覆盖 dev/max/回退/空表四种取值。
func TestPickContextWindow(t *testing.T) {
	cases := []struct {
		in   map[string]int64
		want int64
	}{
		{map[string]int64{"dev": 200000}, 200000},
		{map[string]int64{"max": 272000}, 272000},
		{map[string]int64{"dev": 300000, "max": 300000}, 300000},
		{map[string]int64{"foo": 128000}, 128000},
		{map[string]int64{"dev": 0, "max": 0}, 0},
		{nil, 0},
	}
	for _, tc := range cases {
		if got := pickContextWindow(tc.in); got != tc.want {
			t.Errorf("pickContextWindow(%v)=%d want %d", tc.in, got, tc.want)
		}
	}
}
