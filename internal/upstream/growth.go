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
	"strconv"
	"strings"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
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

// growthDig 从嵌套结构中按候选键依次取值（对齐参考 fs_utils::dig 语义）：
// 每个候选键独立尝试——当前层直接命中（大小写宽容），未命中则下钻信封键与数组元素，
// 任一候选键先命中非 nil 值即返回；全部未命中返回 nil。
func growthDig(v any, keys ...string) any {
	for _, k := range keys {
		if got := growthDigKey(v, k, 0); got != nil {
			return got
		}
	}
	return nil
}

// growthEnvelopeKeys 信封键：候选键未直接命中时，逐层下钻这些容器再找。
var growthEnvelopeKeys = [...]string{"data", "result", "resp", "response", "info", "auth", "account"}

func growthDigKey(v any, k string, depth int) any {
	if depth > 8 || v == nil {
		return nil
	}
	switch t := v.(type) {
	case map[string]any:
		if val, exists := t[k]; exists && val != nil {
			return val
		}
		for kk, vv := range t {
			if equalFold(kk, k) && vv != nil {
				return vv
			}
		}
		for _, env := range growthEnvelopeKeys {
			if val, exists := t[env]; exists {
				if got := growthDigKey(val, k, depth+1); got != nil {
					return got
				}
			}
		}
	case []any:
		for _, item := range t {
			if got := growthDigKey(item, k, depth+1); got != nil {
				return got
			}
		}
	}
	return nil
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

// growthSOf 仅接受字符串（对齐参考 s_of），非字符串返回空。
func growthSOf(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// growthDisplay 展示用值文本：字符串原样，其余 JSON 表示（对齐参考 value_display）。
func growthDisplay(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return fmt.Sprintf("%v", t)
		}
		return string(b)
	}
}

// growth200 判断 2xx。
func growthOK(status int) bool { return status >= 200 && status <= 201 }

// GrowthTravelSimple 旅行：status 到达 → claim 领奖 → config 取目的地 → depart 出发。
// 返回 (msg, authErr)；跳过以 "skip: " 前缀的 msg 表示，由包装器转为 error 上抛。
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
	arrived := growthTruthy(growthDig(body, "arrived", "is_arrived", "has_arrived"))
	recordID := growthDig(body, "record_id", "recordId")
	if !arrived {
		dest := growthSOf(growthDig(body, "destination", "name", "target"))
		if dest != "" {
			return "skip: 旅行在途（" + dest + "）", false
		}
		return "skip: 旅行在途", false
	}
	// 领奖（失败不阻塞出发）
	claimBody := map[string]any{}
	if recordID != nil {
		claimBody["record_id"] = recordID
	}
	st1, raw1 := c.growthPost(c.growthURL(a, "buddy/travel/claim"), a, claimBody)
	if st1 == http.StatusUnauthorized {
		return "登录态失效（401）", true
	}
	claimTxt := ""
	if growthOK(st1) {
		var b1 map[string]any
		if err := json.Unmarshal(raw1, &b1); err == nil {
			if rw := growthDig(b1, "reward", "credits", "points", "amount", "value"); rw != nil {
				claimTxt = " +" + growthDisplay(rw)
			}
		}
	} else {
		claimTxt = fmt.Sprintf(" claim 失败（HTTP %d）", st1)
	}
	// 查目的地（失败不阻塞出发）：出发体用原始值，展示用文本
	var destVal any
	if _, r2 := c.growthGet(c.growthURL(a, "buddy/travel/config"), a); len(r2) > 0 {
		var b2 map[string]any
		if err := json.Unmarshal(r2, &b2); err == nil {
			destVal = growthDig(b2, "destination", "name", "target")
		}
	}
	departBody := map[string]any{}
	destTxt := ""
	if destVal != nil {
		departBody["destination"] = destVal
		destTxt = "（" + growthDisplay(destVal) + "）"
	}
	st3, _ := c.growthPost(c.growthURL(a, "buddy/travel/depart"), a, departBody)
	if growthOK(st3) {
		return "领奖" + claimTxt + "，已出发" + destTxt, false
	}
	return fmt.Sprintf("领奖%s；depart 失败（HTTP %d）", claimTxt, st3), false
}

// GrowthTravel 成长中心接口：每日旅行（默认不指定目的地）。
// 跳过（"skip: " 前缀）与登录态失效均以 error 上抛，scheduler 按前缀分流展示。
func (c *Client) GrowthTravel(a *auth.Auth) error {
	msg, authErr := c.GrowthTravelSimple(a)
	if authErr || strings.HasPrefix(msg, "skip: ") {
		return fmt.Errorf("%s", msg)
	}
	log.Printf("growth travel done uid=%s msg=%s", a.UID, msg)
	return nil
}

// growthIOf 整数解析（对齐参考）：数字截断取整，字符串去空白后 ParseInt；失败返回 0。
func growthIOf(v any) int64 {
	switch t := v.(type) {
	case float64:
		return int64(t)
	case json.Number:
		n, _ := t.Int64()
		return n
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(t), 10, 64)
		if err != nil {
			return 0
		}
		return n
	}
	return 0
}

// GrowthLotterySimple 盲盒：chances balance>0 → draw 循环（上限 maxDraws）。
// 返回 (msg, authErr)；无可用次数以 "skip: " 前缀的 msg 表示，由包装器转为 error 上抛。
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
	balance := growthIOf(growthDig(body, "balance", "chances", "count", "remain"))
	if balance <= 0 {
		return "skip: 无可用次数", false
	}
	draws, rewardTxts := 0, []string{}
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
			if rw := growthDig(b2, "reward", "credits", "points", "amount"); rw != nil {
				if txt := growthDisplay(rw); txt != "" {
					rewardTxts = append(rewardTxts, txt)
				}
			}
		}
	}
	if draws == 0 {
		return "draw 不可用", false
	}
	return fmt.Sprintf("抽取 %d 次（奖励 %s）", draws, strings.Join(rewardTxts, "/")), false
}

// GrowthLottery 成长中心接口：每日幸运抽奖（默认抽完所有机会）。
// 跳过（"skip: " 前缀）与登录态失效均以 error 上抛，scheduler 按前缀分流展示。
func (c *Client) GrowthLottery(a *auth.Auth) error {
	msg, authErr := c.GrowthLotterySimple(a, 20)
	if authErr || strings.HasPrefix(msg, "skip: ") {
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
	tasks := growthDig(body, "tasks", "list", "records")
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

		taskName := growthStr(growthDig(obj, "name", "title"))
		if taskName == "" {
			taskName = growthStr(growthDig(obj, "task_name"))
		}

		code := growthStr(growthDig(obj, "task_code"))
		if code == "" {
			code = growthStr(growthDig(obj, "taskCode"))
		}
		if code == "" {
			code = growthStr(growthDig(obj, "code"))
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

			// 关键：accept 响应必须先反序列化再 dig，否则永远挖不到奖励字段。
			var bResp map[string]any
			_ = json.Unmarshal(rawResp, &bResp)
			if rw := growthDig(bResp, "reward", "credits", "points", "amount", "value"); rw != nil {
				switch v := rw.(type) {
				case float64:
					reward.Credits = int64(v)
				case json.Number:
					f, _ := v.Int64()
					reward.Credits = f
				case map[string]any:
					if cVal := growthDig(v, "credits", "points", "amount"); cVal != nil {
						switch n := cVal.(type) {
						case float64:
							reward.Credits = int64(n)
						case json.Number:
							f, _ := n.Int64()
							reward.Credits = f
						}
					}
					if cn := growthStr(growthDig(v, "coupon_name", "couponName")); cn != "" {
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
		if r.Lottery != "" && !strings.Contains(r.Lottery, "skip") && !strings.Contains(r.Lottery, "无可") {
			lotteryCount++
		}
		if r.Travel != "" && !strings.Contains(r.Travel, "skip") && !strings.Contains(r.Travel, "在途") {
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
