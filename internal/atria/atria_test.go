package atria

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
)

func TestClassify(t *testing.T) {
	c := New()
	tests := []struct {
		status int
		body   string
		want   provider.ErrKind
	}{
		{401, `{"error":{"message":"invalid api key"}}`, provider.ErrSessionDead},
		{429, `{"error":{"message":"rate limit exceeded"}}`, provider.ErrSoftRate},
		{429, `{"error":{"message":"insufficient quota"}}`, provider.ErrHardCredit},
		{404, `{"error":{"message":"not found"}}`, provider.ErrNotFound},
		{500, `{"error":{"message":"internal error"}}`, provider.ErrServer},
		{400, `{"error":{"message":"bad request"}}`, provider.ErrClient},
		{200, `{"choices":[]}`, provider.ErrNone},
	}
	for _, tt := range tests {
		got := c.Classify(tt.status, tt.body)
		if got != tt.want {
			t.Errorf("Classify(%d, %q) = %v, want %v", tt.status, tt.body, got, tt.want)
		}
	}
}

func TestStaticModels(t *testing.T) {
	models := StaticModels()
	if len(models) == 0 {
		t.Fatal("StaticModels should not be empty")
	}
	if models[0].ID != ModelID {
		t.Errorf("want model ID %s, got %s", ModelID, models[0].ID)
	}
}

func TestChatStreamRewrite(t *testing.T) {
	receivedBody := ""
	authHeader := ""

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		b, _ := io.ReadAll(r.Body)
		receivedBody = string(b)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	c := New()
	c.Base = server.URL

	a := &auth.Auth{AccessToken: "atr_my_test_key", UID: "atria-test"}
	body := []byte(`{"model":"custom-model","messages":[{"role":"user","content":"hi"}],"max_tokens":100}`)

	rc, status, _, err := c.ChatStream(a, body)
	if err != nil {
		t.Fatalf("ChatStream error: %v", err)
	}
	defer rc.Close()

	if status != http.StatusOK {
		t.Errorf("want status 200, got %d", status)
	}
	if authHeader != "Bearer atr_my_test_key" {
		t.Errorf("want Bearer atr_my_test_key, got %s", authHeader)
	}
	if bytes.Contains([]byte(receivedBody), []byte("max_tokens")) {
		t.Errorf("body should have max_tokens removed: %s", receivedBody)
	}
	if !bytes.Contains([]byte(receivedBody), []byte(`"stream":true`)) {
		t.Errorf("body should have stream:true: %s", receivedBody)
	}
	if !bytes.Contains([]byte(receivedBody), []byte(ModelID)) {
		t.Errorf("body should have model Atria-Dawn-Preview: %s", receivedBody)
	}
}
