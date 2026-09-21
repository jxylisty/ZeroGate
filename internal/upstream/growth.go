// growth.go WorkBuddy 成长中心自动化：旅行（status→claim→config→depart）、
// 盲盒（chances→draw 循环）、任务（tasks→accept）。
// 协议移植自 smart-open/TraeWorkAssistant（MIT）src-tauri/src/tasks/wb_checkin.rs，
// 凭证即账号自身 access_token，无额外设备信息。
package upstream

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/provider"
)

// GrowthResult 成长中心单次执行结果。
type GrowthResult struct {
	Travel    string         `json:"travel,omitempty"`  // "ok: 领奖 X，已出发" | "skip: 旅行在途" | "fail: ..."
	Lottery   string         `json:"lottery,omitempty"` // "ok: 抽取 N 次（奖励 ...）"
	Tasks     string         `json:"tasks,omitempty"`   // "ok: 领取 N 项" | "skip: 无可领"
	TaskDetails []provider.TaskReward `json:"task_details,omitempty"` // 任务奖励明细
	AuthErr   bool           `json:"auth_err,omitempty"` // 登录态失效，需刷新
}

// growthURL 拼成长中心接口地址。
func (c *Client) growthURL(a *auth.Auth, path string) string {
	return c.billingBase(a) + "/v2/activity/growth/" + path
}

// growthGet 成长中心 GET：返回 (http 状态, body 字节)；响应非 code/data 信封，原样返回。
func (c *Client) growthGet(url string, a *auth.Auth) (int, []byte) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return 0, []byte(err.Error())
	}
	BillingHeaders(req, a)
	resp, err := c.billingClient().Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return resp.StatusCode, raw
}

// growthPost 成长中心 POST（body 可为 nil=空对象）。
func (c *Client) growthPost(url string, a *auth.Auth, body any) (int, []byte) {
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return 0, []byte(err.Error())
	}
	BillingHeaders(req, a)
	resp, err := c.billingClient().Do(req)
	if err != nil {
		return 0, []byte(err.Error())
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	return resp.StatusCode, raw
}

// growthDig 从嵌套 map 按路径取任意值（宽容大小写字段名）。
func growthDig(v any, keys ...string) any {
	cur := v
	for _, k := range keys {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		val, exists := m[k]
		if !exists {
			// 大小写宽容回退
			val = nil
			for kk, vv := range m {
				if equalFold(kk, k) {
					val = vv
					break
				}
			}
		}
		cur = val
	}
	return cur
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := 0; i < len(a); i++ {
		ca, cb := a[i], b[i]
		if 'A' <= ca && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if 'A' <= cb && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}

// growthTruthy 类 Python truthy：非 nil 且非 false/0/""/"false"/"0"/"null"。
func growthTruthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case float64:
		return t != 0
	case json.Number:
		f, _ := t.Float64()
		return f != 0
	case string:
		return t != "" && t != "false" && t != "0" && t != "null" && t != "False"
	case []any:
		return len(t) > 0
	case map[string]any:
		return len(t) > 0
	}
	return true
}

func growthStr(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case float64:
		return fmt.Sprintf("%g", t)
	case json.Number:
		return t.String()
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", t)
	}
}

// growth200 判断 2xx。
func growthOK(status int) bool { return status >= 200 && status <= 201 }

// GrowthTravelSimple 旅行：status 到达 → claim 领奖 → config 取目的地 → depart 出发。
func (c *Client) GrowthTravelSimple(a *auth.Auth) (string, bool) {
	url := c.growthURL(a, "buddy/travel/status")
	status, raw := c.growthGet(url, a)
	if status == http.StatusUnauthorized {
		return "登录态失效（401）", true
	}
	if status < 200 || status >= 300 {
		return fmt.Sprintf("travel/status 不可用（HTTP %d）", status), false
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return "travel/status 响应解析失败", false
	}
	arrived := growthTruthy(growthDig(body, "arrived")) ||
		growthTruthy(growthDig(body, "is_arrived")) ||
		growthTruthy(growthDig(body, "has_arrived"))
	recordID := growthDig(body, "record_id")
	if recordID == nil {
		recordID = growthDig(body, "recordId")
	}
	if !arrived {
		dest := growthStr(growthDig(body, "destination", "name"))
		if dest == "" {
			dest = growthStr(growthDig(body, "target"))
		}
		if dest != "" {
			return "旅行在途（" + dest + "）", false
		}
		return "旅行在途", false
	}
	// 领奖
	claimBody := map[string]any{}
	if r, ok := recordID.(string); ok && r != "" {
		claimBody["record_id"] = r
	}
	st1, raw1 := c.growthPost(c.growthURL(a, "buddy/travel/claim"), a, claimBody)
	if st1 == http.StatusUnauthorized {
		return "登录态失效（401）", true
	}
	claimTxt := ""
	if growthOK(st1) {
		var b1 map[string]any
		if err := json.Unmarshal(raw1, &b1); err == nil {
			rw := growthDig(b1, "reward", "credits")
			if rw == nil {
				rw = growthDig(b1, "reward")
			}
			if rw != nil {
				claimTxt = " " + growthStr(rw)
			}
		}
	} else {
		return fmt.Sprintf("领奖失败（HTTP %d）", st1), false
	}
	// 查目的地（失败不阻塞出发）
	dest := ""
	if _, r2 := c.growthGet(c.growthURL(a, "buddy/travel/config"), a); len(r2) > 0 {
		var b2 map[string]any
		if err := json.Unmarshal(r2, &b2); err == nil {
			dest = growthStr(growthDig(b2, "destination", "name"))
			if dest == "" {
				dest = growthStr(growthDig(b2, "target"))
			}
		}
	}
	departBody := map[string]any{}
	if dest != "" {
		departBody["destination"] = dest
	}
	st3, _ := c.growthPost(c.growthURL(a, "buddy/travel/depart"), a, departBody)
	if growthOK(st3) {
		if dest != "" {
			return "领奖" + claimTxt + "，已出发（" + dest + "）", false
		}
		return "领奖" + claimTxt + "，已出发", false
	}
	return fmt.Sprintf("领奖%s；depart 失败（HTTP %d）", claimTxt, st3), false
}

// GrowthTravel 成长中心接口：每日旅行（默认不指定目的地）
func (c *Client) GrowthTravel(a *auth.Auth) error {
	msg, ok := c.GrowthTravelSimple(a)
	if !ok {
		return fmt.Errorf("%s", msg)
	}
	log.Printf("growth travel done uid=%s msg=%s", a.UID, msg)
	return nil
}

// GrowthLotterySimple 盲盒：chances balance>0 → draw 循环（上限 20）。
func (c *Client) GrowthLotterySimple(a *auth.Auth, maxDraws int) (string, bool) {
	if maxDraws <= 0 {
		maxDraws = 20
	}
	status, raw := c.growthGet(c.growthURL(a, "lottery/chances"), a)
	if status == http.StatusUnauthorized {
		return "登录态失效（401）", true
	}
	if status < 200 || status >= 300 {
		return fmt.Sprintf("lottery/chances 不可用（HTTP %d）", status), false
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		return "lottery/chances 响应解析失败", false
	}
	balance := 0
	if b := growthDig(body, "balance"); b != nil {
		switch t := b.(type) {
		case float64:
			balance = int(t)
		case json.Number:
			f, _ := t.Float64()
			balance = int(f)
		}
	}
	draws, rewards := 0, 0
	for draws < maxDraws && balance > 0 {
		st, r2 := c.growthPost(c.growthURL(a, "lottery/draw"), a, map[string]any{})
		if st == http.StatusUnauthorized {
			return fmt.Sprintf("登录态失效（401，已抽 %d 次）", draws), true
		}
		if !growthOK(st) {
			break
		}
		draws++
		balance--
		var b2 map[string]any
		if err := json.Unmarshal(r2, &b2); err == nil {
			rw := growthDig(b2, "reward", "credits")
			if rw == nil {
				rw = growthDig(b2, "reward")
			}
			if rw != nil && growthStr(rw) != "" {
				rewards++
			}
		}
	}
	if draws == 0 {
		if balance <= 0 {
			return "盲盒机会已用完", false
		}
		return "draw 不可用", false
	}
	return fmt.Sprintf("抽取 %d 次（%d 次有奖励）", draws, rewards), false
}

// GrowthLottery 成长中心接口：每日幸运抽奖（默认抽完所有机会）
func (c *Client) GrowthLottery(a *auth.Auth) error {
	msg, ok := c.GrowthLotterySimple(a, 20)
	if !ok {
		return fmt.Errorf("%s", msg)
	}
	log.Printf("growth lottery done uid=%s msg=%s", a.UID, msg)
	return nil
}

// GrowthTasksResult 任务执行结果。
type GrowthTasksResult = provider.GrowthTasksResult

// GrowthTasks 任务领奖：tasks → 过滤 has_reward && 未领取 → accept。
// 增强版：记录每个任务的奖励详情（credits/coupons），返回详细统计。
func (c *Client) GrowthTasks(a *auth.Auth) (string, bool) {
	result := c.GrowthTasksDetailed(a)
	return result.Message, result.AuthErr
}

// GrowthTasksDetailed 详细版任务领奖，返回完整结果对象。
func (c *Client) GrowthTasksDetailed(a *auth.Auth) GrowthTasksResult {
	var result GrowthTasksResult

	status, raw := c.growthGet(c.growthURL(a, "tasks"), a)
	if status == http.StatusUnauthorized {
		result.Message = "登录态失效（401）"
		result.AuthErr = true
		return result
	}
	if status < 200 || status >= 300 {
		result.Message = fmt.Sprintf("tasks 不可用（HTTP %d）", status)
		return result
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		result.Message = "tasks 响应解析失败"
		return result
	}
	tasks := growthDig(body, "tasks", "list")
	if tasks == nil {
		tasks = growthDig(body, "tasks", "records")
	}
	list, ok := tasks.([]any)
	if !ok {
		result.Message = "tasks 无可领任务"
		return result
	}
	var claimed, skipped int
	var rewards []provider.TaskReward
	var totalCredits int64

	for _, t := range list {
		obj, ok := t.(map[string]any)
		if !ok {
			continue
		}

		hasReward := growthTruthy(obj["has_reward"]) || growthTruthy(growthDig(obj, "hasReward"))
		if !hasReward {
			skipped++
			continue
		}

		acceptStatus := growthStr(growthDig(obj, "accept_status"))
		if acceptStatus == "" {
			acceptStatus = growthStr(growthDig(obj, "acceptStatus"))
		}

		taskName := growthStr(growthDig(obj, "name"))
		if taskName == "" {
			taskName = growthStr(growthDig(obj, "task_name"))
		}

		code := growthStr(growthDig(obj, "task_code"))
		if code == "" {
			code = growthStr(growthDig(obj, "taskCode"))
		}

		switch acceptStatus {
		case "1", "2", "claimed", "accepted", "已领取", "true", "True", "completed", "已完成":
			skipped++
			continue
		}

		if code == "" {
			skipped++
			log.Printf("[GrowthTasks] 跳过无 task_code 的任务：%s", taskName)
			continue
		}

		acceptBody := map[string]any{
			"task_code": code,
		}

		st, rawResp := c.growthPost(c.growthURL(a, "tasks/accept"), a, acceptBody)
		if st == http.StatusUnauthorized {
			result.Message = fmt.Sprintf("登录态失效（401，已领 %d 项）", claimed)
			result.AuthErr = true
			return result
		}

		if growthOK(st) {
			claimed++
			var reward provider.TaskReward
			reward.Name = taskName

			var rw any
			if rw = growthDig(rawResp, "reward", "credits"); rw != nil {
				switch v := rw.(type) {
				case float64:
					reward.Credits = int64(v)
				case json.Number:
					f, _ := v.Int64()
					reward.Credits = f
				}
			} else if rw = growthDig(rawResp, "reward"); rw != nil {
				if m, ok := rw.(map[string]any); ok {
					if cVal := growthDig(m, "credits"); cVal != nil {
						switch v := cVal.(type) {
						case float64:
							reward.Credits = int64(v)
						case json.Number:
							f, _ := v.Int64()
							reward.Credits = f
						}
					}
					if cn := growthStr(growthDig(m, "coupon_name")); cn != "" {
						reward.CouponName = cn
					}
				}
			}

			totalCredits += reward.Credits
			rewards = append(rewards, reward)

			if reward.Credits > 0 {
				log.Printf("[GrowthTasks] 领取成功：%s (+%d credits)", taskName, reward.Credits)
			} else if reward.CouponName != "" {
				log.Printf("[GrowthTasks] 领取成功：%s 优惠券：%s", taskName, reward.CouponName)
			} else {
				log.Printf("[GrowthTasks] 领取成功：%s", taskName)
			}
		} else {
			log.Printf("[GrowthTasks] 接受任务失败 [%s]: HTTP %d", code, st)
			skipped++
		}
	}

	if claimed == 0 {
		if skipped == len(list) {
			result.Message = "无可领奖励（全部已领取或无奖励）"
		} else {
			result.Message = fmt.Sprintf("无可领奖励（已跳过 %d 项）", skipped)
		}
		return result
	}

	result.Message = fmt.Sprintf("领取 %d 项任务奖励 (+%d credits)", claimed, totalCredits)
	result.TotalCredits = totalCredits
	result.Rewards = rewards
	
	var couponRewards int
	for _, r := range rewards {
		if r.CouponName != "" {
			couponRewards++
		}
	}
	result.CouponCount = couponRewards
	if couponRewards > 0 {
		result.Message += fmt.Sprintf("（含%d个优惠券）", couponRewards)
	}

	return result
}

// GrowthAll 执行成长中心全部功能，返回每项结果。
func (c *Client) GrowthAll(a *auth.Auth) GrowthResult {
	res := GrowthResult{}
	if a == nil {
		res.Travel, res.Lottery, res.Tasks = "无凭证", "无凭证", "无凭证"
		return res
	}
	trErr := c.GrowthTravel(a)
	if trErr != nil {
		res.Travel = fmt.Sprintf("error: %s", trErr.Error())
	} else {
		res.Travel = "ok"
	}
	loErr := c.GrowthLottery(a)
	if loErr != nil {
		res.Lottery = fmt.Sprintf("error: %s", loErr.Error())
	} else {
		res.Lottery = "ok"
	}
	tkResult := c.GrowthTasksDetailed(a)
	res.Tasks = tkResult.Message
	res.TaskDetails = tkResult.Rewards
	res.AuthErr = (trErr != nil) || (loErr != nil) || tkResult.AuthErr
	
	log.Printf("workbuddy growth uid=%s travel=%s lottery=%s tasks=%s (+%d credits)", 
		a.UID, res.Travel, res.Lottery, tkResult.Message, tkResult.TotalCredits)
	return res
}

// GrowthRecord 单次成长中心执行记录。
type GrowthRecord struct {
	Timestamp    int64                    `json:"timestamp"` // Unix 秒
	UID          string                   `json:"uid"`
	Nickname     string                   `json:"nickname"`
	Channel      string                   `json:"channel"` // workbuddy | wbintl | codebuddy
	Travel       string                   `json:"travel,omitempty"`
	Lottery      string                   `json:"lottery,omitempty"`
	Tasks        string                   `json:"tasks,omitempty"`
	TaskDetails  []provider.TaskReward    `json:"task_details,omitempty"`
	TotalCredits int64                    `json:"total_credits"`
	CouponCount  int                      `json:"coupon_count"`
	AuthErr      bool                     `json:"auth_err,omitempty"`
}

// GrowthHistory 成长中心历史记录管理器。
type GrowthHistory struct {
	filePath string
	records  []GrowthRecord
}

// NewGrowthHistory 创建历史记录管理器。
func NewGrowthHistory(filePath string) *GrowthHistory {
	h := &GrowthHistory{
		filePath: filePath,
		records:  make([]GrowthRecord, 0),
	}
	h.load()
	return h
}

// load 从文件加载历史记录。
func (h *GrowthHistory) load() {
	data, err := os.ReadFile(h.filePath)
	if err != nil {
		return
	}
	var recs []GrowthRecord
	if err := json.Unmarshal(data, &recs); err == nil {
		h.records = recs
	}
}

// save 保存历史记录到文件。
func (h *GrowthHistory) save() error {
	data, err := json.MarshalIndent(h.records, "", "  ")
	if err != nil {
		return err
	}
	dir := filepath.Dir(h.filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(h.filePath, data, 0o644)
}

// Add 添加一条记录。
func (h *GrowthHistory) Add(rec GrowthRecord) {
	rec.Timestamp = time.Now().Unix()
	h.records = append([]GrowthRecord{rec}, h.records...)
	// 限制最多保留 100 条
	if len(h.records) > 100 {
		h.records = h.records[:100]
	}
	_ = h.save()
}

// GetToday 获取今日所有记录。
func (h *GrowthHistory) GetToday() []GrowthRecord {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location()).Unix()
	end := start + 86400 // 一天 86400 秒
	
	var today []GrowthRecord
	for _, r := range h.records {
		if r.Timestamp >= start && r.Timestamp < end {
			today = append(today, r)
		}
	}
	return today
}

// GetSummary 获取今日汇总统计。
func (h *GrowthHistory) GetSummary() map[string]any {
	today := h.GetToday()
	totalCredits := int64(0)
	couponCount := 0
	taskCount := 0
	lotteryCount := 0
	travelCount := 0
	
	for _, r := range today {
		totalCredits += r.TotalCredits
		couponCount += r.CouponCount
		taskCount += len(r.TaskDetails)
		if r.Lottery != "" && !strings.Contains(r.Lottery, "无可抽") {
			lotteryCount++
		}
		if r.Travel != "" && !strings.Contains(r.Travel, "已在路上") && !strings.Contains(r.Travel, "已领取") {
			travelCount++
		}
	}
	
	return map[string]any{
		"total_credits": totalCredits,
		"coupon_count":  couponCount,
		"task_count":    taskCount,
		"lottery_count": lotteryCount,
		"travel_count":  travelCount,
		"record_count":  len(today),
	}
}

// Recent 获取最近 N 条记录。
func (h *GrowthHistory) Recent(limit int) []GrowthRecord {
	if limit <= 0 {
		limit = 10
	}
	if len(h.records) <= limit {
		return h.records
	}
	return h.records[:limit]
}
