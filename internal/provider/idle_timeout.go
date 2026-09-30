package provider

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

// idleTimeoutReader 给流式响应体加"空闲超时"：连续 d 内读不到任何字节，就主动
// 关闭底层 body，让阻塞中的 Read 以错误返回。
//
// 为什么需要：本地桥实例僵死时（进程还在、端口在监听，但请求处理不了），
// http.Client 只能兜住建连与响应头（Transport.ResponseHeaderTimeout=120s）；
// 一旦响应头已经返回、后续 SSE 却不再出数据，就没有任何超时能救。
// 2026-10-01 00:32 实证：zcode 请求因此挂了 7 分钟不返回（requests.jsonl 无
// 记录、客户端无回复），元凶是 acc-big 实例（18083 端口在听，但 /quota 8s
// 无响应）。加空闲超时后，「永久卡死」降级为一次读错误 → handler 走
// "stream aborted" 分支补发 SSE error 事件，客户端可重试。
//
// 注意这是「空闲」超时而非总超时：长时间思考只要仍在持续吐字就不会被打断，
// 只有真的静默 d 才断。
type idleTimeoutReader struct {
	rc    io.ReadCloser
	d     time.Duration
	timer *time.Timer
	fired atomic.Bool
}

// IdleTimeout 用空闲超时包装流式响应体；rc 为 nil 或 d <= 0 时原样返回（不启用）。
func IdleTimeout(rc io.ReadCloser, d time.Duration) io.ReadCloser {
	if rc == nil || d <= 0 {
		return rc
	}
	r := &idleTimeoutReader{rc: rc, d: d}
	r.timer = time.AfterFunc(d, func() {
		r.fired.Store(true)
		_ = r.rc.Close() // 关闭底层连接，唤醒阻塞中的 Read
	})
	return r
}

func (r *idleTimeoutReader) Read(p []byte) (int, error) {
	n, err := r.rc.Read(p)
	if n > 0 {
		r.timer.Reset(r.d) // 读到数据即续期
		return n, err
	}
	if r.fired.Load() {
		return 0, fmt.Errorf("stream idle %s: 上游静默无数据（本地桥实例可能已卡死）", r.d)
	}
	return n, err
}

func (r *idleTimeoutReader) Close() error {
	r.timer.Stop()
	return r.rc.Close()
}
