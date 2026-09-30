package server

// 「本地桥不可达 ≠ 账号故障」回归测试（2026-09-30 zcode 事故）。
//
// 事故现场：21:41 zcode 五个桥进程全没在跑，转发打到 127.0.0.1:18083-18087
// 全被 connection refused。handler 的 transport error 分支原先无差别调
// NoteError，于是 5 个账号各吃 3 次错误 → 整池被罚 10 分钟长冷却
// （余额 85M~98M 明明健康），紧接着渠道熔断 0/5 healthy 反复 trip / open。
//
// 修法：本地桥错误由 producer 打上 provider.ErrBridgeDown 标记，handler 据此
// 只给一次短冷却（让整池不可用时熔断仍能快速失败），绝不累计 errCount。
// 本文件锁定三件事：
//   1. zcode 桥 / openaicompat 本地桥连不上时确实产出 BridgeDown 标记；
//   2. 云端渠道（非 loopback）的同类错误不会被误标；
//   3. 桥挂时账号池不会被长冷却冻死（对比旧的 NoteError 行为）。

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"

	"zerogate/internal/auth"
	"zerogate/internal/openaicompat"
	"zerogate/internal/pool"
	"zerogate/internal/provider"
	"zerogate/internal/zcode"
)

// freePort 返回一个当前没人监听的本地端口（绑 :0 拿到后立刻释放）。
// 用它构造必然 connection refused 的本地桥地址，无需任何 mock。
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("取空闲端口失败: %v", err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// isBridgeDown 复刻 handler transport 分支的判定条件。
func isBridgeDown(err error) bool {
	var ue *provider.Error
	return errors.As(err, &ue) && ue.Kind == provider.ErrBridgeDown
}

// zcode 桥连不上 → 标记为 BridgeDown（而不是普通 transport error）。
func TestZcodeBridgeUnreachableIsBridgeDown(t *testing.T) {
	port := freePort(t)
	c := &zcode.Client{
		BridgeURL: "http://127.0.0.1:" + strconv.Itoa(port),
		HTTP:      &http.Client{Timeout: 3 * time.Second},
	}
	_, _, _, err := c.ChatStream(&auth.Auth{UID: "zcode-test"}, []byte(`{"model":"glm-5.3-flash"}`))
	if err == nil {
		t.Fatal("桥端口没人听，应当返回 transport error")
	}
	if !isBridgeDown(err) {
		t.Fatalf("zcode 桥不可达应标记 ErrBridgeDown，实际 err=%v", err)
	}
}

// openaicompat 本地桥（copilot/doubao/gptgemini 同款客户端）连不上 → BridgeDown。
func TestOpenAICompatLoopbackUnreachableIsBridgeDown(t *testing.T) {
	port := freePort(t)
	c := openaicompat.NewWithKey("http://127.0.0.1:"+strconv.Itoa(port), "", "")
	_, _, _, err := c.ChatStream(&auth.Auth{UID: "copilot-test"}, []byte(`{"model":"x"}`))
	if err == nil {
		t.Fatal("桥端口没人听，应当返回 transport error")
	}
	if !isBridgeDown(err) {
		t.Fatalf("本地桥不可达应标记 ErrBridgeDown，实际 err=%v", err)
	}
}

// 云端渠道（非 loopback）不会被误标——否则线上波动也会走错分支。
func TestRemoteUpstreamNotTreatedAsBridgeDown(t *testing.T) {
	for _, u := range []string{
		"https://api.groq.com/openai/v1",
		"https://open.bigmodel.cn/api/paas/v4",
		"https://tierflow.cn/v1",
		"http://127.0.0.1.nip.io:8080",      // 子串含 127.0.0.1 但实为公网域名
		"http://localhost.example.com:8080", // 子串含 localhost 同上
	} {
		if provider.IsLoopbackURL(u) {
			t.Errorf("%s 不该被判为本地桥", u)
		}
	}
	for _, u := range []string{
		"http://127.0.0.1:18083",
		"http://localhost:7868",
		"http://[::1]:4141",
	} {
		if !provider.IsLoopbackURL(u) {
			t.Errorf("%s 应被判为本地桥", u)
		}
	}
}

// 桥挂 3 次不得把账号冻死：新逻辑（短冷却）过后账号自动恢复健康，
// 旧逻辑（NoteError 累计到阈值）则会被长冷却冻住——这正是事故的两种走向。
func TestBridgeDownDoesNotFreezeAccount(t *testing.T) {
	const soft = 300 * time.Millisecond
	const hard = 10 * time.Minute
	uid := "zcode-big"

	// 新逻辑：本地桥不可达 → CoolSoft 短冷却，不累计 errCount
	pNew := pool.New(t.TempDir() + "/state.json")
	pNew.Add(&auth.Auth{UID: uid, Kind: "zcode", AccessToken: "tok"})
	for i := 0; i < 3; i++ {
		pNew.Cooldown(uid, pool.CoolSoft, soft, "本地桥不可达（非账号问题）")
	}
	time.Sleep(soft + 100*time.Millisecond)
	if healthy, total := pNew.HealthyCount(); healthy != 1 || total != 1 {
		t.Fatalf("短冷却到期后账号应恢复健康，实际 %d/%d", healthy, total)
	}

	// 旧逻辑（对照组）：3 次 NoteError 达阈值 → 长冷却，账号持续不健康
	pOld := pool.New(t.TempDir() + "/state.json")
	pOld.Add(&auth.Auth{UID: uid, Kind: "zcode", AccessToken: "tok"})
	for i := 0; i < 3; i++ {
		pOld.NoteError(uid, 3, hard)
	}
	if healthy, _ := pOld.HealthyCount(); healthy != 0 {
		t.Fatalf("阈值 3 次错误应触发长冷却（事故现场行为），实际 healthy=%d", healthy)
	}
}
