// Package scheduler 定时任务：每日签到 + token keepalive。
// 签到成功后重新查余额，余额 > 0 的冷却账号自动解冻。
// 签到时间支持分钟精度运行中更新（SetCheckinMinutes），由 GUI 面板写入 config.json 后调用。
package scheduler

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/pool"
	"wild-work/internal/provider"
)

// Config 调度器依赖。
type Config struct {
	Pool           *pool.Pool
	Upstream       provider.Upstream
	Name           string // 日志中的平台名
	CheckinHours   []int  // 旧配置兼容，整点小时
	CheckinMinutes []int  // 当天分钟数，优先于 CheckinHours
	GrowthMinutes  []int  // 成长中心执行时间（当天分钟数）
	KeepaliveHours []int  // 默认 [22]
}

// Scheduler 调度器。
type Scheduler struct {
	mu        sync.Mutex // 保护 cfg 中的小时配置
	cfg       Config
	wake      chan struct{}              // 配置变更唤醒 Run 循环重算下次触发
	onCheckin func(CheckinResult)        // 结果观察器，供 GUI 接收自动签到结果
	onGrowth  func(GrowthResult)         // 成长中心结果观察器
	onRefresh func(string, bool, string) // token 刷新结果观察器
}

// New 构建。
func New(cfg Config) *Scheduler {
	if len(cfg.CheckinMinutes) == 0 {
		if len(cfg.CheckinHours) > 0 {
			cfg.CheckinMinutes = make([]int, 0, len(cfg.CheckinHours))
			for _, h := range cfg.CheckinHours {
				if h >= 0 && h <= 23 {
					cfg.CheckinMinutes = append(cfg.CheckinMinutes, h*60)
				}
			}
		} else {
			cfg.CheckinMinutes = []int{9 * 60, 21 * 60}
		}
	}
	if len(cfg.KeepaliveHours) == 0 {
		cfg.KeepaliveHours = []int{22}
	}
	return &Scheduler{cfg: cfg, wake: make(chan struct{}, 1)}
}

// schedule 返回当前签到分钟/保活小时配置的副本。
func (s *Scheduler) schedule() (checkinMinutes, growthMinutes, keepaliveHours []int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]int{}, s.cfg.CheckinMinutes...), append([]int{}, s.cfg.GrowthMinutes...), append([]int{}, s.cfg.KeepaliveHours...)
}

// CheckinHours 保留旧 API，返回整点签到小时。
func (s *Scheduler) CheckinHours() []int {
	minutes, _, _ := s.schedule()
	out := make([]int, 0, len(minutes))
	for _, m := range minutes {
		if m%60 == 0 {
			out = append(out, m/60)
		}
	}
	return out
}

// CheckinMinutes 当前签到时间，单位为当天分钟数（0..1439）。
func (s *Scheduler) CheckinMinutes() []int {
	minutes, _, _ := s.schedule()
	return minutes
}

// CheckinTimes 当前签到时间，格式为 HH:MM。
func (s *Scheduler) CheckinTimes() []string {
	minutes := s.CheckinMinutes()
	out := make([]string, 0, len(minutes))
	for _, m := range minutes {
		out = append(out, fmt.Sprintf("%02d:%02d", m/60, m%60))
	}
	return out
}

// GrowthMinutes 当前成长中心执行时间，单位为当天分钟数（0..1439）。
func (s *Scheduler) GrowthMinutes() []int {
	_, minutes, _ := s.schedule()
	return minutes
}

// GrowthTimes 当前成长中心执行时间，格式为 HH:MM。
func (s *Scheduler) GrowthTimes() []string {
	minutes := s.GrowthMinutes()
	out := make([]string, 0, len(minutes))
	for _, m := range minutes {
		out = append(out, fmt.Sprintf("%02d:%02d", m/60, m%60))
	}
	return out
}

// KeepaliveHours 当前保活时间（副本）。
func (s *Scheduler) KeepaliveHours() []int {
	_, _, kh := s.schedule()
	return kh
}

// SetCheckinMinutes 运行中更新签到分钟并唤醒调度循环。
func (s *Scheduler) SetCheckinMinutes(minutes []int) {
	clean := make([]int, 0, len(minutes))
	seen := map[int]bool{}
	for _, m := range minutes {
		if m >= 0 && m < 24*60 && !seen[m] {
			seen[m] = true
			clean = append(clean, m)
		}
	}
	sort.Ints(clean)
	s.mu.Lock()
	s.cfg.CheckinMinutes = clean
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// SetCheckinHours 保留旧 API，将整点小时转换为当天分钟数。
func (s *Scheduler) SetCheckinHours(hours []int) {
	minutes := make([]int, 0, len(hours))
	for _, h := range hours {
		minutes = append(minutes, h*60)
	}
	s.SetCheckinMinutes(minutes)
}

// SetGrowthMinutes 运行中更新成长中心执行分钟并唤醒调度循环。
func (s *Scheduler) SetGrowthMinutes(minutes []int) {
	clean := make([]int, 0, len(minutes))
	seen := map[int]bool{}
	for _, m := range minutes {
		if m >= 0 && m < 24*60 && !seen[m] {
			seen[m] = true
			clean = append(clean, m)
		}
	}
	sort.Ints(clean)
	s.mu.Lock()
	s.cfg.GrowthMinutes = clean
	s.mu.Unlock()
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

// SetGrowthObserver 设置成长中心结果观察器；用于把定时任务结果推送到 GUI。
func (s *Scheduler) SetGrowthObserver(fn func(GrowthResult)) {
	s.mu.Lock()
	s.onGrowth = fn
	s.mu.Unlock()
}

func (s *Scheduler) notifyGrowth(r GrowthResult) {
	s.mu.Lock()
	fn := s.onGrowth
	s.mu.Unlock()
	if fn != nil {
		fn(r)
	}
}

// SetCheckinObserver 设置签到结果观察器；用于把定时任务结果推送到 GUI。
func (s *Scheduler) SetCheckinObserver(fn func(CheckinResult)) {
	s.mu.Lock()
	s.onCheckin = fn
	s.mu.Unlock()
}

func (s *Scheduler) notifyCheckin(r CheckinResult) {
	s.mu.Lock()
	fn := s.onCheckin
	s.mu.Unlock()
	if fn != nil {
		fn(r)
	}
}

// SetRefreshObserver 设置 token 刷新结果观察器。
func (s *Scheduler) SetRefreshObserver(fn func(uid string, ok bool, msg string)) {
	s.mu.Lock()
	s.onRefresh = fn
	s.mu.Unlock()
}

func (s *Scheduler) notifyRefresh(uid string, ok bool, msg string) {
	s.mu.Lock()
	fn := s.onRefresh
	s.mu.Unlock()
	if fn != nil {
		fn(uid, ok, msg)
	}
}

// NextFire 返回最近的一次触发时间（签到 + 保活合并）。
func (s *Scheduler) NextFire() time.Time {
	ch, _, kh := s.schedule()
	all := append(append([]int{}, ch...), hoursToMinutes(kh)...)
	return nextFireMinutes(time.Now(), all)
}

// NextGrowthFire 返回最近的成长中心执行时间。
func (s *Scheduler) NextGrowthFire() time.Time {
	_, gm, _ := s.schedule()
	if len(gm) == 0 {
		return time.Time{}
	}
	return nextFireMinutes(time.Now(), gm)
}

// nextFire 保留旧测试/API语义：hours 为本地小时（0-23）。
func nextFire(now time.Time, hours []int) time.Time {
	return nextFireMinutes(now, hoursToMinutes(hours))
}

func hoursToMinutes(hours []int) []int {
	out := make([]int, 0, len(hours))
	for _, h := range hours {
		out = append(out, h*60)
	}
	return out
}

// nextFireMinutes 返回 now 之后最近的触发时间；输入为当天分钟数。
func nextFireMinutes(now time.Time, minutes []int) time.Time {
	var earliest time.Time
	for _, m := range minutes {
		if m < 0 || m >= 24*60 {
			continue
		}
		t := time.Date(now.Year(), now.Month(), now.Day(), m/60, m%60, 0, 0, now.Location())
		if !t.After(now) {
			t = t.Add(24 * time.Hour)
		}
		if earliest.IsZero() || t.Before(earliest) {
			earliest = t
		}
	}
	return earliest
}

// Run 主循环，阻塞直到 ctx 取消。
func (s *Scheduler) Run(ctx context.Context) {
	for {
		ch, gm, kh := s.schedule()
		all := append(append(append([]int{}, ch...), hoursToMinutes(kh)...), gm...)
		next := nextFireMinutes(time.Now(), all)
		timer := time.NewTimer(time.Until(next))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-s.wake:
			timer.Stop() // 配置变更，重算
		case <-timer.C:
			now := time.Now()
			minute := now.Hour()*60 + now.Minute()
			if containsMinute(ch, minute) {
				s.RunCheckinNow()
			}
			if containsMinute(gm, minute) {
				s.RunGrowthNow()
			}
			if contains(kh, now.Hour()) {
				s.RunKeepaliveNow()
			}
		}
	}
}

func containsMinute(minutes []int, minute int) bool {
	for _, v := range minutes {
		if v == minute {
			return true
		}
	}
	return false
}

func contains(hours []int, h int) bool {
	for _, v := range hours {
		if v == h {
			return true
		}
	}
	return false
}

// CheckinResult 单账号签到结果（GUI 面板展示）。
type CheckinResult struct {
	UID       string `json:"uid"`
	OK        bool   `json:"ok"`
	Msg       string `json:"msg"`
	Remain    int64  `json:"remain"`
	HasRemain bool   `json:"has_remain"`
}

// GrowthResult 单账号成长中心结果（GUI 面板展示）。
type GrowthResult struct {
	UID          string         `json:"uid"`
	Nickname     string         `json:"nickname"`
	Travel       string         `json:"travel,omitempty"`
	Lottery      string         `json:"lottery,omitempty"`
	Tasks        string         `json:"tasks,omitempty"`
	TaskDetails  []TaskReward   `json:"task_details,omitempty"`
	TotalCredits int64          `json:"total_credits"`
	CouponCount  int            `json:"coupon_count"`
	AuthErr      bool           `json:"auth_err,omitempty"`
}

// TaskReward 任务奖励详情。
type TaskReward struct {
	Name       string `json:"name"`
	Credits    int64  `json:"credits"`
	CouponName string `json:"coupon_name,omitempty"`
}

// RunCheckinNow 立即对所有账号执行签到 + 余额刷新 + 解冻。
// 冷却中的账号也参与（签到就是为了解冻它们）；禁用的跳过。
func (s *Scheduler) RunCheckinNow() {
	name := s.name()
	log.Printf("checkin batch start platform=%s accounts=%d", name, len(s.cfg.Pool.List()))
	for _, st := range s.cfg.Pool.List() {
		if st.Disabled {
			log.Printf("checkin skip platform=%s uid=%s reason=disabled", name, st.UID)
			continue
		}
		r := s.checkinOne(st.UID)
		log.Printf("checkin result platform=%s uid=%s ok=%t msg=%s remain=%d has_remain=%t", name, st.UID, r.OK, r.Msg, r.Remain, r.HasRemain)
	}
	log.Printf("checkin batch done platform=%s", name)
}

// CheckinAccount 单个账号立即签到（禁用跳过），返回该账号结果。
func (s *Scheduler) CheckinAccount(uid string) (CheckinResult, error) {
	st, ok := s.cfg.Pool.Status(uid)
	if !ok {
		return CheckinResult{}, fmt.Errorf("unknown account %s", uid)
	}
	if st.Disabled {
		return CheckinResult{}, fmt.Errorf("account %s disabled", uid)
	}
	return s.checkinOne(uid), nil
}

// checkinOne 单账号签到 + 余额刷新 + 解冻 + 记录签到状态。
func (s *Scheduler) checkinOne(uid string) CheckinResult {
	name := s.name()
	log.Printf("checkin start platform=%s uid=%s", name, uid)
	a := s.cfg.Pool.AuthByUID(uid)
	if a == nil || a.RefreshToken == "" {
		r := CheckinResult{UID: uid, Msg: "no refresh token"}
		s.cfg.Pool.RecordCheckin(uid, false, r.Msg)
		s.notifyCheckin(r)
		return r
	}
	// 签到前保证 access token 有效；否则仅依赖晚间 keepalive 时，早上的签到可能拿过期 token。
	if a.NeedsRefresh(2 * time.Hour) {
		if err := s.refreshForCheckin(a, uid); err != nil {
			r := CheckinResult{UID: uid, Msg: "refresh: " + shortErr(err)}
			s.cfg.Pool.RecordCheckin(uid, false, r.Msg)
			s.notifyCheckin(r)
			return r
		}
	}
	r := CheckinResult{UID: uid}
	checkinErr := s.cfg.Upstream.DailyCheckin(a)
	// status 接口本身就是令牌有效性验证；若返回 session dead，刷新一次后重试整套签到。
	if checkinErr != nil && isSessionDead(checkinErr) {
		log.Printf("checkin token invalid platform=%s uid=%s, refreshing and retrying", name, uid)
		if err := s.refreshForCheckin(a, uid); err != nil {
			checkinErr = fmt.Errorf("token invalid; refresh failed: %w", err)
		} else {
			checkinErr = s.cfg.Upstream.DailyCheckin(a)
		}
	}
	if checkinErr != nil {
		log.Printf("checkin failed platform=%s uid=%s err=%v", name, uid, checkinErr)
		r.Msg = shortErr(checkinErr)
		if isAlready(checkinErr) {
			r.OK = true // 已签到时视为成功状态
			r.Msg = "已签到"
		}
	} else {
		r.OK = true
		r.Msg = "ok"
	}
	// 无论签到成败都查余额（已签到等业务错误下余额刷新仍有效）
	remain, rerr := s.cfg.Upstream.UserResource(a)
	if rerr != nil {
		log.Printf("checkin credits failed platform=%s uid=%s err=%v", name, uid, rerr)
		r.OK = false // 签到后的积分确认失败，整次操作向 GUI 报告失败
		if r.Msg == "" {
			r.Msg = "余额查询失败"
		} else {
			r.Msg += "；余额查询失败"
		}
	} else {
		r.Remain, r.HasRemain = remain, true
		log.Printf("checkin credits platform=%s uid=%s remain=%d", name, uid, remain)
		s.cfg.Pool.ReenableIfCredits(uid, remain)
	}
	s.cfg.Pool.RecordCheckin(uid, r.OK, r.Msg)
	s.notifyCheckin(r)
	return r
}

// isAlready 只匹配明确的“今日已签到”，不能因错误文本包含 checkin 就判成功。
func isAlready(err error) bool {
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "已签到") ||
		strings.Contains(s, "already check") ||
		strings.Contains(s, "already checked") ||
		strings.Contains(s, "code=9095")
}

func shortErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if len(s) > 120 {
		return s[:120]
	}
	return s
}

func (s *Scheduler) name() string {
	if s.cfg.Name != "" {
		return s.cfg.Name
	}
	return "unknown"
}

func (s *Scheduler) refreshForCheckin(a *auth.Auth, uid string) error {
	name := s.name()
	log.Printf("refresh start platform=%s uid=%s reason=checkin", name, uid)
	if err := s.cfg.Upstream.RefreshToken(a); err != nil {
		log.Printf("refresh failed platform=%s uid=%s err=%v", name, uid, err)
		return err
	}
	if err := a.SaveAtomic(); err != nil {
		log.Printf("refresh save failed platform=%s uid=%s err=%v", name, uid, err)
		return fmt.Errorf("refresh save: %w", err)
	}
	log.Printf("refresh success platform=%s uid=%s expires_at=%d", name, uid, a.ExpiresAt)
	return nil
}

func isSessionDead(err error) bool {
	var ue *provider.Error
	return errors.As(err, &ue) && ue.Kind == provider.ErrSessionDead
}

// RunKeepaliveNow 立即对所有账号刷新 token；session 死亡的自动禁用。
func (s *Scheduler) RunKeepaliveNow() {
	name := s.name()
	log.Printf("refresh batch start platform=%s", name)
	for _, st := range s.cfg.Pool.List() {
		if st.Disabled {
			log.Printf("refresh skip platform=%s uid=%s reason=disabled", name, st.UID)
			continue
		}
		a := s.cfg.Pool.AuthByUID(st.UID)
		if a == nil || a.RefreshToken == "" {
			msg := "no refresh token"
			log.Printf("refresh skip platform=%s uid=%s reason=%s", name, st.UID, msg)
			s.notifyRefresh(st.UID, false, msg)
			continue
		}
		log.Printf("refresh start platform=%s uid=%s reason=keepalive", name, st.UID)
		if err := s.cfg.Upstream.RefreshToken(a); err != nil {
			log.Printf("refresh failed platform=%s uid=%s err=%v", name, st.UID, err)
			var ue *provider.Error
			if errors.As(err, &ue) && ue.Kind == provider.ErrSessionDead {
				s.cfg.Pool.Disable(st.UID, "12153 session dead")
				log.Printf("refresh disabled platform=%s uid=%s reason=session_dead", name, st.UID)
			}
			s.notifyRefresh(st.UID, false, err.Error())
			continue
		}
		if err := a.SaveAtomic(); err != nil {
			log.Printf("refresh save failed platform=%s uid=%s err=%v", name, st.UID, err)
			s.notifyRefresh(st.UID, false, "refresh save: "+err.Error())
			continue
		}
		log.Printf("refresh success platform=%s uid=%s expires_at=%d", name, st.UID, a.ExpiresAt)
		s.notifyRefresh(st.UID, true, "ok")
	}
	log.Printf("refresh batch done platform=%s", name)
}

// RunGrowthNow 立即对所有账号执行成长中心任务。
func (s *Scheduler) RunGrowthNow() {
	name := s.name()
	log.Printf("growth batch start platform=%s accounts=%d", name, len(s.cfg.Pool.List()))
	for _, st := range s.cfg.Pool.List() {
		if st.Disabled {
			log.Printf("growth skip platform=%s uid=%s reason=disabled", name, st.UID)
			continue
		}
		r := s.growthOne(st.UID)
		log.Printf("growth result platform=%s uid=%s travel=%s lottery=%s tasks=%s credits=%d coupons=%d",
			name, st.UID, r.Travel, r.Lottery, r.Tasks, r.TotalCredits, r.CouponCount)
	}
	log.Printf("growth batch done platform=%s", name)
}

// GrowthAccount 单个账号立即执行成长中心（禁用跳过），返回该账号结果。
func (s *Scheduler) GrowthAccount(uid string) (GrowthResult, error) {
	st, ok := s.cfg.Pool.Status(uid)
	if !ok {
		return GrowthResult{}, fmt.Errorf("unknown account %s", uid)
	}
	if st.Disabled {
		return GrowthResult{}, fmt.Errorf("account %s disabled", uid)
	}
	return s.growthOne(uid), nil
}

// growthOne 单账号执行成长中心（travel + lottery + tasks）。
func (s *Scheduler) growthOne(uid string) GrowthResult {
	name := s.name()
	a := s.cfg.Pool.AuthByUID(uid)
	if a == nil {
		r := GrowthResult{UID: uid, AuthErr: true}
		s.notifyGrowth(r)
		return r
	}
	r := GrowthResult{UID: uid, Nickname: a.Nickname}
	
	// 执行每日旅行
	tr := ""
	if err := s.cfg.Upstream.GrowthTravel(a); err != nil {
		// skip: 前缀 = 正常跳过（如旅行在途），原样展示；否则记为失败。
		if strings.HasPrefix(err.Error(), "skip: ") {
			tr = err.Error()
			log.Printf("growth travel skip platform=%s uid=%s msg=%s", name, uid, err)
		} else {
			tr = fmt.Sprintf("error: %s", shortErr(err))
			log.Printf("growth travel failed platform=%s uid=%s err=%v", name, uid, err)
		}
	} else {
		tr = "ok"
		log.Printf("growth travel success platform=%s uid=%s", name, uid)
	}
	r.Travel = tr

	// 执行幸运抽奖
	lo := ""
	if err := s.cfg.Upstream.GrowthLottery(a); err != nil {
		if strings.HasPrefix(err.Error(), "skip: ") {
			lo = err.Error()
			log.Printf("growth lottery skip platform=%s uid=%s msg=%s", name, uid, err)
		} else {
			lo = fmt.Sprintf("error: %s", shortErr(err))
			log.Printf("growth lottery failed platform=%s uid=%s err=%v", name, uid, err)
		}
	} else {
		lo = "ok"
		log.Printf("growth lottery success platform=%s uid=%s", name, uid)
	}
	r.Lottery = lo
	
	// 执行任务领取
	tkResult := s.cfg.Upstream.GrowthTasksDetailed(a)
	r.Tasks = tkResult.Message
	r.TaskDetails = make([]TaskReward, len(tkResult.Rewards))
	for i, rew := range tkResult.Rewards {
		r.TaskDetails[i] = TaskReward{
			Name:       rew.Name,
			Credits:    rew.Credits,
			CouponName: rew.CouponName,
		}
	}
	r.TotalCredits = tkResult.TotalCredits
	r.CouponCount = tkResult.CouponCount
	log.Printf("growth tasks completed platform=%s uid=%s msg=%s credits=%d coupons=%d", 
		name, uid, tkResult.Message, tkResult.TotalCredits, r.CouponCount)
	
	s.notifyGrowth(r)
	return r
}
