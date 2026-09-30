package provider

import (
	"io"
	"strings"
	"testing"
	"time"
)

// silentReader 模拟"先给一段数据、之后永久静默"的僵死桥。
type silentReader struct {
	sent   bool
	closed chan struct{}
}

func (r *silentReader) Read(p []byte) (int, error) {
	if !r.sent {
		r.sent = true
		return copy(p, "data: hello\n\n"), nil
	}
	<-r.closed // 静默直到被 Close 唤醒
	return 0, io.ErrUnexpectedEOF
}

func (r *silentReader) Close() error {
	select {
	case <-r.closed:
	default:
		close(r.closed)
	}
	return nil
}

// trickleReader 持续吐字（每 gap 出 1 字节），用于验证不误杀活跃流。
type trickleReader struct {
	left int
	gap  time.Duration
}

func (r *trickleReader) Read(p []byte) (int, error) {
	if r.left == 0 {
		return 0, io.EOF
	}
	time.Sleep(r.gap)
	r.left--
	return copy(p, "x"), nil
}

func (r *trickleReader) Close() error { return nil }

func TestIdleTimeoutAbortsSilentStream(t *testing.T) {
	rc := IdleTimeout(&silentReader{closed: make(chan struct{})}, 100*time.Millisecond)
	defer rc.Close()

	buf := make([]byte, 64)
	if n, err := rc.Read(buf); n == 0 || err != nil {
		t.Fatalf("首段应有数据: n=%d err=%v", n, err)
	}

	start := time.Now()
	drained := false
	for time.Now().Before(start.Add(3 * time.Second)) {
		if _, err := rc.Read(buf); err != nil {
			if !strings.Contains(err.Error(), "idle") {
				t.Fatalf("应报空闲超时，实际: %v", err)
			}
			drained = true
			break
		}
	}
	if !drained {
		t.Fatal("静默后 Read 必须报错，否则又变成无限等待")
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("应在空闲超时附近断开，实际等了 %v", el)
	}
}

func TestIdleTimeoutKeepsActiveStream(t *testing.T) {
	// gap=30ms < d=120ms：每次读到数据都续期，不应误判空闲。
	rc := IdleTimeout(&trickleReader{left: 5, gap: 30 * time.Millisecond}, 120*time.Millisecond)
	defer rc.Close()

	buf := make([]byte, 8)
	got := 0
	for {
		n, err := rc.Read(buf)
		got += n
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("活跃流不该被中断: %v", err)
		}
	}
	if got != 5 {
		t.Fatalf("应读到 5 字节，实际 %d", got)
	}
}

func TestIdleTimeoutDisabled(t *testing.T) {
	inner := io.NopCloser(strings.NewReader("abc"))
	if got := IdleTimeout(inner, 0); got != io.ReadCloser(inner) {
		t.Fatal("d<=0 时应原样返回，不包装")
	}
	if got := IdleTimeout(nil, time.Second); got != nil {
		t.Fatal("nil body 应原样返回")
	}
}
