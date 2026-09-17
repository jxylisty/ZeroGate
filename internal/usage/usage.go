// Package usage 用量记录器：周期拉取各账号实时余额并落盘 CSV + 内存快照，
// 供 /api/usage 汇报「剩余 / 总量 / 已用 / 本期消耗 / 签到状态」。
package usage

import (
	"encoding/csv"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Snapshot 单次余额快照。
type Snapshot struct {
	TS     time.Time `json:"ts"`
	Remain int64     `json:"remain"`
	Total  int64     `json:"total"`
}

// DailyUsage 单日净消耗（正=消耗，负=签到入账）。
type DailyUsage struct {
	Day    string `json:"day"`    // YYYY-MM-DD
	Delta  int64  `json:"delta"`  // 当日净消耗
	Remain int64  `json:"remain"` // 当日最后余额
}

// TokenDay 单日 Token 用量（反代转发时从上游 usage 字段统计）。
type TokenDay struct {
	Day        string `json:"day"`    // YYYY-MM-DD
	Prompt     int64  `json:"prompt"` // 输入
	Completion int64  `json:"completion"`
	Total      int64  `json:"total"`
	CacheRead  int64  `json:"cache_read"`  // 缓存命中输入量
	CacheWrite int64  `json:"cache_write"` // 缓存写入量
}

// AccountUsage 单账号用量汇总（对外展示）。
type AccountUsage struct {
	UID            string       `json:"uid"`
	Kind           string       `json:"kind"`
	Nickname       string       `json:"nickname"`
	Remain         int64        `json:"remain"`
	Total          int64        `json:"total"`
	ExpireAt       int64        `json:"expire_at,omitempty"` // 最早到期套餐时间（Unix 秒）；0=长期有效
	Used           int64        `json:"used"`                // 最新快照的 总量-剩余
	Delta          int64        `json:"delta"`               // 与上一快照差值（正=消耗，负=入账签到）
	Today          int64        `json:"today"`               // 今日净消耗（正=消耗，负=入账签到）
	Daily          []DailyUsage `json:"daily"`               // 按天聚合（近 maxDaily 天）
	Tokens         []TokenDay   `json:"tokens,omitempty"`    // 按天 Token 用量（升序）
	LastCheckinOK  bool         `json:"last_checkin_ok"`
	LastCheckinAt  string       `json:"last_checkin_at"`
	LastCheckinMsg string       `json:"last_checkin_msg"`
	Snapshots      []Snapshot   `json:"snapshots"` // 近 N 条
}

const (
	maxSnapshots = 672 // 30 分钟一条 ≈ 14 天，趋势图数据基础
	maxDaily     = 14  // 每日聚合最多输出天数
)

// Recorder 用量记录器。
type Recorder struct {
	mu      sync.Mutex
	fp      string // usage.csv 路径（可空 = 不落盘）
	tokenFP string // tokens.csv 路径（可空 = 不记录 token）
	byUID   map[string]*account
}

type account struct {
	UID, Kind, Nickname string
	ExpireAt            int64
	Snapshots           []Snapshot
	Tokens              map[string]*TokenDay // 按天
	LastCheckinOK       bool
	LastCheckinAt       string
	LastCheckinMsg      string
}

// New 构建记录器；fp 非空时加载历史 CSV。
func New(fp string) *Recorder {
	r := &Recorder{fp: fp, byUID: map[string]*account{}}
	if fp != "" {
		r.tokenFP = strings.TrimSuffix(fp, ".csv") + "-tokens.csv"
		r.load()
		r.loadTokens()
	}
	return r
}

// Record 记录一次快照；kind/nickname 首次出现时保留。
func (r *Recorder) Record(kind, uid, nickname string, remain, total int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	acc := r.byUID[uid]
	if acc == nil {
		acc = &account{UID: uid, Kind: kind, Nickname: nickname}
		r.byUID[uid] = acc
	}
	if acc.Kind == "" {
		acc.Kind = kind
	}
	if acc.Nickname == "" {
		acc.Nickname = nickname
	}
	acc.Snapshots = append(acc.Snapshots, Snapshot{TS: time.Now(), Remain: remain, Total: total})
	if len(acc.Snapshots) > maxSnapshots {
		acc.Snapshots = acc.Snapshots[len(acc.Snapshots)-maxSnapshots:]
	}
	if r.fp != "" {
		r.appendCSV(acc, remain, total)
	}
}

// SetCheckin 同步签到状态（供报告展示）。
func (r *Recorder) SetCheckin(uid string, ok bool, at, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if acc := r.byUID[uid]; acc != nil {
		acc.LastCheckinOK = ok
		acc.LastCheckinAt = at
		acc.LastCheckinMsg = msg
	}
}

// SetExpire 同步最早到期套餐时间（expireAt Unix 秒；0=长期有效）。
func (r *Recorder) SetExpire(uid string, expireAt int64) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if acc := r.byUID[uid]; acc != nil {
		acc.ExpireAt = expireAt
	}
}

// RecordTokens 记录一次反代请求的 Token 用量（按天聚合，追加落盘 tokens.csv）。
func (r *Recorder) RecordTokens(kind, uid string, prompt, completion, cacheRead, cacheWrite int64) {
	if prompt < 0 {
		prompt = 0
	}
	if completion < 0 {
		completion = 0
	}
	if cacheRead < 0 {
		cacheRead = 0
	}
	if cacheWrite < 0 {
		cacheWrite = 0
	}
	r.mu.Lock()
	acc := r.byUID[uid]
	if acc == nil {
		acc = &account{UID: uid, Kind: kind}
		r.byUID[uid] = acc
	}
	day := time.Now().Format("2006-01-02")
	if acc.Tokens == nil {
		acc.Tokens = map[string]*TokenDay{}
	}
	t := acc.Tokens[day]
	if t == nil {
		t = &TokenDay{Day: day}
		acc.Tokens[day] = t
	}
	t.Prompt += prompt
	t.Completion += completion
	t.Total += prompt + completion
	t.CacheRead += cacheRead
	t.CacheWrite += cacheWrite
	fp := r.tokenFP
	r.mu.Unlock()

	if fp == "" {
		return
	}
	f, err := os.OpenFile(fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write([]string{day, kind, uid, strconv.FormatInt(prompt, 10), strconv.FormatInt(completion, 10), strconv.FormatInt(cacheRead, 10), strconv.FormatInt(cacheWrite, 10)})
	w.Flush()
}

// loadTokens 从 tokens.csv 恢复按天 Token 统计：day,kind,uid,prompt,completion（可重复行，聚合）。
func (r *Recorder) loadTokens() {
	if r.tokenFP == "" {
		return
	}
	f, err := os.Open(r.tokenFP)
	if err != nil {
		return
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return
	}
	for _, row := range rows {
		if len(row) < 5 {
			continue
		}
		uid := row[2]
		acc := r.byUID[uid]
		if acc == nil {
			acc = &account{UID: uid, Kind: row[1]}
			r.byUID[uid] = acc
		}
		if acc.Tokens == nil {
			acc.Tokens = map[string]*TokenDay{}
		}
		day := row[0]
		t := acc.Tokens[day]
		if t == nil {
			t = &TokenDay{Day: day}
			acc.Tokens[day] = t
		}
		p, _ := strconv.ParseInt(row[3], 10, 64)
			c, _ := strconv.ParseInt(row[4], 10, 64)
			cr, cw := int64(0), int64(0)
			if len(row) >= 6 {
				cr, _ = strconv.ParseInt(row[5], 10, 64)
			}
			if len(row) >= 7 {
				cw, _ = strconv.ParseInt(row[6], 10, 64)
			}
			t.Prompt += p
			t.Completion += c
			t.Total += p + c
			t.CacheRead += cr
			t.CacheWrite += cw
	}
}

// tokensSorted 输出按天升序的 Token 统计（含 today 快捷字段）。
func tokensSorted(tokens map[string]*TokenDay) []TokenDay {
	out := make([]TokenDay, 0, len(tokens))
	for _, t := range tokens {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Day < out[j].Day })
	return out
}

// Report 返回全账号用量汇总（按 UID 排序，稳定输出）。
func (r *Recorder) Report() []AccountUsage {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]AccountUsage, 0, len(r.byUID))
	for _, acc := range r.byUID {
		au := AccountUsage{
			UID: acc.UID, Kind: acc.Kind, Nickname: acc.Nickname,
			ExpireAt:      acc.ExpireAt,
			LastCheckinOK: acc.LastCheckinOK, LastCheckinAt: acc.LastCheckinAt,
			LastCheckinMsg: acc.LastCheckinMsg,
		}
		if n := len(acc.Snapshots); n > 0 {
			last := acc.Snapshots[n-1]
			au.Remain, au.Total = last.Remain, last.Total
			au.Used = last.Total - last.Remain
			if n >= 2 {
				au.Delta = acc.Snapshots[n-2].Remain - last.Remain
			}
			au.Today, au.Daily = dailyUsage(acc.Snapshots)
			if len(acc.Tokens) > 0 {
				au.Tokens = tokensSorted(acc.Tokens)
			}
			au.Snapshots = append([]Snapshot(nil), acc.Snapshots...)
		}
		out = append(out, au)
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].UID < out[j-1].UID; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// JSON 打包成 /api/usage 的响应体。
func (r *Recorder) JSON() map[string]any {
	return map[string]any{
		"accounts": r.Report(),
		"now":      time.Now().Format(time.RFC3339),
	}
}

// dailyUsage 把快照序列按天聚合：每条快照与其前一条的余额差值归属到后一条所在天，
// 正=消耗，负=签到入账。返回今日净消耗与按天升序的每日汇总（最多 maxDaily 天）。
func dailyUsage(snaps []Snapshot) (today int64, daily []DailyUsage) {
	if len(snaps) == 0 {
		return 0, nil
	}
	type dayAcc struct {
		delta, remain int64
	}
	byDay := map[string]*dayAcc{}
	prev := snaps[0].Remain
	for i := 1; i < len(snaps); i++ {
		d := snaps[i].TS.Format("2006-01-02")
		acc := byDay[d]
		if acc == nil {
			acc = &dayAcc{}
			byDay[d] = acc
		}
		acc.delta += prev - snaps[i].Remain
		acc.remain = snaps[i].Remain
		prev = snaps[i].Remain
	}
	// 只有一条快照：无差值可用，仅展示当日最后余额。
	if len(byDay) == 0 {
		d := snaps[len(snaps)-1].TS.Format("2006-01-02")
		return 0, []DailyUsage{{Day: d, Remain: snaps[len(snaps)-1].Remain}}
	}
	for d, acc := range byDay {
		daily = append(daily, DailyUsage{Day: d, Delta: acc.delta, Remain: acc.remain})
	}
	sort.Slice(daily, func(i, j int) bool { return daily[i].Day < daily[j].Day })
	if len(daily) > maxDaily {
		daily = daily[len(daily)-maxDaily:]
	}
	if acc, ok := byDay[time.Now().Format("2006-01-02")]; ok {
		today = acc.delta
	}
	return today, daily
}

// ---------------------------------------------------------------------------
// 持久化
// ---------------------------------------------------------------------------

// load 从 CSV 恢复：ts,kind,uid,nickname,remain,total,checkin_ok,checkin_at,checkin_msg
func (r *Recorder) load() {
	f, err := os.Open(r.fp)
	if err != nil {
		return
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		return
	}
	for _, row := range rows {
		if len(row) < 6 {
			continue
		}
		ts, err := time.Parse(time.RFC3339, row[0])
		if err != nil {
			continue
		}
		uid := row[2]
		acc := r.byUID[uid]
		if acc == nil {
			acc = &account{UID: uid, Kind: row[1], Nickname: row[3]}
			r.byUID[uid] = acc
		}
		remain, _ := strconv.ParseInt(row[4], 10, 64)
		total, _ := strconv.ParseInt(row[5], 10, 64)
		acc.Snapshots = append(acc.Snapshots, Snapshot{TS: ts, Remain: remain, Total: total})
		if len(acc.Snapshots) > maxSnapshots {
			acc.Snapshots = acc.Snapshots[len(acc.Snapshots)-maxSnapshots:]
		}
		if len(row) >= 9 {
			acc.LastCheckinOK = row[6] == "true"
			acc.LastCheckinAt = row[7]
			acc.LastCheckinMsg = row[8]
		}
	}
}

func (r *Recorder) appendCSV(acc *account, remain, total int64) {
	if r.fp == "" {
		return
	}
	f, err := os.OpenFile(r.fp, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	last := acc.Snapshots[len(acc.Snapshots)-1]
	w := csv.NewWriter(f)
	_ = w.Write([]string{
		last.TS.Format(time.RFC3339), acc.Kind, acc.UID, acc.Nickname,
		strconv.FormatInt(remain, 10), strconv.FormatInt(total, 10),
		strconv.FormatBool(acc.LastCheckinOK), acc.LastCheckinAt, acc.LastCheckinMsg,
	})
	w.Flush()
}
