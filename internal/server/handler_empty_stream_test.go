package server

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/pool"
	"zerogate/internal/provider"
)

// fakeUpstream 只实现测试关注的行为：ChatStream 按调用序返回 bodies，
// Stream 原样拷贝，其余方法空实现。
type fakeUpstream struct {
	mu        sync.Mutex
	bodies    []string
	chats     int
	chunkSize int // >0 时把响应体按该尺寸切块输出，模拟慢流 / 跨 chunk 边界
}

func (f *fakeUpstream) ChatStream(a *auth.Auth, body []byte) (io.ReadCloser, int, []byte, error) {
	f.mu.Lock()
	i := f.chats
	f.chats++
	b := ""
	if i < len(f.bodies) {
		b = f.bodies[i]
	}
	size := f.chunkSize
	f.mu.Unlock()
	if size > 0 {
		return io.NopCloser(&chunkReader{data: []byte(b), size: size}), http.StatusOK, nil, nil
	}
	return io.NopCloser(strings.NewReader(b)), http.StatusOK, nil, nil
}

func (f *fakeUpstream) Stream(w http.ResponseWriter, r io.Reader) error {
	_, err := io.Copy(w, r)
	if fl, ok := w.(http.Flusher); ok {
		fl.Flush()
	}
	return err
}

func (f *fakeUpstream) Classify(status int, body string) provider.ErrKind { return provider.ErrNone }
func (f *fakeUpstream) RefreshToken(a *auth.Auth) error                   { return nil }
func (f *fakeUpstream) FetchModels(a *auth.Auth) ([]provider.ModelInfo, error) {
	return nil, nil
}
func (f *fakeUpstream) FetchModelPricing(a *auth.Auth) ([]provider.ModelPricing, error) {
	return nil, nil
}
func (f *fakeUpstream) UserResource(a *auth.Auth) (int64, error) { return 0, nil }
func (f *fakeUpstream) UserResourceDetail(a *auth.Auth) (int64, []provider.ResourceItem, error) {
	return 0, nil, nil
}
func (f *fakeUpstream) DailyCheckin(a *auth.Auth) error  { return nil }
func (f *fakeUpstream) GrowthTravel(a *auth.Auth) error  { return nil }
func (f *fakeUpstream) GrowthLottery(a *auth.Auth) error { return nil }
func (f *fakeUpstream) GrowthTasksDetailed(a *auth.Auth) provider.GrowthTasksResult {
	return provider.GrowthTasksResult{}
}
func (f *fakeUpstream) Aggregate(r io.Reader) (map[string]any, error) {
	return nil, errors.New("not implemented")
}

func (f *fakeUpstream) chatCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.chats
}

func newEmptyStreamHandler(t *testing.T, fu provider.Upstream, uids ...string) (*Handler, *pool.Pool) {
	t.Helper()
	p := pool.New(t.TempDir() + "/state.json")
	for i, uid := range uids {
		a := &auth.Auth{UID: uid, Kind: "qoder", AccessToken: "t"}
		a.ExpiresAt = time.Now().Add(time.Hour).Unix() // 不触发 RefreshToken
		p.Add(a)
		// 池内到期时间按加入顺序递增：uids[0] 最早到期 → Pick 确定性优先选它，
		// 否则 betterThan 平手时结果取决于 map 随机遍历序，轮换测试会 flaky。
		p.SetExpire(uid, time.Now().Add(time.Duration(i+1)*time.Hour).Unix())
	}
	h := NewHandler(Config{
		Runtimes: map[provider.Kind]*Runtime{
			provider.Qoder: {Kind: provider.Qoder, Pool: p, Upstream: fu},
		},
	})
	return h, p
}

func postStreamChat(h *Handler, model string) *httptest.ResponseRecorder {
	body := `{"model":"` + model + `","stream":true}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.mux.ServeHTTP(rec, req)
	return rec
}

// 空流触发轮换：一号空流吃短冷却（不累计 errCount）并轮换，客户端无感拿到二号正常流。
func TestEmptyStreamRotatesToNextAccount(t *testing.T) {
	fu := &fakeUpstream{bodies: []string{
		"", // 第一号：HTTP 200 但 EOF 无任何 data 帧
		"data: {\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":7}}\n\ndata: [DONE]\n\n",
	}}
	h, p := newEmptyStreamHandler(t, fu, "a1", "a2")

	rec := postStreamChat(h, "qoder/m1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200, body=%s", rec.Code, rec.Body.String())
	}
	if got := fu.chatCount(); got != 2 {
		t.Fatalf("chat calls = %d, want 2 (空流后应轮换重试)", got)
	}
	if !strings.Contains(rec.Body.String(), `"prompt_tokens":5`) {
		t.Fatalf("client body missing usage from second account: %s", rec.Body.String())
	}
	s1, _ := p.Status("a1")
	s2, _ := p.Status("a2")
	if s1.ErrCount+s2.ErrCount != 0 {
		t.Fatalf("total err_count = %d+%d, want 0 (空流不累计账号错误)", s1.ErrCount, s2.ErrCount)
	}
	if !s1.Cooling {
		t.Fatalf("空流账号应处于短冷却中")
	}
}

// 正常流零损耗透传：窥探字节必须原样续传，包括 data: 关键字跨 chunk 边界。
func TestNormalStreamPassthroughUnchanged(t *testing.T) {
	sse := "data: {\"id\":\"x\"}\n\ndata: {\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":22}}\n\ndata: [DONE]\n\n"
	fu := &fakeUpstream{bodies: []string{sse}, chunkSize: 3}
	h, p := newEmptyStreamHandler(t, fu, "a1")

	rec := postStreamChat(h, "qoder/m1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != sse {
		t.Fatalf("client body mismatch (窥探字节未原样续传):\n got=%q\nwant=%q", rec.Body.String(), sse)
	}
	if got := fu.chatCount(); got != 1 {
		t.Fatalf("chat calls = %d, want 1", got)
	}
	if s, _ := p.Status("a1"); s.ErrCount != 0 {
		t.Fatalf("err_count = %d, want 0", s.ErrCount)
	}
}

// usage 报 0 token 的"假成功"：完整透传给客户端，账号侧只吃短冷却不累计
// errCount（2026-10-09 整改：非风控/封号级错误不允许长停账号）。
func TestZeroTokenUsageGetsShortCooldown(t *testing.T) {
	fu := &fakeUpstream{bodies: []string{
		"data: {\"usage\":{\"prompt_tokens\":0,\"completion_tokens\":0}}\n\ndata: [DONE]\n\n",
	}}
	h, p := newEmptyStreamHandler(t, fu, "a1")

	rec := postStreamChat(h, "qoder/m1")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (流已开始，只能透传)", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"prompt_tokens":0`) {
		t.Fatalf("client body missing passthrough usage: %s", rec.Body.String())
	}
	if got := fu.chatCount(); got != 1 {
		t.Fatalf("chat calls = %d, want 1", got)
	}
	s, _ := p.Status("a1")
	if s.ErrCount != 0 {
		t.Fatalf("err_count = %d, want 0 (0-token 假成功不累计账号错误)", s.ErrCount)
	}
	if !s.Cooling {
		t.Fatalf("应处于短冷却中")
	}
}

func TestSSEStreamLooksEmpty(t *testing.T) {
	cases := []struct {
		name      string
		input     string
		chunkSize int
		wantEmpty bool
	}{
		{"空体", "", 0, true},
		{"无data行", "hello\nworld\n", 0, true},
		{"首帧即data", "data: hi\n\n", 0, false},
		{"data跨chunk边界", "data: hi\n\n", 3, false},
		{"不完整data前缀", "dat", 0, true},
		{"data前缀跨chunk但未完成", "hello dat", 4, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rc io.ReadCloser = io.NopCloser(strings.NewReader(tc.input))
			if tc.chunkSize > 0 {
				rc = io.NopCloser(&chunkReader{data: []byte(tc.input), size: tc.chunkSize})
			}
			empty, _, err := sseStreamLooksEmpty(rc)
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if empty != tc.wantEmpty {
				t.Fatalf("empty = %v, want %v", empty, tc.wantEmpty)
			}
		})
	}
}

func TestSSEStreamLooksEmptyReadError(t *testing.T) {
	rc := io.NopCloser(&errReader{data: []byte("partial"), err: errors.New("boom")})
	empty, head, err := sseStreamLooksEmpty(rc)
	if err == nil || empty {
		t.Fatalf("want (false, err), got empty=%v err=%v", empty, err)
	}
	if string(head) != "partial" {
		t.Fatalf("head = %q, want %q", head, "partial")
	}
}

func TestPeekedStreamReplaysHead(t *testing.T) {
	ps := &peekedStream{ReadCloser: io.NopCloser(strings.NewReader("TAIL")), head: []byte("HEAD")}
	out, err := io.ReadAll(ps)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if string(out) != "HEADTAIL" {
		t.Fatalf("out = %q, want %q", out, "HEADTAIL")
	}
}

type chunkReader struct {
	data []byte
	off  int
	size int
}

func (c *chunkReader) Read(b []byte) (int, error) {
	if c.off >= len(c.data) {
		return 0, io.EOF
	}
	end := c.off + c.size
	if end > len(c.data) {
		end = len(c.data)
	}
	n := copy(b, c.data[c.off:end])
	c.off += n
	return n, nil
}

type errReader struct {
	data []byte
	off  int
	err  error
}

func (e *errReader) Read(b []byte) (int, error) {
	if e.off < len(e.data) {
		n := copy(b, e.data[e.off:])
		e.off += n
		return n, nil
	}
	return 0, e.err
}
