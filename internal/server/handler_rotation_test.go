package server

// 桥接渠道（zcode）轮转调度测试：验证 2026-09-19 引入的 pickBridgeRotation
// 不破坏粘性路由的其他渠道、不吃掉禁用账号、并发计数器推进正确。

import (
	"testing"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/pool"
	"zerogate/internal/provider"
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

// 面板「优先调度」预览必须与真实轮转同源，且只读（不推进计数器）。
// 修正前面板用 Pool.Pick()（快到期+低余额），与 zcode 实际轮转顺序不符。
func TestPickBridgeRotationNextMatchesActualRotation(t *testing.T) {
	p := pool.New(t.TempDir() + "/state.json")
	for _, uid := range []string{"zcode-big", "zcode-fuka", "zcode-fuka2"} {
		p.Add(&auth.Auth{UID: uid, Kind: "zcode", AccessToken: "bridge"})
	}
	p.SetExpire("zcode-big", time.Now().Add(16*time.Hour).Unix())
	p.SetExpire("zcode-fuka", time.Now().Add(7*time.Hour).Unix())
	p.SetExpire("zcode-fuka2", time.Now().Add(7*time.Hour).Unix())

	rt := &Runtime{Kind: provider.ZCode, Pool: p}

	// 只读：连读两次必须是同一个号（若推进计数则第二次会变）。
	a := rt.PickBridgeRotationNext()
	b := rt.PickBridgeRotationNext()
	if a == nil || b == nil {
		t.Fatalf("预览返回 nil: a=%v b=%v", a, b)
	}
	if a.UID != b.UID {
		t.Fatalf("预览不应推进计数: 第一次=%s 第二次=%s", a.UID, b.UID)
	}
	// 首顺位 = 到期最早（fuka 与 fuka2 同到期，UID 升序 → fuka）
	if a.UID != "zcode-fuka" {
		t.Fatalf("首顺位 = %s, want zcode-fuka", a.UID)
	}

	// 预览必须与实际调度逐次吻合（sorted: fuka, fuka2, big）
	want := []string{"zcode-fuka", "zcode-fuka2", "zcode-big", "zcode-fuka"}
	for i, w := range want {
		preview := rt.PickBridgeRotationNext()
		actual := rt.pickBridgeRotation()
		if preview == nil || actual == nil || preview.UID != actual.UID {
			t.Fatalf("第%d次 预览与实值不一致", i)
		}
		if actual.UID != w {
			t.Fatalf("第%d次 实际=%s, want %s", i, actual.UID, w)
		}
	}
}

// 非 zcode 渠道仍走 Pool.Pick()（快到期/低余额），不被桥接轮转影响。
func TestPickNextPreviewNonBridgeUsesPoolPick(t *testing.T) {
	p := pool.New(t.TempDir() + "/state.json")
	for _, uid := range []string{"wb-a", "wb-b"} {
		p.Add(&auth.Auth{UID: uid, Kind: "workbuddy", AccessToken: "t"})
	}
	p.SetExpire("wb-b", time.Now().Add(2*time.Hour).Unix()) // 更早到期 → 应被 Pick 选中
	rt := &Runtime{Kind: provider.WorkBuddy, Pool: p}
	h := NewHandler(Config{Runtimes: map[provider.Kind]*Runtime{provider.WorkBuddy: rt}})

	got := h.PickNextPreview(provider.WorkBuddy)
	if got == nil || got.UID != "wb-b" {
		t.Fatalf("非桥接渠道应走 Pool.Pick（快到期优先）: %v", got)
	}
	if h.PickNextPreview(provider.Qoder) != nil {
		t.Fatal("未注册渠道应返回 nil")
	}
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
