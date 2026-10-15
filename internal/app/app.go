// Package app 业务编排层：HTTP 管理 API + 托盘动作 + 登录编排 + 日志。
// 由 daemon 入口（cmd/zerogate）装配，替代旧版 wails 绑定层。
package app

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"zerogate/internal/audit"
	"zerogate/internal/auth"
	"zerogate/internal/autoclaw"
	"zerogate/internal/bridge"
	"zerogate/internal/clientkey"
	"zerogate/internal/config"
	"zerogate/internal/doubao"
	"zerogate/internal/login"
	loginqoder "zerogate/internal/login_qoder"
	logintrae "zerogate/internal/login_trae"
	"zerogate/internal/platform"
	"zerogate/internal/pool"
	"zerogate/internal/provider"
	"zerogate/internal/qoder"
	"zerogate/internal/scheduler"
	"zerogate/internal/server"
	"zerogate/internal/upstream"
	"zerogate/internal/usage"
	"zerogate/internal/zcode"
)

// Version 版本号。
const Version = "2.0.1"

const (
	loginTimeout   = 5 * time.Minute
	loginPollEvery = 2 * time.Second
)

// Runtime 一个渠道的运行时资源。
type Runtime struct {
	Kind      provider.Kind
	Pool      *pool.Pool
	Upstream  provider.Upstream
	Scheduler *scheduler.Scheduler
}

// Options 构建 App 的依赖。
type Options struct {
	ConfigPath string
	Config     *config.Config
	Runtimes   map[provider.Kind]*Runtime
	Handler    *server.Handler
	ClientKeys *clientkey.Store
	AuditRec   *audit.Recorder
}

// App 应用编排。
type App struct {
	cfgPath  string
	cfg      *config.Config
	runtimes map[provider.Kind]*Runtime
	handler  *server.Handler

	clientKeys *clientkey.Store
	auditRec   *audit.Recorder

	mu      sync.Mutex // 保护 httpSrv / dashSrv / cfg 修改
	httpSrv *http.Server
	// dashSrv 只读 Token 大屏专用监听（cfg.ListenDashboard 启用时非空）。
	// 与主监听隔离：仅暴露大屏读端点 + 静态 UI，不含管理 API。
	dashSrv *http.Server

	muLogin       sync.Mutex
	loginBusy     bool
	loginCtx      context.Context
	loginCancel   context.CancelFunc
	loginClient   *http.Client
	loginStateFP  string
	loginKind     provider.Kind
	zcProv        *zcode.Provision // zcode 面板自助加号编排器
	pendingZcProv string           // login/start 请求里的 zcode provider（zai|bigmodel）
	pricingFP     string

	logFile *os.File

	refreshMu  sync.Mutex // 防并发刷新积分
	refreshing bool

	pricingMu      sync.Mutex
	pricingCache   []provider.ModelPricing // 本地缓存
	pricingFetched time.Time
	pricingErr     string // 最近一次拉取错误

	usage *usage.Recorder
	stop  chan struct{} // 关闭周期任务

	// growthHistoryFP 成长中心历史记录文件路径
	growthHistoryFP string
	// growthHistory 成长中心历史记录管理器（懒加载）
	growthHistory *upstream.GrowthHistory

	// bridges 本地桥（豆包桥 / zcode 实例）编排器
	bridges *bridge.Manager
}

// New 构建 App 并接管全局日志（写文件 + 环形缓冲）。
func New(opts Options) (*App, error) {
	a := &App{
		cfgPath:    opts.ConfigPath,
		cfg:        opts.Config,
		runtimes:   opts.Runtimes,
		handler:    opts.Handler,
		clientKeys: opts.ClientKeys,
		auditRec:   opts.AuditRec,
	}
	a.loginStateFP = filepath.Join(filepath.Dir(opts.Config.StateFile), "login-state.json")
	// zcode 自助加号：实例编排（默认目录约定见 AGENTS.md 7.6；用 env 可覆盖）
	a.zcProv = zcode.NewProvision(
		os.Getenv("ZCODE_PROXY_EXE"),
		os.Getenv("ZCODE_PROXY_INSTANCES_DIR"),
		opts.Config.AuthDir,
		18086,
	)
	a.pricingFP = filepath.Join(filepath.Dir(opts.Config.StateFile), "pricing-cache.json")
	a.usage = usage.New(filepath.Join(filepath.Dir(opts.Config.StateFile), "usage.csv"))
	a.growthHistoryFP = filepath.Join(filepath.Dir(opts.Config.StateFile), "growth-history.json")
	a.stop = make(chan struct{})

	// 本地桥编排：豆包视觉桥 + zcode-proxy 实例（面板「本地桥」Tab）
	zcode.SetClaimResultFile(filepath.Join(filepath.Dir(opts.Config.StateFile), "zcode-claim-result.json"))
	a.bridges = bridge.NewManager(bridge.Config{
		ZcodeDir:  os.Getenv("ZCODE_PROXY_ROOT"),
		DoubaoDir: os.Getenv("DOUBAO_BRIDGE_DIR"),
		DoubaoKey: "DoubaoAPI",
		BridgeKey: zcode.BridgeKeyFromEnv(),
	})

	// 日志文件 data/app.log
	logFP := filepath.Join(filepath.Dir(opts.Config.StateFile), "app.log")
	if err := os.MkdirAll(filepath.Dir(logFP), 0o755); err == nil {
		f, err := os.OpenFile(logFP, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
		if err == nil {
			a.logFile = f
		}
	}
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.SetOutput(&logWriter{app: a})
	a.loadPricingCache()
	go a.usageLoop()
	return a, nil
}

// usageLoop 周期记录各账号实时余额快照（30 分钟一次，与调度器错开）。
func (a *App) usageLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	a.captureUsage() // 启动先记一次
	for {
		select {
		case <-ticker.C:
			a.captureUsage()
		case <-a.stop:
			return
		}
	}
}

// captureUsage 拉取全部账号实时余额并写入 Recorder + 同步签到状态。
func (a *App) captureUsage() {
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil || rt.Upstream == nil {
			continue
		}
		for _, st := range rt.Pool.List() {
			au := rt.Pool.AuthByUID(st.UID)
			if au == nil || au.AccessToken == "" {
				continue
			}
			remain, total := st.Credits, st.Credits
			var expireAt int64
			if r, items, err := rt.Upstream.UserResourceDetail(au); err == nil {
				remain = r
				expAt, expRemain, expName := provider.EarliestExpireDetail(items)
				expireAt = expAt
				var sum int64
				for _, it := range items {
					sum += it.Total
				}
				if sum > 0 {
					total = sum
				}
				rt.Pool.SetCreditsExpire(st.UID, remain, expireAt)
				rt.Pool.SetResourceItems(st.UID, items)
				if a.usage != nil {
					a.usage.SetExpireDetail(st.UID, expireAt, expRemain, expName)
				}
			}
			a.usage.Record(rt.Kind.String(), st.UID, st.Nickname, remain, total)
			a.usage.SetCheckin(st.UID, st.LastCheckinOK, fmtTime(st.LastCheckinAt), st.LastCheckinMsg)
		}
	}
}

// StartAutostartBridges 拉起「本地桥」中标记 Autostart 的常驻服务（后台，非阻塞）。
//
// 收编原先散落在注册表 Run 键里的 .bat 自启项：那些脚本各弹一个 cmd 窗口，
// 误关窗口会连带杀掉服务。改由 daemon 单点隐藏拉起后，托盘是唯一入口。
func (a *App) StartAutostartBridges(ctx context.Context) {
	if a.bridges == nil {
		return
	}
	a.bridges.StartAutostart(ctx)
}

// Close 关闭日志文件与后台任务。
func (a *App) Close() {
	if a.stop != nil {
		select {
		case <-a.stop:
		default:
			close(a.stop)
		}
	}
	if a.logFile != nil {
		_ = a.logFile.Close()
		a.logFile = nil
	}
}

func (a *App) runtime(kind provider.Kind) *Runtime {
	if a.runtimes == nil {
		return nil
	}
	return a.runtimes[kind]
}

func (a *App) firstRuntime() *Runtime {
	for _, k := range []provider.Kind{provider.WorkBuddy, provider.TraeWork, provider.Qoder, provider.CodeBuddy} {
		if rt := a.runtime(k); rt != nil {
			return rt
		}
	}
	return nil
}

func (a *App) totalAccounts() int {
	n := 0
	for _, rt := range a.runtimes {
		if rt != nil && rt.Pool != nil {
			n += len(rt.Pool.List())
		}
	}
	return n
}

// ArsenalOverview 聚合各本地服务状态供前端「白嫖总览」动态渲染（尽力而为，失败标 offline）。
func (a *App) ArsenalOverview() map[string]any {
	fetchJSON := func(url, key string, out any) bool {
		req, err := http.NewRequest(http.MethodGet, url, nil)
		if err != nil {
			return false
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		cli := &http.Client{Timeout: 4 * time.Second}
		resp, err := cli.Do(req)
		if err != nil {
			return false
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return json.Unmarshal(raw, out) == nil
	}
	var m8317 struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	ok8317 := fetchJSON("http://127.0.0.1:8317/v1/models", "CodexAPI", &m8317)
	var m4141 struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	ok4141 := fetchJSON("http://127.0.0.1:4141/v1/models", "sk-7199d8a2-72e9-4785-9904-a45aac9594dc", &m4141)
	var stats7868 map[string]any
	ok7868 := fetchJSON("http://127.0.0.1:7868/stats", "DoubaoAPI", &stats7868)
	return map[string]any{
		"wildwork": map[string]any{
			"channels": len(a.runtimes), "accounts": a.totalAccounts(),
		},
		"gpt_gemini": map[string]any{"online": ok8317, "models": len(m8317.Data)},
		"copilot":    map[string]any{"online": ok4141, "models": len(m4141.Data), "model": "auto"},
		"doubao":     map[string]any{"online": ok7868, "stats": stats7868},
	}
}

func (a *App) allStatuses() []pool.Status {
	out := []pool.Status{}
	for _, rt := range a.runtimes {
		if rt != nil && rt.Pool != nil {
			out = append(out, rt.Pool.List()...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out
}

func (a *App) findRuntimeAuth(uid string) (*Runtime, *auth.Auth) {
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil {
			continue
		}
		if au := rt.Pool.AuthByUID(uid); au != nil {
			return rt, au
		}
	}
	return nil, nil
}

func (a *App) checkinTimes() []string {
	if rt := a.firstRuntime(); rt != nil && rt.Scheduler != nil {
		return rt.Scheduler.CheckinTimes()
	}
	return nil
}

func (a *App) keepaliveHours() []int {
	if rt := a.firstRuntime(); rt != nil && rt.Scheduler != nil {
		return rt.Scheduler.KeepaliveHours()
	}
	return nil
}

func (a *App) nextFire() time.Time {
	if rt := a.firstRuntime(); rt != nil && rt.Scheduler != nil {
		return rt.Scheduler.NextFire()
	}
	return time.Time{}
}

// growthTimes 成长中心执行时间（HH:MM 列表）。
func (a *App) growthTimes() []string {
	if rt := a.firstRuntime(); rt != nil && rt.Scheduler != nil {
		return rt.Scheduler.GrowthTimes()
	}
	return nil
}

// nextGrowthFire 最近的成长中心执行时间。
func (a *App) nextGrowthFire() time.Time {
	if rt := a.firstRuntime(); rt != nil && rt.Scheduler != nil {
		return rt.Scheduler.NextGrowthFire()
	}
	return time.Time{}
}

// SetHandler 注入 HTTP handler（在 HandleAPI 注册后调用）。
func (a *App) SetHandler(h *server.Handler) {
	a.handler = h
	// 注入 Token 用量回调：反代转发成功后记录实际消耗（按天聚合到 usage）。
	if h != nil {
		h.SetTokenRecorder(func(kind, uid, model string, prompt, completion, cacheRead, cacheWrite int64) {
			a.usage.RecordTokens(kind, uid, model, prompt, completion, cacheRead, cacheWrite)
		})
		// 注入 UsageRecorder 供 /api/token_usage 查询累计用量
		h.SetUsageRecorder(a.usage)
	}
}

// UsageRecorder 返回 token 用量记录器，供只读大屏 handler 注入（否则 /api/token_usage 返回 501）。
func (a *App) UsageRecorder() any { return a.usage }

// ---------------------------------------------------------------------------
// HTTP 服务
// ---------------------------------------------------------------------------

// StartServer 按当前配置启动 HTTP 服务。
func (a *App) StartServer() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.serveLocked(a.cfg.Listen.Addr())
}

// serveLocked 在 newAddr 上启动新服务并切换；调用方需持有 a.mu。
func (a *App) serveLocked(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           a.handler,
		ReadHeaderTimeout: 30 * time.Second,
	}
	old := a.httpSrv
	a.httpSrv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("http serve %s: %v", addr, err)
		}
	}()
	if old != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = old.Shutdown(shutdownCtx)
	}
	return nil
}

// Stop 停止 HTTP 服务（退出时调用）。
func (a *App) Stop() {
	a.mu.Lock()
	srv := a.httpSrv
	a.httpSrv = nil
	dash := a.dashSrv
	a.dashSrv = nil
	a.mu.Unlock()
	if srv != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}
	if dash != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = dash.Shutdown(shutdownCtx)
	}
	a.Close()
}

// StartDashboardServer 在 cfg.ListenDashboard 上启动只读 Token 大屏监听。
// 使用调用方传入的只读 handler（server.Config.ReadOnly=true，仅大屏读端点 + 静态 UI），
// 与主监听完全隔离：不触碰主 httpSrv，因此不会中断任何在途会话。
// 未配置 ListenDashboard（nil 或 Port<=0）时直接返回 nil。
func (a *App) StartDashboardServer(h *server.Handler) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.ListenDashboard == nil || a.cfg.ListenDashboard.Port <= 0 {
		return nil
	}
	addr := a.cfg.ListenDashboard.Addr()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler:           h,
		ReadHeaderTimeout: 30 * time.Second,
	}
	old := a.dashSrv
	a.dashSrv = srv
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("dashboard serve %s: %v", addr, err)
		}
	}()
	if old != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = old.Shutdown(shutdownCtx)
	}
	return nil
}

// Quit 退出整个程序（供外部显式调用；Web UI 已无对应按钮）。
func (a *App) Quit() {
	a.Stop()
	os.Exit(0)
}

// ---------------------------------------------------------------------------
// 账号操作
// ---------------------------------------------------------------------------

// StartLoginFor 发起指定渠道登录：workbuddy / traework / qoder。
func (a *App) StartLoginFor(kind string) (string, error) {
	k := provider.Kind(strings.TrimSpace(kind))
	if k == "" {
		k = provider.WorkBuddy
	}
	if k != provider.WorkBuddy && k != provider.TraeWork && k != provider.Qoder && k != provider.ZCode &&
		k != provider.CodeBuddy {
		return "", fmt.Errorf("unknown login provider %s", kind)
	}
	if k == provider.ZCode {
		return a.startZcodeLogin()
	}
	a.muLogin.Lock()
	if a.loginBusy {
		a.muLogin.Unlock()
		return "", errors.New("已有登录流程进行中，请先完成或取消")
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.loginCtx, a.loginCancel = ctx, cancel
	a.loginKind = k
	switch k {
	case provider.TraeWork:
		a.loginClient = logintrae.NewClient()
	case provider.Qoder:
		a.loginClient = loginqoder.NewClient()
	default:
		a.loginClient = login.NewClient()
	}
	a.loginBusy = true
	a.muLogin.Unlock()

	var authURL string
	var err error
	switch k {
	case provider.TraeWork:
		authURL, err = logintrae.Start(a.loginClient, a.loginStateFP)
	case provider.Qoder:
		authURL, err = loginqoder.Start(a.loginClient, a.loginStateFP)
	case provider.CodeBuddy, provider.WBIntl:
		// 国际版 WorkBuddy / CodeBuddy 同源共用账号，统一走 workbuddy.ai 的 OAuth 设备流
		authURL, err = login.StartIntl(a.loginClient, a.loginStateFP)
		if err == nil {
			if resolved, rerr := login.ResolveAuthURLIntl(a.loginClient, authURL); rerr == nil && resolved != "" {
				authURL = resolved
			}
		}
	default:
		authURL, err = login.Start(a.loginClient, a.loginStateFP)
		if err == nil {
			if resolved, rerr := login.ResolveAuthURL(a.loginClient, authURL); rerr == nil && resolved != "" {
				authURL = resolved
			}
		}
	}
	if err != nil {
		a.finishLogin()
		return "", err
	}
	// 浏览器打开由前端 Web UI 通过 window.open() 完成
	go a.pollLogin(ctx)
	log.Printf("%s 登录流程已发起", k)
	return authURL, nil
}

// jwtIDName 从 JWT payload（base64url 中段）尽量解出 uid 与昵称。
func jwtIDName(token string) (uid, name string) {
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload := parts[1]
	if padded := payload + strings.Repeat("=", (4-len(payload)%4)%4); padded != payload {
		payload = padded
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		if raw2, err2 := base64.URLEncoding.DecodeString(payload); err2 == nil {
			raw = raw2
		} else {
			return "", ""
		}
	}
	var claims map[string]any
	if err := json.Unmarshal(raw, &claims); err != nil {
		return "", ""
	}
	for _, k := range []string{"sub", "uid", "userId", "user_id", "uuid"} {
		if v, ok := claims[k].(string); ok && v != "" {
			uid = v
			break
		}
	}
	for _, k := range []string{"nickname", "name", "userName", "username", "displayName"} {
		if v, ok := claims[k].(string); ok && v != "" {
			name = v
			break
		}
	}
	if uid == "" {
		if v, ok := claims["user_id"].(float64); ok {
			uid = fmt.Sprintf("%.0f", v)
		}
	}
	return uid, name
}

// autoclawClient 返回 autoclaw 渠道上游客户端。
func (a *App) autoclawClient() (*autoclaw.Client, bool) {
	rt := a.runtime(provider.AutoClaw)
	if rt == nil || rt.Upstream == nil {
		return nil, false
	}
	ac, ok := rt.Upstream.(*autoclaw.Client)
	return ac, ok
}

// newDeviceIDForAutoclaw 生成 autoclaw 登录设备 UUID。
func newDeviceIDForAutoclaw() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}

// startZcodeLogin 面板自助加号：编排实例 + 发起 auth login，返回授权 URL。
// 完成后台 goroutine 等凭证 → Finalize 落库 → 重载账号池。
func (a *App) startZcodeLogin() (string, error) {
	a.muLogin.Lock()
	if a.loginBusy {
		a.muLogin.Unlock()
		return "", errors.New("已有登录流程进行中，请先完成或取消")
	}
	a.loginBusy = true
	a.loginKind = provider.ZCode
	a.muLogin.Unlock()

	a.muLogin.Lock()
	provider := strings.TrimSpace(a.pendingZcProv)
	a.pendingZcProv = ""
	a.muLogin.Unlock()
	if provider == "" {
		provider = "zai"
	}
	url, err := a.zcProv.Start(provider)
	if err != nil {
		a.finishLogin()
		return "", err
	}
	go func() {
		ok, derr := a.zcProv.Done(5 * time.Minute)
		if !ok {
			log.Printf("zcode 登录未完成: %v", derr)
			a.finishLogin()
			return
		}
		fp, ferr := a.zcProv.Finalize()
		if ferr != nil {
			log.Printf("zcode 登录落库失败: %v", ferr)
			a.finishLogin()
			return
		}
		log.Printf("zcode 新账号已落库: %s", filepath.Base(fp))
		a.reloadAccounts()
		a.finishLogin()
	}()
	return url, nil
}

// CancelLogin 取消当前登录流程。
func (a *App) CancelLogin() error {
	a.muLogin.Lock()
	kind := a.loginKind
	cancel := a.loginCancel
	a.muLogin.Unlock()
	if kind == provider.ZCode {
		if a.zcProv == nil {
			return errors.New("没有进行中的登录")
		}
		err := a.zcProv.Abort()
		a.finishLogin()
		log.Printf("zcode 登录已取消")
		return err
	}
	if cancel == nil {
		return errors.New("没有进行中的登录")
	}
	cancel()
	_ = os.Remove(a.loginStateFP)
	log.Printf("登录已取消")
	return nil
}

// pollLogin 后台轮询登录结果，写日志。
func (a *App) pollLogin(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("login poll panic: %v", r)
		}
		a.finishLogin()
	}()
	deadline := time.Now().Add(loginTimeout)
	log.Printf("登录流程已发起，等待浏览器完成…")
	t := time.NewTicker(loginPollEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Printf("登录已取消")
			return
		case <-t.C:
		}
		if time.Now().After(deadline) {
			log.Printf("登录超时，请重新发起")
			return
		}
		if a.loginKind == provider.TraeWork {
			r, err := logintrae.Poll(a.loginClient, a.loginStateFP)
			if err == nil {
				a.completeTraeLogin(r)
				return
			}
			if !errors.Is(err, logintrae.ErrPending) {
				log.Printf("trae login poll failed: %v", err)
			}
			continue
		}
		if a.loginKind == provider.Qoder {
			r, err := loginqoder.Poll(a.loginClient, a.loginStateFP)
			if err == nil {
				a.completeQoderLogin(r)
				return
			}
			if !errors.Is(err, loginqoder.ErrPending) {
				log.Printf("qoder login poll failed: %v", err)
			}
			continue
		}
		if a.loginKind == provider.WBIntl || a.loginKind == provider.CodeBuddy {
			r, err := login.PollIntl(a.loginClient, a.loginStateFP)
			if err == nil {
				a.completeWbintlLogin(a.loginKind, r)
				return
			}
			if !errors.Is(err, login.ErrPending) {
				log.Printf("%s login poll failed: %v", a.loginKind, err)
			}
			continue
		}
		r, err := login.Poll(a.loginClient, a.loginStateFP)
		if err == nil {
			a.completeLogin(r)
			return
		}
		if !errors.Is(err, login.ErrPending) {
			log.Printf("workbuddy login poll failed: %v", err)
		}
	}
}

// completeLogin 登录成功：写 auth 文件、重载账号池，异步签到。
func (a *App) completeLogin(r login.Result) {
	log.Printf("workbuddy 登录成功 uid=%s nickname=%s expires_in=%d refresh_token=%t", r.UID, r.Nickname, r.ExpiresIn, r.RefreshToken != "")
	fp, err := login.SaveAuth(a.cfg.AuthDir, r)
	if err != nil {
		log.Printf("workbuddy 登录保存凭证失败 uid=%s err=%v", r.UID, err)
		return
	}
	log.Printf("workbuddy 登录凭证已保存 uid=%s file=%s", r.UID, filepath.Base(fp))
	a.reloadAccounts()
	a.finishLogin()
	a.safeGo(func() {
		rt := a.runtime(provider.WorkBuddy)
		if rt == nil || rt.Scheduler == nil {
			return
		}
		if res, err := rt.Scheduler.CheckinAccount(r.UID); err != nil {
			log.Printf("新账号签到失败 %s: %v", r.Nickname, err)
		} else {
			log.Printf("新账号签到完成 %s：%s", r.Nickname, res.Msg)
		}
	})
}

func (a *App) completeTraeLogin(r logintrae.Result) {
	log.Printf("traework 登录成功 uid=%s nickname=%s expires_at=%d refresh_token=%t", r.UID, r.Nickname, r.ExpiresAt, r.RefreshToken != "")
	fp, err := logintrae.SaveAuth(a.cfg.AuthDir, r)
	if err != nil {
		log.Printf("traework 登录保存凭证失败 uid=%s err=%v", r.UID, err)
		return
	}
	log.Printf("traework 登录凭证已保存 uid=%s file=%s", r.UID, filepath.Base(fp))
	a.reloadAccounts()
	a.finishLogin()
	a.safeGo(func() {
		rt := a.runtime(provider.TraeWork)
		if rt == nil || rt.Scheduler == nil {
			return
		}
		if res, err := rt.Scheduler.CheckinAccount(r.UID); err != nil {
			log.Printf("TraeWork 新账号签到失败 %s: %v", r.Nickname, err)
		} else {
			log.Printf("TraeWork 新账号签到完成 %s：%s", r.Nickname, res.Msg)
		}
	})
}

// completeWbintlLogin 国际版登录成功：落盘 <kind>-<uid>.json + 重载账号池。
// kind 为 wbintl（www.workbuddy.ai）或 codebuddy（www.codebuddy.ai）；二者同源共用账号。
func (a *App) completeWbintlLogin(kind provider.Kind, r login.Result) {
	domain := "www.workbuddy.ai"
	if kind == provider.CodeBuddy {
		domain = "www.codebuddy.ai"
	} else if d := strings.TrimSpace(r.Domain); d != "" {
		// wbintl：上游返回的 domain 优先（形如 workbuddy.ai），补 www. 前缀；
		// codebuddy 固定用自身域名，避免与 codebuddy.ai base 不一致
		if !strings.HasPrefix(d, "www.") {
			d = "www." + d
		}
		domain = d
	}
	log.Printf("%s 登录成功 uid=%s nickname=%s domain=%s", kind, r.UID, r.Nickname, domain)
	au := &auth.Auth{
		Kind:         string(kind),
		AccessToken:  r.AccessToken,
		RefreshToken: r.RefreshToken,
		Domain:       domain,
		UID:          r.UID,
		Nickname:     r.Nickname,
	}
	if r.ExpiresIn > 0 {
		au.ExpiresAt = time.Now().Add(time.Duration(r.ExpiresIn) * time.Second).Unix()
	}
	au.FilePath = filepath.Join(a.cfg.AuthDir, fmt.Sprintf("%s-%s.json", kind, r.UID))
	if err := au.SaveAtomic(); err != nil {
		log.Printf("%s 登录保存凭证失败 uid=%s err=%v", kind, r.UID, err)
		return
	}
	log.Printf("%s 登录凭证已保存 uid=%s file=%s", kind, r.UID, filepath.Base(au.FilePath))
	a.reloadAccounts()
	a.finishLogin()
}

// completeQoderLogin 登录成功：生成机器指纹、写 auth 文件、重载账号池、首次签到。
func (a *App) completeQoderLogin(r loginqoder.Result) {
	log.Printf("qoder 登录成功 uid=%s nickname=%s expires_in=%d refresh_token=%t", r.UID, r.Nickname, r.ExpiresIn, r.RefreshToken != "")
	au := &auth.Auth{Kind: "qoder", AccessToken: r.AccessToken, RefreshToken: r.RefreshToken, UID: r.UID, Nickname: r.Nickname}
	qoder.EnsureFingerprint(au)
	fp, err := loginqoder.SaveAuth(a.cfg.AuthDir, r, au.MachineID, au.MachineToken, au.MachineType)
	if err != nil {
		log.Printf("qoder 登录保存凭证失败 uid=%s err=%v", r.UID, err)
		return
	}
	log.Printf("qoder 登录凭证已保存 uid=%s file=%s", r.UID, filepath.Base(fp))
	a.reloadAccounts()
	a.finishLogin()
	a.safeGo(func() {
		rt := a.runtime(provider.Qoder)
		if rt == nil || rt.Scheduler == nil {
			return
		}
		if res, err := rt.Scheduler.CheckinAccount(r.UID); err != nil {
			log.Printf("Qoder 新账号签到失败 %s: %v", r.Nickname, err)
		} else {
			log.Printf("Qoder 新账号签到完成 %s：%s", r.Nickname, res.Msg)
		}
	})
}

func (a *App) finishLogin() {
	a.muLogin.Lock()
	a.loginBusy = false
	a.loginCtx, a.loginCancel, a.loginClient = nil, nil, nil
	a.loginKind = ""
	a.muLogin.Unlock()
}

// reloadAccounts 用 auths 目录最新文件对齐账号池。
func (a *App) reloadAccounts() {
	if rt := a.runtime(provider.WorkBuddy); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadWorkBuddyDir(a.cfg.AuthDir, a.cfg.Region)
		if err != nil {
			log.Printf("reload workbuddy accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.TraeWork); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadTraeDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload traework accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.Qoder); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadQoderDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload qoder accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.CodeBuddy); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadCodeBuddyDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload codebuddy accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.ZCode); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadZCodeDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload zcode accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.AutoClaw); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadAutoClawDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload autoclaw accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.Atria); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadAtriaDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload atria accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.Copilot); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadCopilotDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload copilot accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.Doubao); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadDoubaoDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload doubao accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.GptGemini); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadGptGeminiDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload gptgemini accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.GlmFlash); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadOpenAICompatDir(a.cfg.AuthDir, "glmflash")
		if err != nil {
			log.Printf("reload glmflash accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.Groq); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadOpenAICompatDir(a.cfg.AuthDir, "groq")
		if err != nil {
			log.Printf("reload groq accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
	if rt := a.runtime(provider.Tierflow); rt != nil && rt.Pool != nil {
		auths, err := auth.LoadTierflowDir(a.cfg.AuthDir)
		if err != nil {
			log.Printf("reload tierflow accounts: %v", err)
		} else {
			rt.Pool.SyncToDir(auths)
		}
	}
}

// kindNoCheckin 无签到活动的渠道：无网页签到体系渠道 + OpenAI 兼容 key 型渠道。
func kindNoCheckin(k provider.Kind) bool {
	switch k {
	case provider.CodeBuddy, provider.ZCode, provider.Atria,
		provider.Copilot, provider.Doubao, provider.GptGemini, provider.GlmFlash, provider.Groq,
		provider.Tierflow:
		return true
	}
	return false
}

// CheckinAccount 单个账号立即签到。
func (a *App) CheckinAccount(uid string) (scheduler.CheckinResult, error) {
	rt, _ := a.findRuntimeAuth(uid)
	if rt == nil || rt.Scheduler == nil {
		return scheduler.CheckinResult{}, fmt.Errorf("unknown account %s", uid)
	}
	if kindNoCheckin(rt.Kind) {
		return scheduler.CheckinResult{}, fmt.Errorf("%s 渠道无签到活动", rt.Kind)
	}
	res, err := rt.Scheduler.CheckinAccount(uid)
	if err != nil {
		log.Printf("checkin failed uid=%s err=%v", uid, err)
		return res, err
	}
	log.Printf("checkin uid=%s ok=%t msg=%s remain=%d has_remain=%t", uid, res.OK, res.Msg, res.Remain, res.HasRemain)
	return res, nil
}

// CheckinAll 全部账号立即签到。
func (a *App) CheckinAll() []scheduler.CheckinResult {
	results := make([]scheduler.CheckinResult, 0)
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil || rt.Scheduler == nil {
			continue
		}
		if kindNoCheckin(rt.Kind) { // 无签到活动，跳过
			continue
		}
		for _, st := range rt.Pool.List() {
			if st.Disabled {
				results = append(results, scheduler.CheckinResult{UID: st.UID, Msg: "账号已禁用"})
				continue
			}
			res, err := rt.Scheduler.CheckinAccount(st.UID)
			if err != nil {
				res = scheduler.CheckinResult{UID: st.UID, Msg: err.Error()}
			}
			results = append(results, res)
		}
	}
	ok := 0
	for _, r := range results {
		if r.OK {
			ok++
		}
	}
	log.Printf("批量签到完成：total=%d ok=%d failed=%d", len(results), ok, len(results)-ok)
	return results
}

// GrowthAccount 对指定 WorkBuddy 账号执行成长中心自动化（旅行/盲盒/任务领奖）。
// 统一走 Scheduler 路径，保证结果经观察器写入历史记录。
func (a *App) GrowthAccount(uid string) (upstream.GrowthResult, error) {
	rt, _ := a.findRuntimeAuth(uid)
	if rt == nil {
		return upstream.GrowthResult{}, fmt.Errorf("unknown account %s", uid)
	}
	if rt.Kind != provider.WorkBuddy {
		return upstream.GrowthResult{}, fmt.Errorf("成长中心仅支持 WorkBuddy 渠道")
	}
	if rt.Scheduler == nil {
		return upstream.GrowthResult{}, fmt.Errorf("scheduler not available")
	}
	a.initGrowthHistory()
	r, err := rt.Scheduler.GrowthAccount(uid)
	if err != nil {
		log.Printf("growth failed platform=%s uid=%s err=%v", rt.Kind, uid, err)
		return upstream.GrowthResult{}, err
	}
	log.Printf("growth uid=%s travel=%s lottery=%s tasks=%s credits=%d", uid, r.Travel, r.Lottery, r.Tasks, r.TotalCredits)
	return upstream.GrowthResult{
		Travel:      r.Travel,
		Lottery:     r.Lottery,
		Tasks:       r.Tasks,
		TaskDetails: toProviderRewards(r.TaskDetails),
		AuthErr:     r.AuthErr,
	}, nil
}

// toProviderRewards scheduler 任务奖励转 provider 类型。
func toProviderRewards(list []scheduler.TaskReward) []provider.TaskReward {
	out := make([]provider.TaskReward, 0, len(list))
	for _, r := range list {
		out = append(out, provider.TaskReward{Name: r.Name, Credits: r.Credits, CouponName: r.CouponName})
	}
	return out
}

// GrowthRunning 防并发成长中心批量执行标记。
var growthBatchRunning atomic.Bool

// GrowthAll 批量执行所有 WorkBuddy 账号成长中心（同步，供面板"批量运行"按钮）。
func (a *App) GrowthAll() {
	if !growthBatchRunning.CompareAndSwap(false, true) {
		log.Printf("growth batch already running, skip")
		return
	}
	defer growthBatchRunning.Store(false)
	a.initGrowthHistory()
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil || rt.Scheduler == nil || rt.Kind != provider.WorkBuddy {
			continue
		}
		rt.Scheduler.RunGrowthNow()
	}
	log.Printf("growth batch all done")
}

// initGrowthHistory 懒加载 growthHistory。
func (a *App) initGrowthHistory() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.growthHistory == nil {
		a.growthHistory = upstream.NewGrowthHistory(a.growthHistoryFP)
		// 设置到所有 WorkBuddy 渠道的 client
		for _, rt := range a.runtimes {
			if rt != nil && rt.Kind == provider.WorkBuddy {
				if up, ok := rt.Upstream.(*upstream.Client); ok {
					up.SetGrowthHistory(a.growthHistory)
				}
			}
		}
	}
}

// GrowthTodaySummary 今日成长中心统计汇总。
func (a *App) GrowthTodaySummary() map[string]any {
	a.initGrowthHistory()
	if a.growthHistory == nil {
		return map[string]any{"error": "growth history not initialized"}
	}
	return a.growthHistory.GetSummary()
}

// GrowthRecentRecords 获取最近 N 条成长中心记录。
func (a *App) GrowthRecentRecords(limit int) []upstream.GrowthRecord {
	a.initGrowthHistory()
	if a.growthHistory == nil {
		return []upstream.GrowthRecord{}
	}
	return a.growthHistory.Recent(limit)
}

// growthAccountView 成长中心参与账号视图（含最近一次执行结果）。
type growthAccountView struct {
	UID      string                 `json:"uid"`
	Nickname string                 `json:"nickname"`
	Disabled bool                   `json:"disabled"`
	Cooling  bool                   `json:"cooling"`
	Credits  int64                  `json:"credits"`
	Last     *upstream.GrowthRecord `json:"last,omitempty"`
}

// GrowthAccounts 参与成长中心的账号列表：WorkBuddy 渠道全部账号 + 每个账号最近一次执行结果。
func (a *App) GrowthAccounts() []growthAccountView {
	a.initGrowthHistory()
	// uid -> 最近一次记录（历史 Add 为头插，首个命中即最新）。
	lastByUID := map[string]upstream.GrowthRecord{}
	if a.growthHistory != nil {
		for _, r := range a.growthHistory.Recent(100) {
			if _, ok := lastByUID[r.UID]; !ok {
				lastByUID[r.UID] = r
			}
		}
	}
	out := make([]growthAccountView, 0, 8)
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil || rt.Kind != provider.WorkBuddy {
			continue
		}
		for _, s := range rt.Pool.List() {
			v := growthAccountView{
				UID:      s.UID,
				Nickname: s.Nickname,
				Disabled: s.Disabled,
				Cooling:  s.Cooling,
				Credits:  s.Credits,
			}
			if last, ok := lastByUID[s.UID]; ok {
				v.Last = &last
			}
			out = append(out, v)
		}
	}
	return out
}

// RecordGrowth 将调度器产生的成长中心结果写入历史记录（定时/批量路径共用）。
func (a *App) RecordGrowth(channel string, r scheduler.GrowthResult) {
	if r.AuthErr {
		return
	}
	a.initGrowthHistory()
	if a.growthHistory == nil {
		return
	}
	rec := upstream.GrowthRecord{
		UID:          r.UID,
		Nickname:     r.Nickname,
		Channel:      channel,
		Travel:       r.Travel,
		Lottery:      r.Lottery,
		Tasks:        r.Tasks,
		TotalCredits: r.TotalCredits,
		CouponCount:  r.CouponCount,
	}
	for _, rew := range r.TaskDetails {
		rec.TaskDetails = append(rec.TaskDetails, provider.TaskReward{
			Name:       rew.Name,
			Credits:    rew.Credits,
			CouponName: rew.CouponName,
		})
	}
	a.growthHistory.Add(rec)
}

// doubaoResultFP 豆包保活结果文件路径。
func (a *App) doubaoResultFP() string {
	return filepath.Join(filepath.Dir(a.cfg.StateFile), "doubao_renew_result.json")
}

// DoubaoRenew 豆包会话保活续期：读豆包桌面端 Local State 明文 sessionid，
// 两段式探活 + 滑动续期，结果持久化供页面回显（协议见 internal/doubao）。
func (a *App) DoubaoRenew() doubao.Result {
	res := doubao.Renew(doubaoLocalStatePath())
	raw, _ := json.Marshal(res)
	if err := os.WriteFile(a.doubaoResultFP(), raw, 0o644); err != nil {
		log.Printf("doubao renew save failed: %v", err)
	}
	log.Printf("doubao renew done ok=%d expired=%d error=%d accounts=%d",
		res.OK, res.Expired, res.Error, len(res.Accounts))
	return res
}

// DoubaoLastResult 最近一次豆包保活结果（无记录返回空结构）。
func (a *App) DoubaoLastResult() doubao.Result {
	var res doubao.Result
	raw, err := os.ReadFile(a.doubaoResultFP())
	if err != nil {
		return res
	}
	_ = json.Unmarshal(raw, &res)
	return res
}

// doubaoLocalStatePath 豆包桌面客户端 Local State 路径。
func doubaoLocalStatePath() string {
	la := os.Getenv("LOCALAPPDATA")
	if la == "" {
		return ""
	}
	return filepath.Join(la, "Doubao", "User Data", "Local State")
}

// RefreshCredits 刷新单个账号积分。
func (a *App) RefreshCredits(uid string) (int64, error) {
	rt, au := a.findRuntimeAuth(uid)
	if rt == nil || au == nil {
		return 0, fmt.Errorf("unknown account %s", uid)
	}
	log.Printf("credits refresh start platform=%s uid=%s", rt.Kind, uid)
	remain, items, err := rt.Upstream.UserResourceDetail(au)
	if err != nil {
		log.Printf("credits refresh failed platform=%s uid=%s err=%v", rt.Kind, uid, err)
		return 0, err
	}
	expAt, expRemain, expName := provider.EarliestExpireDetail(items)
	rt.Pool.SetCreditsExpire(uid, remain, expAt)
	rt.Pool.SetResourceItems(uid, items)
	if a.usage != nil {
		a.usage.SetExpireDetail(uid, expAt, expRemain, expName)
	}
	log.Printf("credits refresh success platform=%s uid=%s remain=%d", rt.Kind, uid, remain)
	return remain, nil
}

// RefreshAll 刷新全部账号积分，返回汇总（供托盘消息框 / Web UI）。
func (a *App) RefreshAll() RefreshSummary {
	a.refreshMu.Lock()
	if a.refreshing {
		a.refreshMu.Unlock()
		return RefreshSummary{Busy: true}
	}
	a.refreshing = true
	a.refreshMu.Unlock()
	defer func() {
		a.refreshMu.Lock()
		a.refreshing = false
		a.refreshMu.Unlock()
	}()
	sum := RefreshSummary{Platforms: map[string]PlatformSummary{}}
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil || rt.Upstream == nil {
			continue
		}
		ps := PlatformSummary{Accounts: []AccountRefresh{}}
		for _, st := range rt.Pool.List() {
			au := rt.Pool.AuthByUID(st.UID)
			if au == nil || au.AccessToken == "" {
				ps.Failed++
				ps.Accounts = append(ps.Accounts, AccountRefresh{UID: st.UID, OK: false, Msg: "no token"})
				continue
			}
			if remain, items, err := rt.Upstream.UserResourceDetail(au); err == nil {
				expAt, expRemain, expName := provider.EarliestExpireDetail(items)
				rt.Pool.SetCreditsExpire(st.UID, remain, expAt)
				rt.Pool.SetResourceItems(st.UID, items)
				if a.usage != nil {
					a.usage.SetExpireDetail(st.UID, expAt, expRemain, expName)
				}
				ps.OK++
				ps.Accounts = append(ps.Accounts, AccountRefresh{UID: st.UID, OK: true, Remain: remain})
			} else {
				ps.Failed++
				ps.Accounts = append(ps.Accounts, AccountRefresh{UID: st.UID, OK: false, Msg: shortErr(err)})
				log.Printf("credits refresh failed platform=%s uid=%s err=%v", rt.Kind, st.UID, err)
			}
		}
		sum.Total += ps.OK + ps.Failed
		sum.OK += ps.OK
		sum.Failed += ps.Failed
		sum.Platforms[rt.Kind.String()] = ps
	}
	return sum
}

// RefreshSummary 积分刷新汇总（托盘消息框内容）。
type RefreshSummary struct {
	Busy      bool                       `json:"busy"`
	Total     int                        `json:"total"`
	OK        int                        `json:"ok"`
	Failed    int                        `json:"failed"`
	Platforms map[string]PlatformSummary `json:"platforms"`
}

// PlatformSummary 单个渠道汇总。
type PlatformSummary struct {
	OK       int              `json:"ok"`
	Failed   int              `json:"failed"`
	Accounts []AccountRefresh `json:"accounts"`
}

// AccountRefresh 单账号刷新结果。
type AccountRefresh struct {
	UID    string `json:"uid"`
	OK     bool   `json:"ok"`
	Remain int64  `json:"remain"`
	Msg    string `json:"msg,omitempty"`
}

// RemoveAccount 删除账号（auth 文件 + 内存池）。
func (a *App) RemoveAccount(uid string) error {
	rt, au := a.findRuntimeAuth(uid)
	if rt == nil || au == nil {
		return fmt.Errorf("unknown account %s", uid)
	}
	if au.FilePath != "" {
		_ = os.Remove(au.FilePath)
	}
	rt.Pool.Remove(uid)
	log.Printf("已删除账号 %s", shortUID(uid))
	return nil
}

// DisableAccount 停用/启用账号。
func (a *App) DisableAccount(uid string, disabled bool) error {
	rt, _ := a.findRuntimeAuth(uid)
	if rt == nil || rt.Pool == nil {
		return fmt.Errorf("unknown account %s", uid)
	}
	rt.Pool.SetDisabled(uid, disabled)
	if disabled {
		log.Printf("已停用账号 %s", shortUID(uid))
	} else {
		log.Printf("已启用账号 %s", shortUID(uid))
	}
	return nil
}

// ResourceDetail 查询单个账号积分明细。
func (a *App) ResourceDetail(uid string) (int64, []provider.ResourceItem, error) {
	rt, au := a.findRuntimeAuth(uid)
	if rt == nil || au == nil || rt.Upstream == nil {
		return 0, nil, fmt.Errorf("unknown account %s", uid)
	}
	return rt.Upstream.UserResourceDetail(au)
}

// ---------------------------------------------------------------------------
// Atria 官方 API Key 集中管理
// ---------------------------------------------------------------------------

// AddAtriaKey 导入单个 Atria Key：经 auth.SaveAtomic 写嵌套 JSON
// （auth.accessToken=Key，与手工账号 apiKey 字段等价，Parse 自动回退兼容），
// 文件名 auths/atria-<uid>.json，随后 reloadAccounts 入池。同 key 去重。
func (a *App) AddAtriaKey(key, uid, nickname string) (map[string]any, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, fmt.Errorf("key 不能为空")
	}
	if dupUID, exists := a.atriaKeyExists(key); exists {
		return nil, fmt.Errorf("该 Key 已存在（uid=%s）", dupUID)
	}
	if uid = strings.TrimSpace(uid); uid == "" {
		uid = fmt.Sprintf("atria-%d", time.Now().UnixNano()%1000000)
	}
	if nickname = strings.TrimSpace(nickname); nickname == "" {
		nickname = uid
	}
	au := &auth.Auth{
		Kind:        "atria",
		AccessToken: key,
		UID:         uid,
		Nickname:    nickname,
		ExpiresAt:   time.Now().Add(365 * 24 * time.Hour).Unix(),
	}
	au.FilePath = filepath.Join(a.cfg.AuthDir, fmt.Sprintf("atria-%s.json", strings.TrimPrefix(uid, "atria-")))
	if err := au.SaveAtomic(); err != nil {
		return nil, fmt.Errorf("凭证落盘失败: %v", err)
	}
	log.Printf("atria 面板导入 Key uid=%s nickname=%s", uid, nickname)
	a.reloadAccounts()
	return map[string]any{"ok": true, "uid": uid, "nickname": nickname}, nil
}

// AddAtriaKeysBatch 批量导入：每行一个 key，支持 "key 备注名"。
// 返回逐行结果（added/duplicate/error），单行失败不中断整体。
func (a *App) AddAtriaKeysBatch(keysText string) map[string]any {
	type rowRes struct {
		Line   int    `json:"line"`
		Key    string `json:"key"`
		Status string `json:"status"` // added | duplicate | error
		UID    string `json:"uid,omitempty"`
		Detail string `json:"detail,omitempty"`
	}
	res := []rowRes{}
	added := 0
	for i, line := range strings.Split(strings.ReplaceAll(keysText, "\r\n", "\n"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		key, nick := line, ""
		if idx := strings.IndexAny(line, " \t"); idx > 0 {
			key, nick = strings.TrimSpace(line[:idx]), strings.TrimSpace(line[idx+1:])
		}
		if !strings.HasPrefix(key, "atr_") {
			res = append(res, rowRes{Line: i + 1, Key: maskKey(key), Status: "error", Detail: "key 应以 atr_ 开头"})
			continue
		}
		out, err := a.AddAtriaKey(key, "", nick)
		if err != nil {
			status := "error"
			if strings.Contains(err.Error(), "已存在") {
				status = "duplicate"
			}
			res = append(res, rowRes{Line: i + 1, Key: maskKey(key), Status: status, Detail: err.Error()})
			continue
		}
		added++
		res = append(res, rowRes{Line: i + 1, Key: maskKey(key), Status: "added", UID: out["uid"].(string)})
	}
	log.Printf("atria 批量导入完成：added=%d total=%d", added, len(res))
	return map[string]any{"ok": true, "added": added, "results": res}
}

// atriaKeyExists 扫 auths/atria*.json 原始凭证做同 key 去重（不能用
// atriaAccounts 的掩码比较），命中返回其 uid。
func (a *App) atriaKeyExists(key string) (string, bool) {
	files, _ := filepath.Glob(filepath.Join(a.cfg.AuthDir, "atria*.json"))
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		if au, err := auth.Parse(raw); err == nil && au.AccessToken == key {
			uid := au.UID
			if uid == "" {
				uid = strings.TrimSuffix(filepath.Base(f), ".json")
			}
			return uid, true
		}
	}
	return "", false
}

// atriaAccounts Atria 渠道账号清单（uid/昵称/key 掩码/停用状态）。
func (a *App) atriaAccounts() []map[string]any {
	out := []map[string]any{}
	rt := a.runtime(provider.Atria)
	if rt == nil || rt.Pool == nil {
		return out
	}
	for _, s := range rt.Pool.List() {
		au := rt.Pool.AuthByUID(s.UID)
		key := ""
		if au != nil {
			key = au.AccessToken
		}
		out = append(out, map[string]any{
			"uid":      s.UID,
			"nickname": s.Nickname,
			"key":      maskKey(key),
			"disabled": s.Disabled,
			"credits":  s.Credits,
		})
	}
	return out
}

// maskKey API Key 掩码显示（前 8 后 4）。
func maskKey(k string) string {
	if len(k) <= 12 {
		return k
	}
	return k[:8] + "..." + k[len(k)-4:]
}

// ---------------------------------------------------------------------------
// 配置操作
// ---------------------------------------------------------------------------

// SetCheckinTimes 更新自动签到时间（HH:MM）并立即唤醒两个调度器。
func (a *App) SetCheckinTimes(times []string) error {
	minutes, err := config.ParseClockTimes(times)
	if err != nil {
		return err
	}
	clean := normalizeMinutes(minutes)
	if len(clean) == 0 {
		return errors.New("请至少保留一个签到时间")
	}
	formatted := make([]string, 0, len(clean))
	for _, m := range clean {
		formatted = append(formatted, fmt.Sprintf("%02d:%02d", m/60, m%60))
	}
	a.mu.Lock()
	a.cfg.Schedule.CheckinTimes = formatted
	a.cfg.Schedule.CheckinHours = nil
	err = config.Save(a.cfg, a.cfgPath)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	for _, rt := range a.runtimes {
		if rt != nil && rt.Scheduler != nil {
			rt.Scheduler.SetCheckinMinutes(clean)
		}
	}
	log.Printf("自动签到时间已更新：%s", strings.Join(formatted, "、"))
	return nil
}

// SetGrowthTimes 设置成长中心执行时间
func (a *App) SetGrowthTimes(times []string) error {
	minutes, err := config.ParseClockTimes(times)
	if err != nil {
		return err
	}
	clean := normalizeMinutes(minutes)
	if len(clean) == 0 {
		return errors.New("请至少保留一个成长中心执行时间")
	}
	formatted := make([]string, 0, len(clean))
	for _, m := range clean {
		formatted = append(formatted, fmt.Sprintf("%02d:%02d", m/60, m%60))
	}
	a.mu.Lock()
	a.cfg.Schedule.GrowthTimes = formatted
	err = config.Save(a.cfg, a.cfgPath)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	for _, rt := range a.runtimes {
		if rt != nil && rt.Scheduler != nil {
			rt.Scheduler.SetGrowthMinutes(clean)
		}
	}
	log.Printf("成长中心执行时间已更新：%s", strings.Join(formatted, "、"))
	return nil
}

// SetListen 修改 API 监听主机 + 端口：保存配置并热切换监听。
func (a *App) SetListen(host string, port int) error {
	if port <= 0 || port > 65535 {
		return fmt.Errorf("端口无效：%d", port)
	}
	addr := config.Listen{Host: host, Port: port}.Addr()

	a.mu.Lock()
	defer a.mu.Unlock()
	if err := a.serveLocked(addr); err != nil {
		return fmt.Errorf("监听 %s 失败（可能被占用）：%v", addr, err)
	}
	a.cfg.Listen = config.Listen{Host: host, Port: port}
	if err := config.Save(a.cfg, a.cfgPath); err != nil {
		log.Printf("save config after listen change: %v", err)
	}
	log.Printf("API 监听已切换至 %s", addr)
	return nil
}

// SetAPIKey 修改 API 密钥。
func (a *App) SetAPIKey(key string) error {
	key = strings.TrimSpace(key)
	a.mu.Lock()
	a.cfg.APIKey = key
	err := config.Save(a.cfg, a.cfgPath)
	a.mu.Unlock()
	if err != nil {
		return err
	}
	a.handler.SetAPIKey(key)
	log.Printf("API-Key 已更新")
	return nil
}

// SetAutostart 设置/取消开机自启。
func (a *App) SetAutostart(on bool) error {
	if err := platform.SetAutostart(on); err != nil {
		return err
	}
	if on {
		log.Printf("已开启开机自启")
	} else {
		log.Printf("已关闭开机自启")
	}
	return nil
}

// AutostartEnabled 当前是否已开机自启。
func (a *App) AutostartEnabled() bool { return platform.AutostartEnabled() }

// ServerRunning 是否正在监听。
func (a *App) ServerRunning() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.httpSrv != nil
}

// LoginBusy 是否登录中。
func (a *App) LoginBusy() bool {
	a.muLogin.Lock()
	defer a.muLogin.Unlock()
	return a.loginBusy
}

// ---------------------------------------------------------------------------
// 面板数据（Web UI）
// ---------------------------------------------------------------------------

// AccountView 面板展示的账号（脱敏）。
type AccountView struct {
	UID            string                  `json:"uid"`
	Group          string                  `json:"group"` // workbuddy | traework | qoder ...
	Mode           string                  `json:"mode"`  // proxy 反代 | bridge 桥接 | direct 直连 API
	Nickname       string                  `json:"nickname"`
	Credits        int64                   `json:"credits"`
	ExpireAt       int64                   `json:"expire_at"`                // 最早到期套餐时间（Unix 秒，0=长期有效）
	ExpiringRemain int64                   `json:"expiring_remain,omitempty"` // 最早临期套餐剩余量
	ExpiringName   string                  `json:"expiring_name,omitempty"`   // 最早临期套餐名称
	ResourceItems  []provider.ResourceItem `json:"resource_items,omitempty"` // 积分分类明细（含到期时间）
	IsNext         bool                    `json:"is_next"`                  // 是否为当前渠道第一顺位（zcode 按真实轮转顺序）
	BridgeDown     bool                    `json:"bridge_down,omitempty"`    // 桥接账号：bridgeUrl 端口连不上（桥未启动）
	Cooling        bool                    `json:"cooling"`
	Until          string                  `json:"until"`
	Reason         string                  `json:"reason"`
	Disabled       bool                    `json:"disabled"`
	ErrCount       int                     `json:"err_count"`
	LastCheckinOK  bool                    `json:"last_checkin_ok"`
	LastCheckinAt  string                  `json:"last_checkin_at"`
	LastCheckinMsg string                  `json:"last_checkin_msg"`
}

// State Web UI 初始数据。
type State struct {
	Accounts       []AccountView `json:"accounts"`
	CheckinTimes   []string      `json:"checkin_times"`
	KeepaliveHours []int         `json:"keepalive_hours"`
	ListenHost     string        `json:"listen_host"`
	ListenPort     int           `json:"listen_port"`
	APIKey         string        `json:"api_key"`
	LoginBusy      bool          `json:"login_busy"`
	NextCheckin    string        `json:"next_checkin"`
	GrowthTimes    []string      `json:"growth_times"`
	NextGrowth     string        `json:"next_growth"`
	Version        string        `json:"version"`
	Autostart      bool          `json:"autostart"`
	Running        bool          `json:"running"`
}

// GetState 返回面板初始数据。
func (a *App) GetState() State {
	st := State{
		CheckinTimes:   a.checkinTimes(),
		KeepaliveHours: a.keepaliveHours(),
		ListenHost:     a.cfg.Listen.Host,
		ListenPort:     a.cfg.Listen.Port,
		APIKey:         a.cfg.APIKey,
		LoginBusy:      a.LoginBusy(),
		NextCheckin:    fmtTime(a.nextFire()),
		GrowthTimes:    a.growthTimes(),
		NextGrowth:     fmtTime(a.nextGrowthFire()),
		Version:        Version,
		Autostart:      a.AutostartEnabled(),
		Running:        a.ServerRunning(),
	}
	st.Accounts = a.accountViews()
	return st
}

// ---------------------------------------------------------------------------
// 资产总览（GET /api/assets）
// ---------------------------------------------------------------------------

// AssetChannel 单渠道资产：积分余额与 token 折算。
// 折算系数来自 2026-10-06 实测对账（app.log 刷新记录 × requests.jsonl 消耗）：
//   - zcode：干净时段严丝合缝 1 积分 = 1 token（如 10-03 掉 20,547,701 分
//     恰好对应 20,547,701 token），掉分异常时段均为签到/充值事件；
//   - atria：每号赠送 1 亿「积分」即 1 亿 token（pricing-cache 官方备注）；
//   - 其余渠道（workbuddy/qoder/traework/autoclaw 等）两周数亿 token 流量
//     仅掉几百积分 = 限免/按次计费，token 折算无稳定系数，如实标 0。
type AssetChannel struct {
	Channel      string `json:"channel"`
	Accounts     int    `json:"accounts"`
	Healthy      int    `json:"healthy"`
	Credits      int64  `json:"credits"`
	Tokens       int64  `json:"tokens"`        // 可折算 token（不可折算渠道为 0）
	Convertible  bool   `json:"convertible"`   // 积分是否可稳定折算为 token
	Note         string `json:"note,omitempty"`
}

// Assets 资产总览响应。
type Assets struct {
	Channels     []AssetChannel `json:"channels"`
	TotalTokens  int64          `json:"total_tokens"`
	ConvertibleTokens int64     `json:"convertible_tokens"`
	UpdatedAt    string         `json:"updated_at"`
}

// GetAssets 汇总各渠道积分余额并按实测系数折算 token（只读内存池，不发上游请求）。
func (a *App) GetAssets() Assets {
	res := Assets{UpdatedAt: time.Now().Format("2006-01-02 15:04:05")}
	byCh := map[string]*AssetChannel{}
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil {
			continue
		}
		ch := rt.Kind.String()
		ac := byCh[ch]
		if ac == nil {
			ac = &AssetChannel{Channel: ch}
			byCh[ch] = ac
		}
		for _, st := range rt.Pool.List() {
			ac.Accounts++
			if !st.Cooling && !st.Disabled {
				ac.Healthy++
			}
			ac.Credits += st.Credits
		}
	}
	for _, ac := range byCh {
		switch ac.Channel {
		case "zcode":
			ac.Convertible = true
			ac.Tokens = ac.Credits
			ac.Note = "实测 1 积分 ≈ 1 token"
		case "atria":
			ac.Convertible = true
			ac.Tokens = ac.Credits
			ac.Note = "赠送积分即 token（官方口径 1 亿/号）"
		case "workbuddy", "qoder", "traework", "autoclaw", "codebuddy":
			ac.Note = "限免/按次计费，无法折算 token"
		default:
			ac.Note = "积分制渠道，系数未实测"
		}
		res.Channels = append(res.Channels, *ac)
		if ac.Convertible {
			res.ConvertibleTokens += ac.Tokens
		}
		res.TotalTokens += ac.Credits
	}
	sort.Slice(res.Channels, func(i, j int) bool { return res.Channels[i].Credits > res.Channels[j].Credits })
	return res
}

// channelDefaultMode 渠道缺省接入方式：zcode 与本机白嫖三件套（copilot/doubao/gptgemini）
// 走本地桥接，atria 与 OpenAI 兼容 key 渠道（glmflash/groq/tierflow）为 API Key 直连，
// 其余渠道均为网关持有凭证的反代。auth 文件里账号级 account.mode 可覆盖（见 auth.Parse）。
var channelDefaultMode = map[string]string{
	"zcode":     "bridge",
	"copilot":   "bridge",
	"doubao":    "bridge",
	"gptgemini": "bridge",
	"atria":     "direct",
	"glmflash":  "direct",
	"groq":      "direct",
	"tierflow":  "direct",
}

func defaultChannelMode(kind string) string {
	if m, ok := channelDefaultMode[kind]; ok {
		return m
	}
	return "proxy"
}

// bridgePortUp 探测桥接账号的本地桥端口是否可连（bridgeUrl 形如 http://127.0.0.1:18084）。
// 仅回环地址：端口关闭时 connect 立即被 RST 拒绝，开销可忽略；解析失败/无端口按不可连处理。
func bridgePortUp(bridgeURL string) bool {
	u, err := url.Parse(strings.TrimSpace(bridgeURL))
	if err != nil || u.Host == "" || u.Port() == "" {
		return false
	}
	conn, err := net.DialTimeout("tcp", u.Host, 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func (a *App) accountViews() []AccountView {
	// 按渠道 pool 遍历：codebuddy 与 wbintl 可能同 uid（同后端同账户），
	// 必须带上 rt.Kind 才能区分分组，不能仅凭 uid。
	type sv struct {
		kind       string
		s          pool.Status
		isNext     bool
		mode       string
		bridgeDown bool
	}
	var items []sv
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil {
			continue
		}
		// zcode 实际转发走桥接轮转（server.Runtime.pickBridgeRotation），不走 Pool.Pick；
		// 必须用同一个只读预览算「优先调度」，否则标记与实际调度不符（见 PickNextPreview）。
		var nextAuth *auth.Auth
		if a.handler != nil {
			nextAuth = a.handler.PickNextPreview(rt.Kind)
		} else {
			nextAuth = rt.Pool.Pick()
		}
		nextUID := ""
		if nextAuth != nil {
			nextUID = nextAuth.UID
		}
		for _, s := range rt.Pool.List() {
			au := rt.Pool.AuthByUID(s.UID)
			mode := ""
			if au != nil {
				mode = au.Mode
			}
			if mode == "" {
				mode = defaultChannelMode(string(rt.Kind))
			}
			// 只有带 bridgeUrl 的账号（zcode 实例）才探测桥端口：
			// 桥没启动时该号在池里仍是 healthy，必须显式标注，否则会被误当成可用。
			bridgeDown := false
			if au != nil && au.BridgeURL != "" {
				bridgeDown = !bridgePortUp(au.BridgeURL)
			}
			items = append(items, sv{
				kind:       string(rt.Kind),
				s:          s,
				isNext:     s.UID == nextUID && !s.Disabled && !s.Cooling,
				mode:       mode,
				bridgeDown: bridgeDown,
			})
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].s.UID < items[j].s.UID })
	out := make([]AccountView, 0, len(items))
	for _, it := range items {
		s := it.s
		expAt, expRemain, expName := provider.EarliestExpireDetail(s.ResourceItems)
		if s.ExpireAt > 0 && expAt == 0 {
			expAt = s.ExpireAt
		}
		out = append(out, AccountView{
			UID:            s.UID,
			Group:          it.kind,
			Mode:           it.mode,
			Nickname:       s.Nickname,
			Credits:        s.Credits,
			ExpireAt:       expAt,
			ExpiringRemain: expRemain,
			ExpiringName:   expName,
			ResourceItems:  s.ResourceItems,
			IsNext:         it.isNext,
			BridgeDown:     it.bridgeDown,
			Cooling:        s.Cooling,
			Until:          fmtTime(s.Until),
			Reason:         s.Reason,
			Disabled:       s.Disabled,
			ErrCount:       s.ErrCount,
			LastCheckinOK:  s.LastCheckinOK,
			LastCheckinAt:  fmtTime(s.LastCheckinAt),
			LastCheckinMsg: s.LastCheckinMsg,
		})
	}
	return out
}

// accountGroup 返回账号所属分组（workbuddy/traework）。
func (a *App) accountGroup(uid string) string {
	// 遍历各渠道 pool 而非 findRuntimeAuth：codebuddy 与 wbintl 可能同 uid，
	// 需按所属 pool 的 auth 来源（Kind/FilePath）确定分组。
	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil {
			continue
		}
		au := rt.Pool.AuthByUID(uid)
		if au == nil {
			continue
		}
		if au.Kind != "" {
			return au.Kind
		}
		if au.FilePath != "" {
			base := filepath.Base(au.FilePath)
			if strings.HasPrefix(base, "trae-") {
				return "traework"
			}
			if strings.HasPrefix(base, "qoder") {
				return "qoder"
			}
			if strings.HasPrefix(base, "codebuddy") {
				return "codebuddy"
			}
			if strings.HasPrefix(base, "zcode") {
				return "zcode"
			}
			if strings.HasPrefix(base, "autoclaw") {
				return "autoclaw"
			}
		}
		return string(rt.Kind)
	}
	return "workbuddy"
}

// OpenLogFile 用系统默认编辑器打开日志文件。
func (a *App) OpenLogFile() error {
	fp := filepath.Join(filepath.Dir(a.cfg.StateFile), "app.log")
	if _, err := os.Stat(fp); err != nil {
		_ = os.WriteFile(fp, []byte("(empty)\n"), 0o644)
	}
	abs, err := filepath.Abs(fp)
	if err != nil {
		return err
	}
	return platform.OpenWithTextEditor(abs)
}

// NotifyCheckin 记录自动签到结果（调度器观察器）。
func (a *App) NotifyCheckin(platformName string, r scheduler.CheckinResult) {
	log.Printf("checkin platform=%s uid=%s ok=%t msg=%s remain=%d has_remain=%t",
		platformName, r.UID, r.OK, r.Msg, r.Remain, r.HasRemain)
}

// NotifyRefresh 记录 token 刷新结果（调度器观察器）。
func (a *App) NotifyRefresh(platformName, uid string, ok bool, msg string) {
	log.Printf("refresh platform=%s uid=%s ok=%t msg=%s", platformName, uid, ok, msg)
}

// ---------------------------------------------------------------------------
// 管理 API（HTTP）
// ---------------------------------------------------------------------------

// writeJSON 写 JSON 响应。
func writeJSON(w http.ResponseWriter, status int, v any) {
	raw, _ := json.Marshal(v)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(raw)
}

// apiError 统一错误响应。
func apiError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg})
}

// isAllowedOrigin 校验跨域来源是否可信（防恶意网页 CSRF）。
// 允许来源：同源请求、本地回环、局域网私有网段 IP、以及绑定的域名。
func isAllowedOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	if err != nil {
		return false
	}
	originHost := u.Hostname()
	reqHost, _, err := net.SplitHostPort(host)
	if err != nil {
		reqHost = host
	}
	// 1. 完全匹配当前 Host
	if strings.EqualFold(originHost, reqHost) {
		return true
	}
	// 2. 本地回环
	if originHost == "127.0.0.1" || originHost == "localhost" || originHost == "::1" {
		return true
	}
	// 3. 局域网私有网段 (10.x, 172.16-31.x, 192.168.x)
	if ip := net.ParseIP(originHost); ip != nil && (ip.IsPrivate() || ip.IsLoopback()) {
		return true
	}
	// 4. 用户自有域名（如 chatui.my123.bond 或 my123.bond）
	if strings.HasSuffix(originHost, ".my123.bond") || originHost == "my123.bond" {
		return true
	}
	return false
}

// csrfGuard 针对浏览器发起的跨域写操作进行来源核验，拦截外部恶意网页盲打本地管理 API。
func (a *App) csrfGuard(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 读操作（GET/HEAD/OPTIONS）不产生服务端写副作用，直接放行
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		origin := r.Header.Get("Origin")
		if origin == "" {
			// 无 Origin 头：非浏览器跨域请求（如本地后端进程、curl、移动端直连、ChatUI 后端调用），直接放行
			next.ServeHTTP(w, r)
			return
		}
		// 来自浏览器的写请求：校验 Origin 是否合法
		if isAllowedOrigin(origin, r.Host) {
			next.ServeHTTP(w, r)
			return
		}
		log.Printf("[security] 拦截可疑跨域 CSRF 请求: origin=%s target=%s method=%s host=%s", origin, r.URL.Path, r.Method, r.Host)
		apiError(w, http.StatusForbidden, "Cross-origin request rejected (CSRF protection)")
	})
}

// HandleAPI 注册管理 API 路由（挂到 server handler 的 /api/* 上）。
// 内置 CSRF 来源核验，杜绝第三方恶意网页跨域盲打。
func (a *App) HandleAPI(mux *http.ServeMux) {
	apiMux := http.NewServeMux()
	a.registerAPIRoutes(apiMux)
	mux.Handle("/api/", a.csrfGuard(apiMux))
}

// HandleReadOnlyAPI 注册只读 Token 大屏所需的最小 API 路由（供 ListenDashboard 只读监听）。
// 只挂匿名读端点：token_usage / audit 汇总与明细 / 流水 / usage / assets。
// 刻意不挂 /api/state（含 API-Key）、登录、账号操作、路由重定向等管理端点，
// 确保局域网内只能“看大屏”，拿不到任何敏感信息或控制能力。
// 注意：/api/audit/logs 返回的 key_snippet 本身是掩码（internal/audit），无泄漏风险。
func (a *App) HandleReadOnlyAPI(mux *http.ServeMux) {
	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /api/audit/logs", func(w http.ResponseWriter, r *http.Request) {
		if a.auditRec == nil {
			writeJSON(w, http.StatusOK, map[string]any{"logs": []any{}})
			return
		}
		limit := 100
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
				limit = n
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": a.auditRec.Recent(limit)})
	})
	apiMux.HandleFunc("GET /api/assets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.GetAssets())
	})
	apiMux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		if a.usage != nil {
			writeJSON(w, http.StatusOK, a.usage.JSON())
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"accounts": []any{}})
		}
	})
	apiMux.HandleFunc("GET /api/audit/summary", func(w http.ResponseWriter, r *http.Request) {
		if a.auditRec == nil {
			writeJSON(w, http.StatusOK, map[string]any{"requests_today": 0, "tokens_today": 0, "fails_today": 0})
			return
		}
		reqs, tokens, fails := a.auditRec.SummaryToday()
		writeJSON(w, http.StatusOK, map[string]any{
			"requests_today": reqs,
			"tokens_today":   tokens,
			"fails_today":    fails,
		})
	})
	apiMux.HandleFunc("GET /api/audit/analytics", func(w http.ResponseWriter, r *http.Request) {
		if a.auditRec == nil {
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		writeJSON(w, http.StatusOK, a.auditRec.Analytics())
	})
	mux.Handle("/api/", a.csrfGuard(apiMux))
}

func (a *App) registerAPIRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/state", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.GetState())
	})
	mux.HandleFunc("GET /api/assets", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.GetAssets())
	})
	mux.HandleFunc("GET /api/usage", func(w http.ResponseWriter, r *http.Request) {
		if a.usage != nil {
			writeJSON(w, http.StatusOK, a.usage.JSON())
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"accounts": []any{}})
		}
	})
	mux.HandleFunc("POST /api/login/start", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Channel  string `json:"channel"`
			Provider string `json:"provider"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		a.muLogin.Lock()
		a.pendingZcProv = strings.TrimSpace(req.Provider)
		a.muLogin.Unlock()
		url, err := a.StartLoginFor(req.Channel)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"auth_url": url})
	})
	mux.HandleFunc("POST /api/login/cancel", func(w http.ResponseWriter, r *http.Request) {
		if err := a.CancelLogin(); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/account/checkin", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID string `json:"uid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		res, err := a.CheckinAccount(req.UID)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("POST /api/account/checkin_all", func(w http.ResponseWriter, r *http.Request) {
		results := a.CheckinAll()
		writeJSON(w, http.StatusOK, map[string]any{"results": results})
	})
	mux.HandleFunc("GET /api/arsenal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.ArsenalOverview())
	})
	mux.HandleFunc("POST /api/autoclaw/send_code", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Phone string `json:"phone"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		ac, ok := a.autoclawClient()
		if !ok {
			apiError(w, http.StatusBadRequest, "autoclaw 渠道未启用")
			return
		}
		if _, err := ac.SendCode(req.Phone); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/autoclaw/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Phone string `json:"phone"`
			Code  string `json:"code"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		ac, ok := a.autoclawClient()
		if !ok {
			apiError(w, http.StatusBadRequest, "autoclaw 渠道未启用")
			return
		}
		deviceID := ""
		// 沿用本次会话设备：无则现发（首次会重复发条短信，可接受）
		deviceID = newDeviceIDForAutoclaw()
		lr, err := ac.Login(deviceID, req.Phone, req.Code)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		au := &auth.Auth{
			Kind:         "autoclaw",
			AccessToken:  strings.TrimSpace(lr.AccessToken),
			RefreshToken: strings.TrimSpace(lr.RefreshToken),
			DeviceID:     lr.DeviceID,
			UID:          lr.UserID,
			Nickname:     lr.UserName,
		}
		au.FilePath = filepath.Join(a.cfg.AuthDir, fmt.Sprintf("autoclaw-%s.json", lr.UserID))
		if err := au.SaveAtomic(); err != nil {
			apiError(w, http.StatusInternalServerError, "凭证落盘失败: "+err.Error())
			return
		}
		log.Printf("autoclaw 面板登录成功 uid=%s nickname=%s", lr.UserID, lr.UserName)
		a.reloadAccounts()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "uid": lr.UserID, "nickname": lr.UserName})
	})
	mux.HandleFunc("POST /api/wbintl/add", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Domain       string `json:"domain"` // codebuddy.ai（wbintl 渠道已下线，仅保留 codebuddy）
			Token        string `json:"token"`
			RefreshToken string `json:"refresh_token"`
			Nickname     string `json:"nickname"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		token := strings.TrimSpace(req.Token)
		if token == "" {
			apiError(w, http.StatusBadRequest, "token 不能为空")
			return
		}
		kind := ""
		switch req.Domain {
		case "codebuddy.ai":
			kind = "codebuddy"
		default:
			apiError(w, http.StatusBadRequest, "domain 仅支持 codebuddy.ai")
			return
		}
		uid, uname := jwtIDName(token)
		if uid == "" {
			uid = fmt.Sprintf("%d", time.Now().UnixNano())
		}
		if req.Nickname != "" {
			uname = req.Nickname
		}
		au := &auth.Auth{
			Kind:         kind,
			AccessToken:  token,
			RefreshToken: strings.TrimSpace(req.RefreshToken),
			Domain:       "www." + req.Domain,
			UID:          uid,
			Nickname:     uname,
		}
		au.FilePath = filepath.Join(a.cfg.AuthDir, fmt.Sprintf("%s-%s.json", kind, uid))
		if err := au.SaveAtomic(); err != nil {
			apiError(w, http.StatusInternalServerError, "凭证落盘失败: "+err.Error())
			return
		}
		log.Printf("%s 面板粘贴登录成功 uid=%s nickname=%s", kind, uid, uname)
		a.reloadAccounts()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "uid": uid, "nickname": uname})
	})
	// Atria 官方 API Key 导入（单个）：key 存扁平 JSON 的 apiKey 字段（与手工账号一致）
	mux.HandleFunc("POST /api/atria/add", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key      string `json:"key"`
			Nickname string `json:"nickname"`
			UID      string `json:"uid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		key := strings.TrimSpace(req.Key)
		if key == "" {
			apiError(w, http.StatusBadRequest, "key 不能为空")
			return
		}
		if !strings.HasPrefix(key, "atr_") {
			apiError(w, http.StatusBadRequest, "key 格式不对：Atria API Key 应以 atr_ 开头")
			return
		}
		res, err := a.AddAtriaKey(key, req.UID, req.Nickname)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	// Atria 批量导入：一行一个 key（可 "key 备注名"），重复 key 自动跳过
	mux.HandleFunc("POST /api/atria/add_batch", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Keys string `json:"keys"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if strings.TrimSpace(req.Keys) == "" {
			apiError(w, http.StatusBadRequest, "keys 不能为空")
			return
		}
		writeJSON(w, http.StatusOK, a.AddAtriaKeysBatch(req.Keys))
	})
	// Atria 渠道账号清单（uid/昵称/停用状态，供管理界面回显）
	mux.HandleFunc("GET /api/atria/list", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"accounts": a.atriaAccounts()})
	})
	// 本地桥：探测 / 启动 / 停止 / 一键全启
	mux.HandleFunc("GET /api/bridges", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"bridges": a.bridges.ProbeAll()})
	})
	mux.HandleFunc("POST /api/bridges/start", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		st, err := a.bridges.Start(r.Context(), req.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": st, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": st})
	})
	mux.HandleFunc("POST /api/bridges/start_all", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"statuses": a.bridges.StartAll(r.Context())})
	})
	mux.HandleFunc("POST /api/bridges/stop", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			ID string `json:"id"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		st, err := a.bridges.Stop(r.Context(), req.ID)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"status": st, "error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": st})
	})
	// ZCode 周末包领取：最近结果 + 手动触发（CLI 路径，勿改走 HTTP admin 端点）
	mux.HandleFunc("GET /api/zcode/claim_status", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"report": zcode.ClaimLastReport()})
	})
	mux.HandleFunc("POST /api/zcode/claim_now", func(w http.ResponseWriter, r *http.Request) {
		go zcode.ClaimNow("manual")
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "msg": "领取已启动，稍后刷新查看结果"})
	})
	// ZCode captcha 载体页：领取前用用户真实浏览器 mint param（daemon 经环境变量注入 CLI）。
	// GET 直接渲染（打开页面的是浏览器，无管理凭证）；POST 回传受 csrfGuard 同源校验保护。
	mux.HandleFunc("GET /api/zcode/carrier", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		contribute := "http://" + r.Host + "/api/zcode/carrier/contribute"
		html, ok := zcode.CarrierPage(token, contribute)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(html))
	})
	mux.HandleFunc("POST /api/zcode/carrier/contribute", func(w http.ResponseWriter, r *http.Request) {
		token := r.URL.Query().Get("token")
		var req struct {
			Param string `json:"param"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := zcode.DeliverCaptchaParam(token, req.Param); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/account/refresh", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID string `json:"uid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		remain, err := a.RefreshCredits(req.UID)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"remain": remain})
	})
	mux.HandleFunc("POST /api/account/refresh_all", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.RefreshAll())
	})
	mux.HandleFunc("POST /api/account/growth", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID string `json:"uid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		res, err := a.GrowthAccount(req.UID)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, res)
	})
	mux.HandleFunc("GET /api/growth/summary", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.GrowthTodaySummary())
	})
	mux.HandleFunc("POST /api/growth/run_now", func(w http.ResponseWriter, r *http.Request) {
		go a.GrowthAll()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "msg": "成长中心批量执行已启动"})
	})
	mux.HandleFunc("GET /api/growth/accounts", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"accounts": a.GrowthAccounts()})
	})
	mux.HandleFunc("GET /api/growth/records", func(w http.ResponseWriter, r *http.Request) {
		limit := 30
		if l := r.URL.Query().Get("limit"); l != "" {
			fmt.Sscanf(l, "%d", &limit)
			if limit < 1 || limit > 200 {
				limit = 30
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"records": a.GrowthRecentRecords(limit)})
	})
	mux.HandleFunc("POST /api/account/remove", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID string `json:"uid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.RemoveAccount(req.UID); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/account/disable", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID      string `json:"uid"`
			Disabled bool   `json:"disabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.DisableAccount(req.UID, req.Disabled); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/account/resource_detail", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			UID string `json:"uid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		remain, items, err := a.ResourceDetail(req.UID)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"remain": remain, "items": items})
	})
	mux.HandleFunc("POST /api/doubao/renew", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.DoubaoRenew())
	})
	mux.HandleFunc("GET /api/doubao/renew_result", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.DoubaoLastResult())
	})
	mux.HandleFunc("POST /api/config/checkin_times", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Times []string `json:"times"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.SetCheckinTimes(req.Times); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/config/growth_times", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"times": a.growthTimes()})
	})
	mux.HandleFunc("POST /api/config/growth_times", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Times []string `json:"times"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.SetGrowthTimes(req.Times); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/config/listen", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Host string `json:"host"`
			Port int    `json:"port"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.SetListen(req.Host, req.Port); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/config/api_key", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Key string `json:"key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.SetAPIKey(req.Key); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/config/autostart", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			On bool `json:"on"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.SetAutostart(req.On); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("GET /api/fees", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.FeesInfo())
	})
	mux.HandleFunc("POST /api/fees/refresh", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, a.FeesInfo())
		go a.safeGo(func() { a.RefreshPricing() })
	})
	mux.HandleFunc("GET /api/logs", func(w http.ResponseWriter, r *http.Request) {
		fp := filepath.Join(filepath.Dir(a.cfg.StateFile), "app.log")
		raw, _ := os.ReadFile(fp)
		lines := strings.Split(string(raw), "\n")
		if len(lines) > 300 {
			lines = lines[len(lines)-300:]
		}
		writeJSON(w, http.StatusOK, map[string]any{"lines": lines})
	})
	// POST /api/quit 已移除（2026-09-23）：
// 前端从不调用它，是旧版 Web UI 遗留的孤儿路由；任何本地进程 POST 一下就会
// 无声杀掉整个网关（日志无痕，表现为"自己退掉了"）。退出统一走托盘菜单「退出」。
// （如确需远程退出，请显式恢复并加鉴权，勿静默。)

	// 客户端 Key 管理
	mux.HandleFunc("GET /api/clients", func(w http.ResponseWriter, r *http.Request) {
		if a.clientKeys != nil {
			writeJSON(w, http.StatusOK, map[string]any{"clients": a.clientKeys.List()})
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"clients": []any{}})
		}
	})
	mux.HandleFunc("POST /api/clients/create", func(w http.ResponseWriter, r *http.Request) {
		if a.clientKeys == nil {
			apiError(w, http.StatusInternalServerError, "client keys store not initialized")
			return
		}
		var req struct {
			Name string `json:"name"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		k, err := a.clientKeys.Create(req.Name)
		if err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, k)
	})
	mux.HandleFunc("POST /api/clients/delete", func(w http.ResponseWriter, r *http.Request) {
		if a.clientKeys == nil {
			apiError(w, http.StatusInternalServerError, "client keys store not initialized")
			return
		}
		var req struct {
			Key string `json:"key"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.clientKeys.Delete(req.Key); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	mux.HandleFunc("POST /api/clients/toggle", func(w http.ResponseWriter, r *http.Request) {
		if a.clientKeys == nil {
			apiError(w, http.StatusInternalServerError, "client keys store not initialized")
			return
		}
		var req struct {
			Key     string `json:"key"`
			Enabled bool   `json:"enabled"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if err := a.clientKeys.Toggle(req.Key, req.Enabled); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	// 实时调用审计日志流水
	mux.HandleFunc("GET /api/audit/logs", func(w http.ResponseWriter, r *http.Request) {
		if a.auditRec == nil {
			writeJSON(w, http.StatusOK, map[string]any{"logs": []any{}})
			return
		}
		limit := 100
		if lStr := r.URL.Query().Get("limit"); lStr != "" {
			if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
				limit = n
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"logs": a.auditRec.Recent(limit)})
	})
	mux.HandleFunc("GET /api/audit/summary", func(w http.ResponseWriter, r *http.Request) {
		if a.auditRec == nil {
			writeJSON(w, http.StatusOK, map[string]any{"requests_today": 0, "tokens_today": 0, "fails_today": 0})
			return
		}
		reqs, tokens, fails := a.auditRec.SummaryToday()
		writeJSON(w, http.StatusOK, map[string]any{
			"requests_today": reqs,
			"tokens_today":   tokens,
			"fails_today":    fails,
		})
	})
	mux.HandleFunc("GET /api/audit/analytics", func(w http.ResponseWriter, r *http.Request) {
		if a.auditRec == nil {
			writeJSON(w, http.StatusOK, map[string]any{})
			return
		}
		writeJSON(w, http.StatusOK, a.auditRec.Analytics())
	})

	// 模型路由重定向规则
	mux.HandleFunc("GET /api/router/redirects", func(w http.ResponseWriter, r *http.Request) {
		if a.handler != nil {
			writeJSON(w, http.StatusOK, map[string]any{"redirects": a.handler.GetModelRedirects()})
		} else {
			writeJSON(w, http.StatusOK, map[string]any{"redirects": a.cfg.ModelRedirects})
		}
	})
	mux.HandleFunc("POST /api/router/redirects", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Redirects map[string][]string `json:"redirects"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			apiError(w, http.StatusBadRequest, err.Error())
			return
		}
		a.mu.Lock()
		a.cfg.ModelRedirects = req.Redirects
		_ = config.Save(a.cfg, a.cfgPath)
		a.mu.Unlock()
		if a.handler != nil {
			a.handler.SetModelRedirects(req.Redirects)
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
}

// ---------------------------------------------------------------------------
// 渠道费率信息（本地缓存 + 按需刷新）
// ---------------------------------------------------------------------------

// FeesInfo 返回渠道费率说明（本地缓存优先，有账号时尝试拉取上游）。
func (a *App) FeesInfo() map[string]any {
	a.pricingMu.Lock()
	cached := a.pricingCache
	errMsg := a.pricingErr
	a.pricingMu.Unlock()

	if len(cached) == 0 {
		// 首次加载：异步拉取，先返回静态兜底
		go a.safeGo(func() { a.RefreshPricing() })
		return staticFeesInfo()
	}

	// 缓存超过 1 小时，后台静默刷新
	if time.Since(a.pricingFetched) > time.Hour {
		go a.safeGo(func() { a.RefreshPricing() })
	}

	// 按渠道分组。过滤用户自定义反代模型：其模型名形如 "渠道/模型"（含斜杠），
	// 展示时会拼成 "渠道/渠道/模型" 多段怪名，且普遍无倍率被误标限免。
	groups := map[string][]map[string]any{}
	for _, p := range cached {
		if p.Model == "" || strings.Contains(p.Model, "/") {
			continue
		}
		groups[p.Channel] = append(groups[p.Channel], map[string]any{
			"model": p.Model,
			"rate":  p.Rate,
			"note":  p.Note,
		})
	}

	channels := make([]map[string]any, 0)
	allChList := []string{
		"workbuddy", "traework", "qoder", "codebuddy", "wbintl",
		"zcode", "autoclaw", "atria",
		"copilot", "doubao", "gptgemini", "glmflash", "groq", "tierflow",
	}
	for _, ch := range allChList {
		models, ok := groups[ch]
		if !ok || len(models) == 0 {
			models = staticChannelModels(ch)
		}
		if len(models) > 0 {
			channels = append(channels, map[string]any{
				"channel": ch,
				"models":  models,
			})
		}
	}

	result := map[string]any{
		"note":       "费率随上游平台政策动态变化，请以官方为准；点击「刷新费率」重新拉取。",
		"channels":   channels,
		"disclaimer": "本工具仅聚合转发，不参与定价；渠道费率以各上游官方页面为准。",
		"cached_at":  a.pricingFetched.Format("01-02 15:04"),
	}
	if errMsg != "" {
		result["error"] = errMsg
	}
	return result
}

// RefreshPricing 从所有已接入渠道拉取最新定价并更新缓存。
func (a *App) RefreshPricing() {
	allPricing := make([]provider.ModelPricing, 0)
	var errs []string

	for _, rt := range a.runtimes {
		if rt == nil || rt.Pool == nil || rt.Upstream == nil || len(rt.Pool.List()) == 0 {
			continue
		}
		acct := rt.Pool.Pick()
		if acct == nil {
			continue
		}
		// 先确保 token 有效
		if acct.NeedsRefresh(10 * time.Minute) {
			if err := rt.Upstream.RefreshToken(acct); err != nil {
				errs = append(errs, fmt.Sprintf("%s: token refresh failed", rt.Kind))
				continue
			}
		}
		pricing, err := rt.Upstream.FetchModelPricing(acct)
		if err != nil {
			errs = append(errs, fmt.Sprintf("%s: %s", rt.Kind, shortErr(err)))
			log.Printf("pricing fetch failed platform=%s err=%v", rt.Kind, err)
			continue
		}
		allPricing = append(allPricing, pricing...)
		log.Printf("pricing fetched platform=%s count=%d", rt.Kind, len(pricing))
	}

	// 排序：按渠道 + 倍率
	sort.Slice(allPricing, func(i, j int) bool {
		if allPricing[i].Channel != allPricing[j].Channel {
			return allPricing[i].Channel < allPricing[j].Channel
		}
		return allPricing[i].Rate < allPricing[j].Rate
	})

	a.pricingMu.Lock()
	a.pricingCache = allPricing
	a.pricingFetched = time.Now()
	if len(errs) > 0 {
		a.pricingErr = strings.Join(errs, "; ")
	} else {
		a.pricingErr = ""
	}
	a.pricingMu.Unlock()
	a.savePricingCache()
}

// loadPricingCache 从文件加载定价缓存。
func (a *App) loadPricingCache() {
	raw, err := os.ReadFile(a.pricingFP)
	if err != nil {
		return
	}
	var cache struct {
		Models  []provider.ModelPricing `json:"models"`
		Fetched string                  `json:"fetched"`
	}
	if err := json.Unmarshal(raw, &cache); err != nil {
		return
	}
	a.pricingMu.Lock()
	a.pricingCache = cache.Models
	if cache.Fetched != "" {
		if t, err := time.Parse(time.RFC3339, cache.Fetched); err == nil {
			a.pricingFetched = t
		}
	}
	a.pricingMu.Unlock()
}

// savePricingCache 持久化定价缓存到文件。
func (a *App) savePricingCache() {
	a.pricingMu.Lock()
	cache := struct {
		Models  []provider.ModelPricing `json:"models"`
		Fetched string                  `json:"fetched"`
	}{
		Models:  a.pricingCache,
		Fetched: a.pricingFetched.Format(time.RFC3339),
	}
	a.pricingMu.Unlock()
	raw, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(a.pricingFP, raw, 0o644)
}

// staticChannelModels 单渠道静态兜底模型定义（无动态拉取或拉取失败时兜底）。
func staticChannelModels(ch string) []map[string]any {
	switch ch {
	case "workbuddy":
		return []map[string]any{
			{"model": "auto", "rate": 0, "note": "自动路由最优模型 · 按对话扣积分"},
			{"model": "deepseek-v4.1-flash", "rate": 0, "note": "DeepSeek V4.1 高速版"},
			{"model": "deepseek-v4-pro", "rate": 0, "note": "DeepSeek V4 深度思考"},
			{"model": "glm-5.3-flash", "rate": 0, "note": "智谱 GLM-5.3 高速版"},
			{"model": "kimi-k3", "rate": 0, "note": "Kimi K3 超长上下文"},
			{"model": "hy3-preview", "rate": 0, "note": "腾讯混元 Hy3 Preview"},
		}
	case "traework":
		return []map[string]any{
			{"model": "DeepSeek-V4-Pro", "rate": 0, "note": "官方渠道按 token 计费"},
			{"model": "DeepSeek-V4-Flash", "rate": 0, "note": "官方渠道按 token 计费"},
			{"model": "glm-5.3", "rate": 0, "note": "智谱旗舰编码模型"},
			{"model": "Doubao-Seed-2.1-Pro", "rate": 0, "note": "火山豆包 2.1 Pro"},
			{"model": "qwen3.8-max", "rate": 0, "note": "阿里通义千问 3.8 Max"},
			{"model": "kimi-k3", "rate": 0, "note": "月之暗面 K3"},
		}
	case "qoder":
		return []map[string]any{
			{"model": "deepseek-v4-pro", "rate": 0, "note": "按积分扣除 · 智能编程"},
			{"model": "glm-5.3-flash", "rate": 0, "note": "按积分扣除 · 极速响应"},
			{"model": "qwen3.8-max", "rate": 0, "note": "千问 3.8 Max"},
			{"model": "kimi-k3", "rate": 0, "note": "Kimi K3 旗舰"},
			{"model": "minimax-m3", "rate": 0, "note": "MiniMax M3 编程"},
		}
	case "codebuddy":
		return []map[string]any{
			{"model": "default-model", "rate": 0, "note": "国际版限免 0 积分"},
			{"model": "deepseek-v4.1-flash", "rate": 0, "note": "隐藏模型限免 0 积分"},
			{"model": "hy4-preview", "rate": 0, "note": "混元 4 Preview 限免"},
			{"model": "fast-model", "rate": 0.34, "note": "高速轻量模型"},
			{"model": "balanced-model", "rate": 0.59, "note": "平衡主力模型"},
			{"model": "glm-5.3", "rate": 0.79, "note": "智谱 GLM-5.3"},
			{"model": "gpt-5.6-terra", "rate": 1.39, "note": "GPT-5.6 Terra"},
		}
	case "wbintl":
		return []map[string]any{
			{"model": "auto", "rate": 0, "note": "WorkBuddy 国际版 · 自动最优"},
			{"model": "deepseek-chat", "rate": 0, "note": "国际版直连"},
			{"model": "claude-3-5-sonnet", "rate": 0, "note": "国际版限免/积分"},
		}
	case "zcode":
		return []map[string]any{
			{"model": "glm-5.3-flash", "rate": 0, "note": "周末计划免费领 3 亿 Token · 极速编程 ⭐"},
			{"model": "glm-5.3", "rate": 0, "note": "智谱全能旗舰 · 300M Weekend Build 支持"},
			{"model": "GLM-5.2", "rate": 0, "note": "1M 超大上下文 · 本地桥直通"},
			{"model": "GLM-5-Turbo", "rate": 0, "note": "136K 上下文高速响应"},
		}
	case "autoclaw":
		return []map[string]any{
			{"model": "glm-5.3-flash", "rate": 0, "note": "会员/按量通道 · 实测扣积分（短请求≈1分）⭐"},
			{"model": "zaicoding_glm-5.3", "rate": 0, "note": "GLM-5.3 编码通道 · 按量计费"},
			{"model": "zai_auto", "rate": 0, "note": "服务端自动选型路由 · 支持 Vision"},
			{"model": "zai_auto-fast", "rate": 0, "note": "快速自动路由"},
		}
	case "atria":
		return []map[string]any{
			{"model": "Atria-Dawn-Preview", "rate": 0, "note": "官方赠送 1 亿 Token · 256K 上下文 · 官方直连 ⭐"},
		}
	case "copilot":
		return []map[string]any{
			{"model": "gpt-4o", "rate": 0, "note": "本地白嫖服务 127.0.0.1:4141 · OpenAI 兼容"},
			{"model": "claude-3-5-sonnet", "rate": 0, "note": "Copilot 转代 · 旗舰编程"},
			{"model": "o1-preview", "rate": 0, "note": "慢思考推理模型"},
		}
	case "doubao":
		return []map[string]any{
			{"model": "doubao-seed-2-1-pro", "rate": 0, "note": "本地桥 127.0.0.1:7868 · 豆包旗舰 2.1 Pro"},
			{"model": "doubao-seed-2-1-turbo", "rate": 0, "note": "豆包 2.1 Turbo 高速版"},
			{"model": "doubao-seed-code", "rate": 0, "note": "豆包专有代码补全模型"},
		}
	case "gptgemini":
		return []map[string]any{
			{"model": "gemini-3-flash", "rate": 0, "note": "本地白嫖服务 127.0.0.1:8317 · Gemini 3 Flash"},
			{"model": "gemini-3.1-pro", "rate": 0, "note": "Gemini 3.1 Pro 超长上下文"},
			{"model": "gpt-5.6-terra", "rate": 0, "note": "GPT-5.6 转发"},
			{"model": "grok-4.7", "rate": 0, "note": "xAI Grok 最新版"},
		}
	case "glmflash":
		return []map[string]any{
			{"model": "glm-5.3-flash", "rate": 0, "note": "智谱官方开放平台 · 永久免费层 ⭐"},
			{"model": "glm-5.3", "rate": 0, "note": "智谱开放平台 API Key 直连"},
		}
	case "groq":
		return []map[string]any{
			{"model": "llama-3.3-70b-versatile", "rate": 0, "note": "Groq LPU 极速推理云 · 免费层"},
			{"model": "qwen-2.5-coder-32b", "rate": 0, "note": "通义千问代码版 · 免费极速"},
		}
	case "tierflow":
		return []map[string]any{
			{"model": "claude-3-5-sonnet", "rate": 0, "note": "tierflow.cn 免费聚合 · Bearer Key"},
			{"model": "gpt-4o", "rate": 0, "note": "免费聚合通道"},
			{"model": "deepseek-chat", "rate": 0, "note": "DeepSeek 直连"},
		}
	default:
		return nil
	}
}

// staticFeesInfo 静态兜底费率说明（无账号/首次加载时）。
func staticFeesInfo() map[string]any {
	channels := make([]map[string]any, 0)
	for _, ch := range []string{
		"workbuddy", "traework", "qoder", "codebuddy", "wbintl",
		"zcode", "autoclaw", "atria",
		"copilot", "doubao", "gptgemini", "glmflash", "groq", "tierflow",
	} {
		if models := staticChannelModels(ch); len(models) > 0 {
			channels = append(channels, map[string]any{
				"channel": ch,
				"models":  models,
			})
		}
	}
	return map[string]any{
		"note":       "费率随上游平台政策动态变化，请以官方为准；添加账号后点击「刷新费率」拉取实时数据。",
		"channels":   channels,
		"disclaimer": "本工具仅聚合转发，不参与定价；渠道费率以各上游官方页面为准。",
	}
}

// ---------------------------------------------------------------------------
// 日志
// ---------------------------------------------------------------------------

// logWriter 写日志文件。
type logWriter struct{ app *App }

func (w *logWriter) Write(p []byte) (int, error) {
	if w.app.logFile != nil {
		_, _ = w.app.logFile.Write(p)
	}
	return len(p), nil
}

// safeGo 带 panic 兜底的 goroutine 启动器。
func (a *App) safeGo(f func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC: %v", r)
			}
		}()
		f()
	}()
}

// ---------------------------------------------------------------------------
// 小工具
// ---------------------------------------------------------------------------

func normalizeMinutes(minutes []int) []int {
	seen := map[int]bool{}
	out := []int{}
	for _, m := range minutes {
		if m >= 0 && m < 24*60 && !seen[m] {
			seen[m] = true
			out = append(out, m)
		}
	}
	sort.Ints(out)
	return out
}

func fmtTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("01-02 15:04")
}

func shortUID(uid string) string {
	if len(uid) <= 10 {
		return uid
	}
	return uid[:10] + "…"
}

func shortErr(err error) string {
	s := strings.TrimSpace(err.Error())
	if len(s) > 120 {
		return s[:120]
	}
	return s
}
