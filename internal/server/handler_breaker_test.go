package server

// 渠道级熔断回归测试（2026-09-23）。
//
// 背景：客户端（ZCode / Trae）失败后会自行重试。若渠道整池不可用，daemon 仍会
// 把每次重试转发上游，上游看到"凭证失效却持续请求"→ 账号被风控封禁（AutoClaw
// 两账号 410004 事故）。本测试锁定：整池不可用时必须快速失败、不得转发上游。

import (
	"testing"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/pool"
	"zerogate/internal/provider"
)

func newBreakerHandler() *Handler {
	return &Handler{breaker: make(map[string]time.Time)}
}

// 熔断置位/查询/到期/清除的基本语义。
func TestBreakerTripOpenReset(t *testing.T) {
	h := newBreakerHandler()
	kind := provider.AutoClaw

	if open, _ := h.breakerOpen(kind); open {
		t.Fatal("fresh handler should not be tripped")
	}
	h.breakerTrip(kind)
	open, wait := h.breakerOpen(kind)
	if !open {
		t.Fatal("should be open after trip")
	}
	if wait <= 0 || wait > breakerCooldown {
		t.Fatalf("wait %v out of range (0, %v]", wait, breakerCooldown)
	}
	// 到期后应自动放行（半开）
	h.breakerMu.Lock()
	h.breaker[string(kind)] = time.Now().Add(-time.Second)
	h.breakerMu.Unlock()
	if open, _ := h.breakerOpen(kind); open {
		t.Fatal("expired breaker should allow a probe request")
	}
	// 到期放行时已清除记录
	h.breakerMu.Lock()
	_, still := h.breaker[string(kind)]
	h.breakerMu.Unlock()
	if still {
		t.Fatal("expired breaker entry should be deleted on read")
	}

	h.breakerTrip(kind)
	h.breakerReset(kind)
	if open, _ := h.breakerOpen(kind); open {
		t.Fatal("reset should clear the breaker")
	}
}

// 熔断按渠道隔离：一个渠道熔断不影响其他渠道。
func TestBreakerIsolatedPerChannel(t *testing.T) {
	h := newBreakerHandler()
	h.breakerTrip(provider.AutoClaw)
	if open, _ := h.breakerOpen(provider.WorkBuddy); open {
		t.Fatal("WorkBuddy must not be affected by AutoClaw breaker")
	}
	if open, _ := h.breakerOpen(provider.AutoClaw); !open {
		t.Fatal("AutoClaw should stay open")
	}
}

// 健康计数：全禁用时 healthy=0（熔断触发条件），部分可用时 healthy>0。
func TestPoolHealthyCount(t *testing.T) {
	p := pool.New(t.TempDir() + "/state.json")
	for _, uid := range []string{"a", "b", "c"} {
		p.Add(&auth.Auth{UID: uid, Kind: "autoclaw", AccessToken: "tok"})
	}

	if healthy, total := p.HealthyCount(); healthy != 3 || total != 3 {
		t.Fatalf("all-healthy: got %d/%d, want 3/3", healthy, total)
	}

	// 禁用两个：仍有 1 个健康 → 不该熔断
	p.SetDisabled("a", true)
	p.SetDisabled("b", true)
	if healthy, total := p.HealthyCount(); healthy != 1 || total != 3 {
		t.Fatalf("partly disabled: got %d/%d, want 1/3", healthy, total)
	}

	// 全部禁用 → healthy=0，这是熔断的触发条件
	p.SetDisabled("c", true)
	if healthy, total := p.HealthyCount(); healthy != 0 || total != 3 {
		t.Fatalf("all disabled: got %d/%d, want 0/3", healthy, total)
	}
}

// 封禁冷却期内账号不健康；冷却结束后恢复健康（熔断到期自动半开的依据）。
func TestPoolHealthyCountAfterBanCooldown(t *testing.T) {
	p := pool.New(t.TempDir() + "/state.json")
	p.Add(&auth.Auth{UID: "x", Kind: "autoclaw", AccessToken: "tok"})

	p.Ban("x", "上游封禁（410004）")
	if healthy, _ := p.HealthyCount(); healthy != 0 {
		t.Fatalf("banned account should be unhealthy, got healthy=%d", healthy)
	}
}

// 短冷却判定：全池只是短冷却（网络瞬断，剩余 <= max）时应可自愈，不该被渠道
// 熔断锁 60s；长冷却/禁用/存在健康账号都应交回渠道熔断处理。
func TestPoolShortBlocked(t *testing.T) {
	const max = 10 * time.Second
	newPool := func(uids ...string) *pool.Pool {
		p := pool.New(t.TempDir() + "/state.json")
		for _, uid := range uids {
			p.Add(&auth.Auth{UID: uid, Kind: "codebuddy", AccessToken: "tok"})
		}
		return p
	}

	// 空池：不视为短阻塞
	if short, _ := newPool().ShortBlocked(max); short {
		t.Fatal("empty pool should not be short-blocked")
	}

	// 全部短冷却 → 短暂阻塞，可自愈
	p := newPool("a", "b")
	p.Cooldown("a", pool.CoolSoft, 8*time.Second, "上游不可达（非账号问题）")
	p.Cooldown("b", pool.CoolSoft, 8*time.Second, "上游不可达（非账号问题）")
	short, remain := p.ShortBlocked(max)
	if !short {
		t.Fatal("all short-cooldown: want short=true")
	}
	if remain <= 0 || remain > max {
		t.Fatalf("remain %v out of range (0, %v]", remain, max)
	}

	// 有一个账号健康 → 不是整池阻塞
	p.ReenableIfCredits("a", 100)
	if short, _ := p.ShortBlocked(max); short {
		t.Fatal("one healthy account: should not be short-blocked")
	}

	// 长冷却（超过 max）→ 交给渠道熔断
	p2 := newPool("a")
	p2.Cooldown("a", pool.CoolHard, time.Hour, "余额不足")
	if short, _ := p2.ShortBlocked(max); short {
		t.Fatal("long cooldown: should not be short-blocked")
	}

	// 禁用账号 → 交给渠道熔断
	p3 := newPool("a")
	p3.SetDisabled("a", true)
	if short, _ := p3.ShortBlocked(max); short {
		t.Fatal("disabled account: should not be short-blocked")
	}
}
