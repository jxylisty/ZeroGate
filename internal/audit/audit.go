// Package audit 提供轻量级请求调用流水审计日志记录，
// 内存环形缓存最近请求供 Web 快速查看，后台异步写入 JSONL 保持零延迟。
// 统计口径（今日汇总/趋势/TOP 榜）基于按日聚合持久化 audit_daily.json，
// 不受内存流水窗口裁剪影响，重启不丢。
package audit

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Entry 单次请求审计条目。
type Entry struct {
	ID               string `json:"id"`
	Time             string `json:"time"`                // RFC3339
	Client           string `json:"client"`              // 客户端名称（如 Cursor、Default）
	KeySnippet       string `json:"key_snippet"`         // 掩码密钥（如 sk-zg-...1234）
	ModelReq         string `json:"model_req"`           // 客户端请求模型名
	ModelTarget      string `json:"model_target"`        // 上游实际使用的模型名
	Channel          string `json:"channel"`             // 命中渠道
	UID              string `json:"uid"`                 // 命中账号 UID
	PromptTokens     int64  `json:"prompt_tokens"`       // 输入 Token
	CompletionTokens int64  `json:"completion_tokens"`   // 输出 Token
	CacheTokens      int64  `json:"cache_tokens"`        // 缓存命中 Token
	TotalTokens      int64   `json:"total_tokens"`        // 总 Token
	Credits          float64 `json:"credits,omitempty"`   // 积分/额度消耗（针对某些渠道）
	DurationMs       int64   `json:"duration_ms"`         // 耗时毫秒
	TTFTMs           int64  `json:"ttft_ms,omitempty"`   // 首字延迟毫秒（流式=首帧到达；非流式=响应头就绪）
	Status           int    `json:"status"`              // HTTP 状态码
	Error            string `json:"error,omitempty"`     // 错误信息摘要
	Stream           bool   `json:"stream"`              // 是否流式
	UsageRaw         string `json:"usage_raw,omitempty"` // 流式上游 usage 原文（缓存字段取证用）
}

// Recorder 审计流水记录器。
type Recorder struct {
	mu         sync.RWMutex
	fp         string
	maxMem     int
	entries    []Entry
	in         chan Entry
	closed     chan struct{}
	writerDone chan struct{}

	dailyFP    string             // audit_daily.json：按日聚合，统计接口的事实源
	daily      map[string]*DayAgg // day → 聚合
	dailyDirty bool
	closedOnce sync.Once
	saveMu     sync.Mutex
}

// New 创建审计记录器。fp 为调用流水 JSONL 路径，同目录派生 audit_daily.json。
func New(fp string, maxMem int) *Recorder {
	if maxMem <= 0 {
		maxMem = 500
	}
	r := &Recorder{
		fp:         fp,
		maxMem:     maxMem,
		entries:    make([]Entry, 0, maxMem),
		in:         make(chan Entry, 256),
		closed:     make(chan struct{}),
		writerDone: make(chan struct{}),
		daily:      make(map[string]*DayAgg),
	}
	if fp != "" {
		r.dailyFP = filepath.Join(filepath.Dir(fp), "audit_daily.json")
	}
	r.loadDaily()
	r.initFromDisk()
	go r.writer()
	go r.flusher()
	return r
}

// Close 优雅关闭写入协程，并同步落盘最后一次按日聚合。
func (r *Recorder) Close() {
	r.closedOnce.Do(func() { close(r.closed) })
	<-r.writerDone
	r.saveDaily()
}

// initFromDisk 单次读取流水文件，同时完成内存窗口载入与维度回填（避免二次读盘膨胀内存）。
func (r *Recorder) initFromDisk() {
	if r.fp == "" {
		return
	}
	raw, err := os.ReadFile(r.fp)
	if err != nil || len(raw) == 0 {
		return
	}
	lines := splitLines(raw)

	// 1. 最近流水载入内存（只取最后 maxMem 行）
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

	// 2. 检查是否有缺失维度的日聚合需要回填
	filled := 0
	r.mu.Lock()
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		var e Entry
		if json.Unmarshal(line, &e) != nil || len(e.Time) < 10 {
			continue
		}
		agg := r.daily[e.Time[:10]]
		if agg == nil {
			continue
		}
		if n := agg.fillDims(&e); n > 0 {
			filled += n
			r.dailyDirty = true
		}
	}
	r.mu.Unlock()
	if filled > 0 {
		log.Printf("[audit] 已从流水回填 %d 条维度明细到日聚合", filled)
		go r.saveDaily()
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

// DimStat 维度统计：Token 总量 + 请求次数。
type DimStat struct {
	Tokens   int64 `json:"tokens"`
	ReqCount int64 `json:"req_count"`
}

// DayAgg 单日聚合：统计接口的事实源，跨重启持久化到 audit_daily.json。
type DayAgg struct {
	Day         string `json:"day"`
	ReqCount    int64  `json:"req_count"`
	FailCount   int64  `json:"fail_count"`
	TotalTokens int64  `json:"total_tokens"`
	Prompt      int64  `json:"prompt"`
	Completion  int64  `json:"completion"`
	Cache       int64  `json:"cache"`
	DurationSum int64  `json:"duration_sum"`

	Channels map[string]DimStat `json:"channels,omitempty"`
	Models   map[string]DimStat `json:"models,omitempty"` // 键 = 请求模型名
	Clients  map[string]DimStat `json:"clients,omitempty"`
	Accounts map[string]DimStat `json:"accounts,omitempty"` // 键 = channel + "/" + uid
}

// newDayAgg 创建指定日期的空聚合。
func newDayAgg(day string) *DayAgg {
	return &DayAgg{
		Day:      day,
		Channels: make(map[string]DimStat),
		Models:   make(map[string]DimStat),
		Clients:  make(map[string]DimStat),
		Accounts: make(map[string]DimStat),
	}
}

// absorb 把一条流水并入日聚合。
func (a *DayAgg) absorb(e *Entry) {
	a.ReqCount++
	a.TotalTokens += e.TotalTokens
	a.Prompt += e.PromptTokens
	a.Completion += e.CompletionTokens
	a.Cache += e.CacheTokens
	a.DurationSum += e.DurationMs
	if e.Status >= 400 {
		a.FailCount++
	}
	if e.Channel != "" {
		if a.Channels == nil {
			a.Channels = make(map[string]DimStat)
		}
		c := a.Channels[e.Channel]
		c.Tokens += e.TotalTokens
		c.ReqCount++
		a.Channels[e.Channel] = c
	}
	mName := e.ModelReq
	if mName == "" {
		mName = e.ModelTarget
	}
	if mName != "" {
		if a.Models == nil {
			a.Models = make(map[string]DimStat)
		}
		m := a.Models[mName]
		m.Tokens += e.TotalTokens
		m.ReqCount++
		a.Models[mName] = m
	}
	cName := e.Client
	if cName == "" {
		cName = "Default"
	}
	if a.Clients == nil {
		a.Clients = make(map[string]DimStat)
	}
	cl := a.Clients[cName]
	cl.Tokens += e.TotalTokens
	cl.ReqCount++
	a.Clients[cName] = cl
	if e.UID != "" {
		if a.Accounts == nil {
			a.Accounts = make(map[string]DimStat)
		}
		key := e.Channel + "/" + e.UID
		ac := a.Accounts[key]
		ac.Tokens += e.TotalTokens
		ac.ReqCount++
		a.Accounts[key] = ac
	}
}

// fillDims 幂等回填：把一条流水并入维度 map，只增缺失键，已有键不动（返回补上的键数）。
// 标量字段（req_count/total_tokens 等）不在此处理——它们由 absorb 全量维护。
func (a *DayAgg) fillDims(e *Entry) int {
	n := 0
	if e.Channel != "" {
		if a.Channels == nil {
			a.Channels = make(map[string]DimStat)
		}
		if _, ok := a.Channels[e.Channel]; !ok {
			a.Channels[e.Channel] = DimStat{Tokens: e.TotalTokens, ReqCount: 1}
			n++
		}
	}
	mName := e.ModelReq
	if mName == "" {
		mName = e.ModelTarget
	}
	if mName != "" {
		if a.Models == nil {
			a.Models = make(map[string]DimStat)
		}
		if _, ok := a.Models[mName]; !ok {
			a.Models[mName] = DimStat{Tokens: e.TotalTokens, ReqCount: 1}
			n++
		}
	}
	cName := e.Client
	if cName == "" {
		cName = "Default"
	}
	if a.Clients == nil {
		a.Clients = make(map[string]DimStat)
	}
	if _, ok := a.Clients[cName]; !ok {
		a.Clients[cName] = DimStat{Tokens: e.TotalTokens, ReqCount: 1}
		n++
	}
	if e.UID != "" {
		if a.Accounts == nil {
			a.Accounts = make(map[string]DimStat)
		}
		key := e.Channel + "/" + e.UID
		if _, ok := a.Accounts[key]; !ok {
			a.Accounts[key] = DimStat{Tokens: e.TotalTokens, ReqCount: 1}
			n++
		}
	}
	return n
}

// recordDailyLocked 把条目并入日聚合（调用方须持有 r.mu）。
func (r *Recorder) recordDailyLocked(e *Entry) {
	if len(e.Time) < 10 {
		return
	}
	day := e.Time[:10]
	agg := r.daily[day]
	if agg == nil {
		agg = newDayAgg(day)
		r.daily[day] = agg
	}
	agg.absorb(e)
	r.dailyDirty = true
}

// loadDaily 启动时恢复历史日聚合。
func (r *Recorder) loadDaily() {
	if r.dailyFP == "" {
		return
	}
	raw, err := os.ReadFile(r.dailyFP)
	if err != nil {
		return
	}
	var list []*DayAgg
	if err := json.Unmarshal(raw, &list); err != nil {
		log.Printf("[audit] 日聚合文件解析失败，将重建: %v", err)
		return
	}
	for _, a := range list {
		if a == nil || a.Day == "" {
			continue
		}
		if a.Channels == nil {
			a.Channels = make(map[string]DimStat)
		}
		if a.Models == nil {
			a.Models = make(map[string]DimStat)
		}
		if a.Clients == nil {
			a.Clients = make(map[string]DimStat)
		}
		if a.Accounts == nil {
			a.Accounts = make(map[string]DimStat)
		}
		r.daily[a.Day] = a
	}
}

// saveDaily 原子落盘日聚合（只保留最近 30 天）。
func (r *Recorder) saveDaily() {
	r.mu.RLock()
	dirty := r.dailyDirty
	var list []*DayAgg
	if dirty {
		for _, a := range r.daily {
			list = append(list, a)
		}
	}
	r.mu.RUnlock()
	if !dirty {
		return
	}

	sort.Slice(list, func(i, j int) bool { return list[i].Day > list[j].Day })
	if len(list) > 30 {
		list = list[:30]
	}

	r.saveMu.Lock()
	defer r.saveMu.Unlock()
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		log.Printf("[audit] 日聚合序列化失败: %v", err)
		return
	}
	tmp := r.dailyFP + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		log.Printf("[audit] 日聚合写入失败: %v", err)
		return
	}
	if err := os.Rename(tmp, r.dailyFP); err != nil {
		log.Printf("[audit] 日聚合替换失败: %v", err)
		return
	}
	r.mu.Lock()
	r.dailyDirty = false
	r.mu.Unlock()
}

// flusher 周期性落盘日聚合，进程崩溃最多丢 30 秒统计。
func (r *Recorder) flusher() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-r.closed:
			return
		case <-t.C:
			r.saveDaily()
		}
	}
}

const maxAuditFileSize = 20 * 1024 * 1024 // 20MB 单文件上限

func (r *Recorder) writer() {
	var f *os.File
	var currentSize int64

	openFile := func() {
		if r.fp == "" {
			return
		}
		_ = os.MkdirAll(filepath.Dir(r.fp), 0o755)
		var err error
		f, err = os.OpenFile(r.fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			if fi, err := f.Stat(); err == nil {
				currentSize = fi.Size()
			}
		}
	}
	openFile()
	defer func() {
		if f != nil {
			_ = f.Close()
		}
		close(r.writerDone)
	}()

	writeEntry := func(e Entry) {
		if f == nil {
			return
		}
		data, err := json.Marshal(e)
		if err != nil {
			return
		}
		data = append(data, '\n')
		n, err := f.Write(data)
		if err == nil {
			currentSize += int64(n)
			if currentSize >= maxAuditFileSize {
				_ = f.Close()
				f = nil
				bak := r.fp + ".1"
				_ = os.Remove(bak)
				_ = os.Rename(r.fp, bak)
				openFile()
			}
		}
	}

	for {
		select {
		case <-r.closed:
			// 退出时 drain 剩余未落盘的记录，杜绝停机丢日志
			for {
				select {
				case e := <-r.in:
					writeEntry(e)
				default:
					return
				}
			}
		case e := <-r.in:
			writeEntry(e)
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
	r.recordDailyLocked(&e)
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
// 事实源为按日聚合（audit_daily.json），不受内存流水窗口裁剪影响。
func (r *Recorder) SummaryToday() (reqCount int64, tokenSum int64, failCount int64) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	agg := r.daily[time.Now().Format("2006-01-02")]
	if agg == nil {
		return 0, 0, 0
	}
	return agg.ReqCount, agg.TotalTokens, agg.FailCount
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

// Analytics 返回基于日聚合的多维 Token 分析，跨重启累计完整。
func (r *Recorder) Analytics() Analytics {
	r.mu.RLock()
	defer r.mu.RUnlock()

	res := Analytics{
		ChannelTokens: make(map[string]int64),
	}
	modelMap := make(map[string]*ModelTokenStat)
	clientMap := make(map[string]*ClientTokenStat)
	accountMap := make(map[string]*AccountTokenStat)

	for _, agg := range r.daily {
		res.TotalRequests += agg.ReqCount
		res.TotalTokens += agg.TotalTokens
		res.PromptTokens += agg.Prompt
		res.CompTokens += agg.Completion
		res.CacheTokens += agg.Cache

		for ch, ds := range agg.Channels {
			res.ChannelTokens[ch] += ds.Tokens
		}
		for key, ds := range agg.Accounts {
			idx := strings.Index(key, "/")
			if idx < 0 {
				continue
			}
			acctKey := key
			aSt := accountMap[acctKey]
			if aSt == nil {
				aSt = &AccountTokenStat{UID: key[idx+1:], Channel: key[:idx]}
				accountMap[acctKey] = aSt
			}
			aSt.TotalTokens += ds.Tokens
			aSt.ReqCount += ds.ReqCount
		}
		for name, ds := range agg.Models {
			st := modelMap[name]
			if st == nil {
				st = &ModelTokenStat{Model: name}
				modelMap[name] = st
			}
			st.TotalTokens += ds.Tokens
			st.ReqCount += ds.ReqCount
		}
		for name, ds := range agg.Clients {
			st := clientMap[name]
			if st == nil {
				st = &ClientTokenStat{Client: name}
				clientMap[name] = st
			}
			st.TotalTokens += ds.Tokens
			st.ReqCount += ds.ReqCount
		}

		res.DailyTrend = append(res.DailyTrend, DailyTokenStat{
			Day:        agg.Day,
			Prompt:     agg.Prompt,
			Completion: agg.Completion,
			Cache:      agg.Cache,
			Total:      agg.TotalTokens,
			ReqCount:   agg.ReqCount,
		})
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

	sort.Slice(res.DailyTrend, func(i, j int) bool {
		return res.DailyTrend[i].Day < res.DailyTrend[j].Day
	})

	return res
}
