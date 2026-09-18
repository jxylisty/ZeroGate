// Package audit 提供轻量级请求调用流水审计日志记录，
// 内存环形缓存最近请求供 Web 快速查看，后台异步写入 JSONL 保持零延迟。
package audit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Entry 单次请求审计条目。
type Entry struct {
	ID               string `json:"id"`
	Time             string `json:"time"`               // RFC3339
	Client           string `json:"client"`             // 客户端名称（如 Cursor、Default）
	KeySnippet       string `json:"key_snippet"`        // 掩码密钥（如 sk-zg-...1234）
	ModelReq         string `json:"model_req"`          // 客户端请求模型名
	ModelTarget      string `json:"model_target"`       // 上游实际使用的模型名
	Channel          string `json:"channel"`            // 命中渠道
	UID              string `json:"uid"`                // 命中账号 UID
	PromptTokens     int64  `json:"prompt_tokens"`      // 输入 Token
	CompletionTokens int64  `json:"completion_tokens"`  // 输出 Token
	CacheTokens      int64  `json:"cache_tokens"`       // 缓存命中 Token
	TotalTokens      int64  `json:"total_tokens"`       // 总 Token
	DurationMs       int64  `json:"duration_ms"`        // 耗时毫秒
	Status           int    `json:"status"`             // HTTP 状态码
	Error            string `json:"error,omitempty"`    // 错误信息摘要
	Stream           bool   `json:"stream"`             // 是否流式
}

// Recorder 审计流水记录器。
type Recorder struct {
	mu      sync.RWMutex
	fp      string
	maxMem  int
	entries []Entry
	in      chan Entry
	closed  chan struct{}
}

// New 创建审计记录器。
func New(fp string, maxMem int) *Recorder {
	if maxMem <= 0 {
		maxMem = 500
	}
	r := &Recorder{
		fp:      fp,
		maxMem:  maxMem,
		entries: make([]Entry, 0, maxMem),
		in:      make(chan Entry, 256),
		closed:  make(chan struct{}),
	}
	r.loadRecent()
	go r.writer()
	return r
}

// Close 优雅关闭写入协程。
func (r *Recorder) Close() {
	close(r.closed)
}

func (r *Recorder) loadRecent() {
	if r.fp == "" {
		return
	}
	raw, err := os.ReadFile(r.fp)
	if err != nil {
		return
	}
	lines := splitLines(raw)
	// 只加载最后 maxMem 行
	start := 0
	if len(lines) > r.maxMem {
		start = len(lines) - r.maxMem
	}
	for i := start; i < len(lines); i++ {
		line := lines[i]
		if len(line) == 0 {
			continue
		}
		var e Entry
		if err := json.Unmarshal(line, &e); err == nil {
			r.entries = append(r.entries, e)
		}
	}
}

func splitLines(data []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, b := range data {
		if b == '\n' {
			out = append(out, data[start:i])
			start = i + 1
		}
	}
	if start < len(data) {
		out = append(out, data[start:])
	}
	return out
}

func (r *Recorder) writer() {
	var f *os.File
	if r.fp != "" {
		_ = os.MkdirAll(filepath.Dir(r.fp), 0o755)
		f, _ = os.OpenFile(r.fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	}
	defer func() {
		if f != nil {
			_ = f.Close()
		}
	}()

	for {
		select {
		case <-r.closed:
			return
		case e := <-r.in:
			if f != nil {
				data, err := json.Marshal(e)
				if err == nil {
					data = append(data, '\n')
					_, _ = f.Write(data)
				}
			}
		}
	}
}

// Record 记录一条审计日志。
func (r *Recorder) Record(e Entry) {
	if e.ID == "" {
		b := make([]byte, 4)
		_, _ = rand.Read(b)
		e.ID = "req_" + hex.EncodeToString(b)
	}
	if e.Time == "" {
		e.Time = time.Now().Format(time.RFC3339)
	}
	if e.TotalTokens == 0 {
		e.TotalTokens = e.PromptTokens + e.CompletionTokens
	}

	r.mu.Lock()
	r.entries = append(r.entries, e)
	if len(r.entries) > r.maxMem {
		r.entries = r.entries[len(r.entries)-r.maxMem:]
	}
	r.mu.Unlock()

	select {
	case r.in <- e:
	default:
		// 通道已满时丢弃文件异步写入，保障服务主线程零阻塞
	}
}

// Recent 返回按时间倒序排好的最新审计记录。
func (r *Recorder) Recent(limit int) []Entry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	n := len(r.entries)
	if limit <= 0 || limit > n {
		limit = n
	}
	out := make([]Entry, limit)
	for i := 0; i < limit; i++ {
		out[i] = r.entries[n-1-i]
	}
	return out
}

// SummaryToday 统计今日调用指标：请求数、总Token数、失败数。
func (r *Recorder) SummaryToday() (reqCount int64, tokenSum int64, failCount int64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	today := time.Now().Format("2006-01-02")
	for _, e := range r.entries {
		if len(e.Time) >= 10 && e.Time[:10] == today {
			reqCount++
			tokenSum += e.TotalTokens
			if e.Status >= 400 {
				failCount++
			}
		}
	}
	return reqCount, tokenSum, failCount
}

// ModelTokenStat 单个模型的用量统计
type ModelTokenStat struct {
	Model       string `json:"model"`
	TotalTokens int64  `json:"total_tokens"`
	ReqCount    int64  `json:"req_count"`
}

// ClientTokenStat 单个客户端的用量统计
type ClientTokenStat struct {
	Client      string `json:"client"`
	TotalTokens int64  `json:"total_tokens"`
	ReqCount    int64  `json:"req_count"`
}

// AccountTokenStat 单个账号的用量统计
type AccountTokenStat struct {
	UID         string `json:"uid"`
	Channel     string `json:"channel"`
	TotalTokens int64  `json:"total_tokens"`
	ReqCount    int64  `json:"req_count"`
}

// DailyTokenStat 单日 Token 统计
type DailyTokenStat struct {
	Day        string `json:"day"`
	Prompt     int64  `json:"prompt"`
	Completion int64  `json:"completion"`
	Cache      int64  `json:"cache"`
	Total      int64  `json:"total"`
	ReqCount   int64  `json:"req_count"`
}

// Analytics 聚合分析结构
type Analytics struct {
	TotalRequests int64              `json:"total_requests"`
	TotalTokens   int64              `json:"total_tokens"`
	PromptTokens  int64              `json:"prompt_tokens"`
	CompTokens    int64              `json:"completion_tokens"`
	CacheTokens   int64              `json:"cache_tokens"`
	TopModels     []ModelTokenStat   `json:"top_models"`
	TopClients    []ClientTokenStat  `json:"top_clients"`
	TopAccounts   []AccountTokenStat `json:"top_accounts"`
	ChannelTokens map[string]int64   `json:"channel_tokens"`
	DailyTrend    []DailyTokenStat   `json:"daily_trend"`
}

// Analytics 返回基于审计记录的多维 Token 分析
func (r *Recorder) Analytics() Analytics {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := Analytics{
		ChannelTokens: make(map[string]int64),
	}

	modelMap := make(map[string]*ModelTokenStat)
	clientMap := make(map[string]*ClientTokenStat)
	accountMap := make(map[string]*AccountTokenStat)
	dailyMap := make(map[string]*DailyTokenStat)

	for _, e := range r.entries {
		res.TotalRequests++
		res.TotalTokens += e.TotalTokens
		res.PromptTokens += e.PromptTokens
		res.CompTokens += e.CompletionTokens
		res.CacheTokens += e.CacheTokens

		if e.Channel != "" {
			res.ChannelTokens[e.Channel] += e.TotalTokens
		}

		if e.UID != "" {
			acctKey := e.Channel + "/" + e.UID
			aSt := accountMap[acctKey]
			if aSt == nil {
				aSt = &AccountTokenStat{UID: e.UID, Channel: e.Channel}
				accountMap[acctKey] = aSt
			}
			aSt.TotalTokens += e.TotalTokens
			aSt.ReqCount++
		}

		mName := e.ModelReq
		if mName == "" {
			mName = e.ModelTarget
		}
		if mName != "" {
			st := modelMap[mName]
			if st == nil {
				st = &ModelTokenStat{Model: mName}
				modelMap[mName] = st
			}
			st.TotalTokens += e.TotalTokens
			st.ReqCount++
		}

		cName := e.Client
		if cName == "" {
			cName = "Default"
		}
		cSt := clientMap[cName]
		if cSt == nil {
			cSt = &ClientTokenStat{Client: cName}
			clientMap[cName] = cSt
		}
		cSt.TotalTokens += e.TotalTokens
		cSt.ReqCount++

		if len(e.Time) >= 10 {
			day := e.Time[:10]
			dSt := dailyMap[day]
			if dSt == nil {
				dSt = &DailyTokenStat{Day: day}
				dailyMap[day] = dSt
			}
			dSt.Prompt += e.PromptTokens
			dSt.Completion += e.CompletionTokens
			dSt.Cache += e.CacheTokens
			dSt.Total += e.TotalTokens
			dSt.ReqCount++
		}
	}

	for _, m := range modelMap {
		res.TopModels = append(res.TopModels, *m)
	}
	sort.Slice(res.TopModels, func(i, j int) bool {
		return res.TopModels[i].TotalTokens > res.TopModels[j].TotalTokens
	})
	if len(res.TopModels) > 10 {
		res.TopModels = res.TopModels[:10]
	}

	for _, c := range clientMap {
		res.TopClients = append(res.TopClients, *c)
	}
	sort.Slice(res.TopClients, func(i, j int) bool {
		return res.TopClients[i].TotalTokens > res.TopClients[j].TotalTokens
	})

	for _, a := range accountMap {
		res.TopAccounts = append(res.TopAccounts, *a)
	}
	sort.Slice(res.TopAccounts, func(i, j int) bool {
		return res.TopAccounts[i].TotalTokens > res.TopAccounts[j].TotalTokens
	})
	if len(res.TopAccounts) > 15 {
		res.TopAccounts = res.TopAccounts[:15]
	}

	for _, d := range dailyMap {
		res.DailyTrend = append(res.DailyTrend, *d)
	}
	sort.Slice(res.DailyTrend, func(i, j int) bool {
		return res.DailyTrend[i].Day < res.DailyTrend[j].Day
	})

	return res
}
