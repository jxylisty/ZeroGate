// Package pool 账号池：内存索引 + 冷却/禁用状态机 + state.json 持久化。
// 挑选策略：有到期时间者优先 > 更早到期者优先 > 剩余积分低者优先。
package pool

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/provider"
)

// CoolKind 冷却类型。
type CoolKind int

const (
	CoolHard       CoolKind = iota // 余额不足 → 长冷却
	CoolSoft                       // 429 → 短冷却
	CoolErr                        // 连续错误 → 中冷却
	CoolLowBalance                 // 余额低于平均 60% → 15min 冷却
)

const (
	banCooldown     = 24 * time.Hour // 封禁冷却时长（如 AutoClaw 410004）
	banDisableAfter = 3              // 封禁累计次数达标后自动禁用
)

func (k CoolKind) String() string {
	switch k {
	case CoolHard:
		return "hard_credit"
	case CoolSoft:
		return "soft_rate"
	case CoolErr:
		return "error_threshold"
	case CoolLowBalance:
		return "low_balance"
	}
	return "unknown"
}

// Status 单个账号对外暴露的状态（脱敏）。
type Status struct {
	UID            string          `json:"uid"`
	Nickname       string          `json:"nickname,omitempty"`
	Credits        int64           `json:"credits"`
	ExpireAt       int64           `json:"expire_at,omitempty"` // 最早到期套餐时间（Unix 秒）；0=长期有效
	ResourceItems  []provider.ResourceItem `json:"resource_items,omitempty"` // 积分分类明细（含到期时间）
	Cooling        bool            `json:"cooling"`
	Until          time.Time       `json:"until,omitempty"`
	Reason         string          `json:"reason,omitempty"`
	Disabled       bool            `json:"disabled"`
	ErrCount       int             `json:"err_count,omitempty"`
	BanCount       int             `json:"ban_count,omitempty"`
	LastCheckinOK  bool            `json:"last_checkin_ok,omitempty"`
	LastCheckinAt  time.Time       `json:"last_checkin_at,omitempty"`
	LastCheckinMsg string          `json:"last_checkin_msg,omitempty"`
}

type entry struct {
	a           *auth.Auth
	placeholder bool
	credits     int64
	expireAt    int64 // 最早到期套餐时间（Unix 秒）；0=长期有效
	resourceItems []provider.ResourceItem // 积分分类明细（含到期时间）
	disabled    bool
	reason      string
	until       time.Time
	errCount    int
	banCount    int // 封禁累计次数（410004 等），达 banDisableAfter 自动禁用

	lastCheckinOK  bool
	lastCheckinAt  time.Time
	lastCheckinMsg string
}

func (e *entry) healthy(now time.Time) bool {
	if e.placeholder {
		return false
	}
	if e.disabled {
		return false
	}
	if !e.until.IsZero() && now.Before(e.until) {
		return false
	}
	return true
}

// stateFile 持久化格式。
type accountState struct {
	Credits        int64     `json:"credits"`
	ExpireAt       int64     `json:"expire_at,omitempty"`
	Disabled       bool      `json:"disabled"`
	Reason         string    `json:"reason,omitempty"`
	Until          time.Time `json:"until,omitempty"`
	BanCount       int       `json:"ban_count,omitempty"`
	LastCheckinOK  bool      `json:"last_checkin_ok,omitempty"`
	LastCheckinAt  time.Time `json:"last_checkin_at,omitempty"`
	LastCheckinMsg string    `json:"last_checkin_msg,omitempty"`
}

type stateFile struct {
	Accounts map[string]accountState `json:"accounts"`
}

// Pool 账号池。
type Pool struct {
	mu      sync.RWMutex
	byUID   map[string]*entry
	stateFp string
	saveMu  sync.Mutex // 独立写锁：保证磁盘 I/O 串行且不阻塞 mu 读写锁
}

// New 构建池；stateFp 非空时尝试加载旧状态。
func New(stateFp string) *Pool {
	p := &Pool{byUID: map[string]*entry{}, stateFp: stateFp}
	if stateFp != "" {
		p.load()
	}
	return p
}

// Add 加入账号；已存在则保留原状态、更新凭证。
func (p *Pool) Add(a *auth.Auth) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.byUID[a.UID]; ok {
		e.a = a // 保留 credits/cooling 状态
		e.placeholder = false
		return
	}
	p.byUID[a.UID] = &entry{a: a}
}

// SyncToDir 用最新扫描结果对齐池：新账号加入、消失的账号剔除（状态保留）。
func (p *Pool) SyncToDir(auths []*auth.Auth) {
	p.mu.Lock()
	defer p.mu.Unlock()
	seen := map[string]bool{}
	for _, a := range auths {
		seen[a.UID] = true
		if e, ok := p.byUID[a.UID]; ok {
			e.a = a
			e.placeholder = false
		} else {
			p.byUID[a.UID] = &entry{a: a}
		}
	}
	for uid := range p.byUID {
		if !seen[uid] {
			delete(p.byUID, uid)
		}
	}
}

// Pick 返回 healthy 中优先消耗快到期积分的账号（有到期 > 更早到期 > 余额低）；
// 无可用返回 nil。credits 为 0 也参与挑选（新账号首次刷新前即从 0 开始计数）。
func (p *Pool) Pick() *auth.Auth {
	return p.PickExcluding(nil)
}

// PickExcluding 同上，但跳过 tried 中的 uid（请求级轮换）。
func (p *Pool) PickExcluding(tried map[string]bool) *auth.Auth {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now()
	var best *entry
	for uid, e := range p.byUID {
		if tried != nil && tried[uid] {
			continue
		}
		if !e.healthy(now) {
			continue
		}
		if best == nil || betterThan(e, best) {
			best = e
		}
	}
	if best == nil {
		return nil
	}
	return best.a
}

// betterThan 返回 e1 是否应比 e2 更优先被路由：
// 有到期时间的优先于长期有效的；同样有时限的越早到期越优先；平手时余额低者优先。
func betterThan(e1, e2 *entry) bool {
	e1Has := e1.expireAt > 0
	e2Has := e2.expireAt > 0
	if e1Has != e2Has {
		return e1Has
	}
	if e1Has && e1.expireAt != e2.expireAt {
		return e1.expireAt < e2.expireAt
	}
	return e1.credits < e2.credits
}

// SetCredits 更新账号余额。
func (p *Pool) SetCredits(uid string, credits int64) {
	p.SetCreditsExpire(uid, credits, 0)
}

// SetCreditsExpire 更新账号余额与最早到期时间（expireAt Unix 秒，0 不清除现有时限）。
func (p *Pool) SetCreditsExpire(uid string, credits, expireAt int64) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.credits = credits
		if expireAt > 0 {
			e.expireAt = expireAt
		}
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// SetResourceItems 更新账号积分分类明细（含到期时间）。
func (p *Pool) SetResourceItems(uid string, items []provider.ResourceItem) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.resourceItems = items
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// SetExpire 仅更新账号最早到期时间（expireAt Unix 秒，0=无时限）。
func (p *Pool) SetExpire(uid string, expireAt int64) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.expireAt = expireAt
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// Cooldown 冷却账号至 now+d。
func (p *Pool) Cooldown(uid string, kind CoolKind, d time.Duration, reason string) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.until = time.Now().Add(d)
		e.reason = reason
		e.errCount = 0
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// Disable 永久禁用（session 死亡），需人工重登后手工恢复或文件替换。
func (p *Pool) Disable(uid, reason string) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.disabled = true
		e.reason = reason
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// Ban 封禁处置（借 autoclawpi CooldownBanned + GLM_proxy 封禁分类）：
// 24h 长冷却并累计封禁次数；累计达 banDisableAfter 次自动转禁用。
func (p *Pool) Ban(uid, reason string) {
	p.mu.Lock()
	e, ok := p.byUID[uid]
	if !ok {
		p.mu.Unlock()
		return
	}
	e.banCount++
	if e.banCount >= banDisableAfter {
		e.disabled = true
		e.reason = fmt.Sprintf("%s（封禁 %d 次自动禁用）", reason, e.banCount)
	} else {
		e.until = time.Now().Add(banCooldown)
		e.reason = reason
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// SetDisabled 设置账号禁用/启用状态；手动解禁时重置封禁计数。
func (p *Pool) SetDisabled(uid string, d bool) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.disabled = d
		if d {
			e.reason = "手动停用"
		} else {
			e.reason = ""
			e.banCount = 0
		}
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// ReenableIfCredits 签到后解冻：仅当 remain > 0 且账号处于冷却（非禁用）时恢复。
func (p *Pool) ReenableIfCredits(uid string, remain int64) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.credits = remain
		if remain > 0 && !e.disabled {
			e.until = time.Time{}
			e.reason = ""
			e.errCount = 0
		}
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// RecordCheckin 记录一次签到结果（含错误信息），随 state.json 持久化。
func (p *Pool) RecordCheckin(uid string, ok bool, msg string) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.lastCheckinOK = ok
		e.lastCheckinAt = time.Now()
		e.lastCheckinMsg = msg
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// Remove 从池中移除账号（内存 + 状态文件）；auth 文件删除由调用方负责。
func (p *Pool) Remove(uid string) {
	p.mu.Lock()
	delete(p.byUID, uid)
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// NoteError 记录一次非余额/非 429 错误；达到 threshold 自动冷却 d 时长。
func (p *Pool) NoteError(uid string, threshold int, d time.Duration) {
	p.mu.Lock()
	if e, ok := p.byUID[uid]; ok {
		e.errCount++
		if e.errCount >= threshold {
			e.until = time.Now().Add(d)
			e.reason = "consecutive errors"
			e.errCount = 0
		}
	}
	sf := p.snapshotLocked()
	p.mu.Unlock()
	p.saveSnapshot(sf)
}

// NoteSuccess 成功请求重置错误计数。
func (p *Pool) NoteSuccess(uid string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.byUID[uid]; ok {
		e.errCount = 0
	}
}

// Status 查询单账号状态。
func (p *Pool) Status(uid string) (Status, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	e, ok := p.byUID[uid]
	if !ok {
		return Status{}, false
	}
	return p.statusOf(uid, e), true
}

// AuthByUID 返回账号的完整凭证（给调度器/运维接口用）。
func (p *Pool) AuthByUID(uid string) *auth.Auth {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if e, ok := p.byUID[uid]; ok {
		return e.a
	}
	return nil
}

// List 返回所有账号状态（按 UID 排序，稳定输出）。
// Auths 返回池中全部账号的 Auth 引用（供桥接/领取等遍历；调用方不得改写字段，
// 需改写请走 Add/Cooldown/Disable 等带锁接口）。
func (p *Pool) Auths() []*auth.Auth {
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]*auth.Auth, 0, len(p.byUID))
	for _, e := range p.byUID {
		out = append(out, e.a)
	}
	return out
}

// HealthyCount 返回当前健康（未禁用、未冷却）的账号数与总数。
//
// 供渠道级熔断判断：健康数为 0 时整池都不可用（全部封禁/冷却/停用），
// 此时继续向上游转发毫无意义 —— 只会把"账号已被封禁"这类错误反复打给上游，
// 反而加深风控标记（2026-09-23 AutoClaw 两账号即因此被封）。
func (p *Pool) HealthyCount() (healthy, total int) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now()
	for _, e := range p.byUID {
		total++
		if e.healthy(now) {
			healthy++
		}
	}
	return healthy, total
}

// ShortBlocked 判断整池是否"仅因短冷却而暂时不可用"，供渠道熔断区分处置。
//
// HealthyCount 只回答"有没有健康账号"，无法区分"全池被封禁/长冷却"（该熔断）
// 与"全池只是撞上一次网络抖动/429 的瞬断"（几秒后自愈，不该把整通道锁 60s）。
// 仅当池中存在账号、且每个账号都处于剩余 <= max 的冷却时返回 (true, 最长剩余)；
// 任一账号健康、被禁用、占位，或冷却剩余超过 max，都返回 false（交给渠道熔断）。
func (p *Pool) ShortBlocked(max time.Duration) (bool, time.Duration) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	now := time.Now()
	seen := false
	var remain time.Duration
	for _, e := range p.byUID {
		seen = true
		if e.disabled || e.placeholder {
			return false, 0
		}
		if e.until.IsZero() || !now.Before(e.until) {
			return false, 0 // 有健康账号
		}
		r := e.until.Sub(now)
		if r > max {
			return false, 0 // 长冷却，交给渠道熔断
		}
		if r > remain {
			remain = r
		}
	}
	if !seen {
		return false, 0
	}
	return true, remain
}

func (p *Pool) List() []Status {
	p.mu.RLock()
	defer p.mu.RUnlock()
	uids := make([]string, 0, len(p.byUID))
	for uid := range p.byUID {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	out := make([]Status, 0, len(uids))
	for _, uid := range uids {
		out = append(out, p.statusOf(uid, p.byUID[uid]))
	}
	return out
}

func (p *Pool) statusOf(uid string, e *entry) Status {
	now := time.Now()
	return Status{
		UID:            uid,
		Nickname:       e.a.Nickname,
		Credits:        e.credits,
		ExpireAt:       e.expireAt,
		ResourceItems:  e.resourceItems,
		Cooling:        !e.until.IsZero() && now.Before(e.until),
		Until:          e.until,
		Reason:         e.reason,
		Disabled:       e.disabled,
		ErrCount:       e.errCount,
		LastCheckinOK:  e.lastCheckinOK,
		LastCheckinAt:  e.lastCheckinAt,
		LastCheckinMsg: e.lastCheckinMsg,
	}
}

// ---------------------------------------------------------------------------
// 持久化
// ---------------------------------------------------------------------------

func (p *Pool) load() {
	raw, err := os.ReadFile(p.stateFp)
	if err != nil {
		return
	}
	var sf stateFile
	if json.Unmarshal(raw, &sf) != nil {
		return
	}
	for uid, s := range sf.Accounts {
		p.byUID[uid] = &entry{
			a:              &auth.Auth{UID: uid}, // placeholder，Add 时会换成完整凭证
			placeholder:    true,
			credits:        s.Credits,
			expireAt:       s.ExpireAt,
			disabled:       s.Disabled,
			reason:         s.Reason,
			until:          s.Until,
			banCount:       s.BanCount,
			lastCheckinOK:  s.LastCheckinOK,
			lastCheckinAt:  s.LastCheckinAt,
			lastCheckinMsg: s.LastCheckinMsg,
		}
	}
}

// snapshotLocked 生成当前账号池状态快照（调用方必须持有 p.mu，耗时 < 1µs）。
func (p *Pool) snapshotLocked() stateFile {
	sf := stateFile{Accounts: make(map[string]accountState, len(p.byUID))}
	for uid, e := range p.byUID {
		sf.Accounts[uid] = accountState{
			Credits:        e.credits,
			ExpireAt:       e.expireAt,
			Disabled:       e.disabled,
			Reason:         e.reason,
			Until:          e.until,
			BanCount:       e.banCount,
			LastCheckinOK:  e.lastCheckinOK,
			LastCheckinAt:  e.lastCheckinAt,
			LastCheckinMsg: e.lastCheckinMsg,
		}
	}
	return sf
}

// saveLocked 保留兼容测试与持锁调用场景。
func (p *Pool) saveLocked() {
	sf := p.snapshotLocked()
	p.saveSnapshot(sf)
}

// saveSnapshot 将快照持久化到磁盘（不在 p.mu 读写锁内执行，杜绝磁盘 I/O 阻塞 Pick 调度）。
func (p *Pool) saveSnapshot(sf stateFile) {
	if p.stateFp == "" {
		return
	}
	p.saveMu.Lock()
	defer p.saveMu.Unlock()

	raw, err := json.MarshalIndent(sf, "", "  ")
	if err != nil {
		log.Printf("pool state marshal failed fp=%s: %v", p.stateFp, err)
		return
	}
	if dir := filepath.Dir(p.stateFp); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			log.Printf("pool state mkdir failed dir=%s: %v", dir, err)
			return
		}
	}
	tmp := p.stateFp + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		log.Printf("pool state save failed fp=%s: %v（账号状态未持久化，重启将丢失冷却/签到记录）", tmp, err)
		return
	}
	// Windows 杀毒软件/索引服务瞬时锁定重命名重试
	var renErr error
	for attempt := 0; attempt < 3; attempt++ {
		renErr = os.Rename(tmp, p.stateFp)
		if renErr == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if renErr != nil {
		log.Printf("pool state rename failed tmp=%s fp=%s: %v", tmp, p.stateFp, renErr)
	}
}
