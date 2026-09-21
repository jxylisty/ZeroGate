package audit

import (
	"path/filepath"
	"testing"
	"time"
)

func newTestEntry(t time.Time, channel, uid, model, client string, total int64, status int) Entry {
	return Entry{
		Time:        t.Format(time.RFC3339),
		Channel:     channel,
		UID:         uid,
		ModelReq:    model,
		Client:      client,
		TotalTokens: total,
		Status:      status,
	}
}

// 核心回归：统计必须基于按日聚合落盘，重启后 SummaryToday / Analytics 不丢数据，
// 且不受内存流水窗口（maxMem）裁剪影响。
func TestStatsSurviveRestartBeyondMemoryWindow(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "requests.jsonl")

	r1 := New(fp, 5) // 内存窗口只有 5 条，制造远超窗口的流量
	now := time.Now()
	const n = 40
	for i := 0; i < n; i++ {
		r1.Record(newTestEntry(now, "zcode", "u1", "model-a", "Cursor", 100, 200))
	}
	r1.Record(newTestEntry(now, "zcode", "u1", "model-a", "Cursor", 10, 502)) // 失败 1 条
	r1.Close()

	if req, tok, fail := r1.SummaryToday(); req != n+1 || tok != (n*100+10) || fail != 1 {
		t.Fatalf("关闭前统计不符: req=%d tok=%d fail=%d", req, tok, fail)
	}

	// 重启：新 Recorder 内存窗口依然是 5，但统计应来自 audit_daily.json
	r2 := New(fp, 5)
	defer r2.Close()
	req, tok, fail := r2.SummaryToday()
	if req != n+1 || tok != n*100+10 || fail != 1 {
		t.Fatalf("重启后今日统计丢失: req=%d tok=%d fail=%d", req, tok, fail)
	}

	a := r2.Analytics()
	if a.TotalRequests != n+1 || a.TotalTokens != n*100+10 {
		t.Fatalf("重启后累计统计不符: req=%d tok=%d", a.TotalRequests, a.TotalTokens)
	}
	if len(a.TopModels) != 1 || a.TopModels[0].Model != "model-a" || a.TopModels[0].ReqCount != n+1 {
		t.Fatalf("模型榜不符: %+v", a.TopModels)
	}
	if len(a.TopAccounts) != 1 || a.TopAccounts[0].UID != "u1" || a.TopAccounts[0].Channel != "zcode" {
		t.Fatalf("账号榜不符: %+v", a.TopAccounts)
	}
	if len(a.TopClients) != 1 || a.TopClients[0].Client != "Cursor" || a.TopClients[0].ReqCount != n+1 {
		t.Fatalf("客户端榜不符: %+v", a.TopClients)
	}
	if a.ChannelTokens["zcode"] != n*100+10 {
		t.Fatalf("渠道 Token 不符: %d", a.ChannelTokens["zcode"])
	}
	if len(a.DailyTrend) != 1 || a.DailyTrend[0].Day != now.Format("2006-01-02") || a.DailyTrend[0].Total != n*100+10 {
		t.Fatalf("日趋势不符: %+v", a.DailyTrend)
	}
}

// 多维度聚合拆分校验：Token 按模型/客户端/账号正确归集。
func TestAnalyticsAggregatesMultipleDims(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "requests.jsonl")
	r := New(fp, 100)
	defer r.Close()
	now := time.Now()

	r.Record(newTestEntry(now, "traework", "acc1", "m1", "Cursor", 300, 200))
	r.Record(newTestEntry(now, "traework", "acc2", "m2", "VSCode", 200, 200))
	r.Record(newTestEntry(now, "zcode", "acc1", "m1", "Cursor", 100, 200))

	a := r.Analytics()
	if a.TotalRequests != 3 || a.TotalTokens != 600 {
		t.Fatalf("总量不符: req=%d tok=%d", a.TotalRequests, a.TotalTokens)
	}
	models := map[string]int64{}
	for _, m := range a.TopModels {
		models[m.Model] = m.TotalTokens
	}
	if models["m1"] != 400 || models["m2"] != 200 {
		t.Fatalf("模型归集不符: %v", models)
	}
	clients := map[string]int64{}
	for _, c := range a.TopClients {
		clients[c.Client] = c.TotalTokens
	}
	if clients["Cursor"] != 400 || clients["VSCode"] != 200 {
		t.Fatalf("客户端归集不符: %v", clients)
	}
	accts := map[string]int64{}
	for _, ac := range a.TopAccounts {
		accts[ac.Channel+"/"+ac.UID] = ac.TotalTokens
	}
	if accts["traework/acc1"] != 300 || accts["traework/acc2"] != 200 || accts["zcode/acc1"] != 100 {
		t.Fatalf("账号归集不符: %v", accts)
	}
}

// 空记录器不应 panic，统计返回零值。
func TestEmptyRecorderZeroStats(t *testing.T) {
	r := New(filepath.Join(t.TempDir(), "requests.jsonl"), 10)
	defer r.Close()
	if req, tok, fail := r.SummaryToday(); req != 0 || tok != 0 || fail != 0 {
		t.Fatalf("空记录器统计应为零: %d %d %d", req, tok, fail)
	}
	a := r.Analytics()
	if a.TotalRequests != 0 || a.ChannelTokens == nil {
		t.Fatalf("空 Analytics 不符: %+v", a)
	}
}
