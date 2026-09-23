// Package limiter 本地小时窗限速。
// 借 OmniRoute QUOTA_SHARE 的 2-bucket 滑动窗口计数器（免定时器、~99% 精度）
// 与 GLM_proxy 的 DEFAULT_BUDGET_REQUESTS_PER_HOUR=300 阈值：每 (渠道, 账号)
// 一个小时窗，防单号请求过密触发上游风控。
package limiter

import (
	"sync"
	"time"
)

const window = time.Hour

// key 限速维度：渠道 + 账号。
type key struct {
	kind string
	uid  string
}

// bucket 2-bucket 滑窗：currStart 为当前对齐窗口序号（UnixNano/window），
// prev/curr 为上一/当前窗口计数。有效计数 = prev*weight + curr，
// weight = 1 - elapsed/window，随时间线性衰减上一窗口的余量。
type bucket struct {
	currStart int64
	prev      int64
	curr      int64
}

func (b *bucket) roll(nowNanos int64) {
	cur := nowNanos / int64(window)
	if b.currStart == 0 {
		b.currStart = cur
		return
	}
	switch cur - b.currStart {
	case 0:
	case 1:
		b.prev = b.curr
		b.curr = 0
		b.currStart = cur
	default:
		b.prev = 0
		b.curr = 0
		b.currStart = cur
	}
}

func (b *bucket) effective(nowNanos int64) int64 {
	if b.currStart == 0 {
		return 0
	}
	elapsed := nowNanos - b.currStart*int64(window)
	if elapsed < 0 {
		elapsed = 0
	}
	weight := 1 - float64(elapsed)/float64(int64(window))
	if weight < 0 {
		weight = 0
	}
	return int64(float64(b.prev)*weight) + b.curr
}

// Limiter 每 (渠道, 账号) 一个滑动窗口。Allow 只读判断、Hit 实际发请求后
// 计数，两者间无原子性——限速目的是保护账号而非精确计量，精度损失可接受。
type Limiter struct {
	mu    sync.Mutex
	limit int64
	m     map[key]*bucket
	now   func() time.Time
}

// New 构建限速器；limit<=0 视为 300（GLM_proxy 同款）。
func New(limit int) *Limiter {
	if limit <= 0 {
		limit = 300
	}
	return &Limiter{limit: int64(limit), m: map[key]*bucket{}, now: time.Now}
}

// Allow 判断该账号本小时窗是否还可发请求（只读，不计数）。
// fail-open：内部任何异常都放行，限速器故障不阻塞正常流量。
func (l *Limiter) Allow(kind, uid string) (allowed bool) {
	defer func() {
		if recover() != nil {
			allowed = true
		}
	}()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.m[key{kind, uid}]
	if !ok {
		return true
	}
	t := l.now()
	n := t.UnixNano()
	b.roll(n)
	return b.effective(n) < l.limit
}

// Hit 记一次请求。
func (l *Limiter) Hit(kind, uid string) {
	defer func() { recover() }()
	l.mu.Lock()
	defer l.mu.Unlock()
	k := key{kind, uid}
	b := l.m[k]
	if b == nil {
		b = &bucket{}
		l.m[k] = b
	}
	n := l.now().UnixNano()
	b.roll(n)
	b.curr++
}

// RetryAfter 估算到当前小时窗重置的剩余时间（客户端 429 的 Retry-After 头）。
func (l *Limiter) RetryAfter(kind, uid string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.now().UnixNano()
	cur := n / int64(window)
	d := time.Duration((cur+1)*int64(window) - n)
	if d < 0 {
		return 0
	}
	return d
}
