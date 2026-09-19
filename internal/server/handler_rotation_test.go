package server

// 桥接渠道（zcode）轮转调度测试：验证 2026-09-19 引入的 pickBridgeRotation
// 不破坏粘性路由的其他渠道、不吃掉禁用账号、并发计数器推进正确。

import (
	"testing"
	"time"

	"wild-work/internal/auth"
	"wild-work/internal/pool"
	"wild-work/internal/provider"
)

func TestBridgeRotationSpreadsAndSkipsDisabled(t *testing.T) {
	p := pool.New(t.TempDir() + "/state.json")
	for _, uid := range []string{"zcode-big", "zcode-fuka", "zcode-fuka2", "zcode-cn"} {
		p.Add(&auth.Auth{UID: uid, Kind: "zcode", AccessToken: "bridge"})
	}
	p.SetExpire("zcode-big", time.Now().Add(16*time.Hour).Unix())
	p.SetExpire("zcode-fuka", time.Now().Add(7*time.Hour).Unix())
	p.SetExpire("zcode-fuka2", time.Now().Add(7*time.Hour).Unix())
	p.SetDisabled("zcode-cn", true)

	h := &Handler{}
	rt := &Runtime{Kind: provider.ZCode, Pool: p}

	got := map[string]int{}
	var first string
	for i := 0; i < 8; i++ {
		a := h.pickWithSticky(rt)
		if a == nil {
			t.Fatalf("pick #%d returned nil", i)
		}
		if a.UID == "zcode-cn" {
			t.Fatalf("pick #%d returned disabled account zcode-cn", i)
		}
		if i == 0 {
			first = a.UID
		}
		got[a.UID]++
	}
	// 到期最早的排最前（fuka 与 fuka2 同到期，UID 稳定排序：fuka < fuka2）
	if first != "zcode-fuka" {
		t.Fatalf("first pick = %s, want soonest-expiring zcode-fuka", first)
	}
	// 8 次 = 两个完整轮转 + 2 次：3 个健康账号必须均匀分摊，禁用账号零流量
	if got["zcode-fuka"] != 3 || got["zcode-fuka2"] != 3 || got["zcode-big"] != 2 {
		t.Fatalf("rotation not evenly spread: %v", got)
	}
}

func TestBridgeRotationAllCoolingReturnsGracefully(t *testing.T) {
	p := pool.New(t.TempDir() + "/state.json")
	p.Add(&auth.Auth{UID: "zcode-big", Kind: "zcode", AccessToken: "bridge"})
	p.Cooldown("zcode-big", pool.CoolErr, time.Hour, "test")

	h := &Handler{}
	rt := &Runtime{Kind: provider.ZCode, Pool: p}
	// 全部冷却：不允许 panic；返回 nil 由上层按无可用账号处理
	_ = h.pickWithSticky(rt)
}

func TestStickyUnchangedForNonBridgeChannels(t *testing.T) {
	p := pool.New(t.TempDir() + "/state.json")
	p.Add(&auth.Auth{UID: "wb-1", Kind: "workbuddy", AccessToken: "t"})
	p.Add(&auth.Auth{UID: "wb-2", Kind: "workbuddy", AccessToken: "t"})

	h := &Handler{sticky: make(map[string]*stickyEntry)}
	rt := &Runtime{Kind: provider.WorkBuddy, Pool: p}

	first := h.pickWithSticky(rt).UID
	for i := 0; i < 10; i++ {
		if got := h.pickWithSticky(rt).UID; got != first {
			t.Fatalf("sticky broken for non-bridge channel: want %s got %s", first, got)
		}
	}
}
