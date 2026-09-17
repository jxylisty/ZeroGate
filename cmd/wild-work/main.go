// Command wild-work 系统托盘 daemon 入口。
// 单进程 = OpenAI 兼容 HTTP 服务 + 自动签到调度器 + 系统托盘 + 静态 Web UI。
// 双击 exe 启动常驻托盘；托盘菜单：打开主界面 / 刷新积分 / 查看日志 / 退出；
// 打开主界面或双击托盘 → 系统浏览器打开 Web UI。
package main

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"wild-work/internal/app"
	"wild-work/internal/auth"
	"wild-work/internal/autoclaw"
	"wild-work/internal/config"
	"wild-work/internal/intl"
	"wild-work/internal/platform"
	"wild-work/internal/pool"
	"wild-work/internal/provider"
	"wild-work/internal/qoder"
	"wild-work/internal/scheduler"
	"wild-work/internal/server"
	"wild-work/internal/systray"
	"wild-work/internal/traework"
	"wild-work/internal/upstream"
	"wild-work/internal/zcode"
)

//go:embed all:web
var webFS embed.FS

//go:embed build/trayicon.ico
var trayIconICO []byte

func main() {
	// 工作目录固定为 exe 所在目录，保证相对路径配置（./auths ./data）稳定
	if exe, err := os.Executable(); err == nil {
		_ = os.Chdir(filepath.Dir(exe))
	}

	cfgPath := "config.json"
	cfg, err := config.Load(cfgPath)
	// --autostart 由开机自启项附带：开机启动不弹提示
	// --no-tray 无头模式（无桌面 Linux/服务器）
	autostart := false
	noTray := false
	for _, arg := range os.Args[1:] {
		switch arg {
		case "--autostart":
			autostart = true
		case "--no-tray":
			noTray = true
		}
	}
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			log.Printf("config.json 不存在，使用默认配置并生成")
			cfg = config.Default()
			if err := config.Save(cfg, cfgPath); err != nil {
				log.Printf("write default config: %v", err)
			}
		} else {
			fatal("加载配置失败：%v\n\n请检查 config.json 后重新启动", err)
		}
	}
	_ = os.MkdirAll(cfg.AuthDir, 0o755)
	_ = os.MkdirAll(filepath.Dir(cfg.StateFile), 0o755)

	// 多渠道运行时：workbuddy / traework / qoder 各自独立 pool + state + scheduler。
	stateDir := filepath.Dir(cfg.StateFile)
	// 旧版兼容：迁移 state.json → 分渠道 state-{kind}.json
	migrateStateFiles(cfg.StateFile, stateDir)

	wbAuths, err := auth.LoadWorkBuddyDir(cfg.AuthDir, cfg.Region)
	if err != nil {
		fatal("读取 WorkBuddy 账号目录失败：%v", err)
	}
	trAuths, err := auth.LoadTraeDir(cfg.AuthDir)
	if err != nil {
		fatal("读取 TraeWork 账号目录失败：%v", err)
	}
	qdAuths, err := auth.LoadQoderDir(cfg.AuthDir)
	if err != nil {
		fatal("读取 Qoder 账号目录失败：%v", err)
	}
	cbAuths, err := auth.LoadCodeBuddyDir(cfg.AuthDir)
	if err != nil {
		fatal("读取 CodeBuddy(intl) 账号目录失败：%v", err)
	}
	wbAuthsI, err := auth.LoadWBIntlDir(cfg.AuthDir)
	if err != nil {
		fatal("读取 WorkBuddy(intl) 账号目录失败：%v", err)
	}
	zcAuths, err := auth.LoadZCodeDir(cfg.AuthDir)
	if err != nil {
		fatal("读取 ZCode 账号目录失败：%v", err)
	}
	acAuths, err := auth.LoadAutoClawDir(cfg.AuthDir)
	if err != nil {
		fatal("读取 AutoClaw 账号目录失败：%v", err)
	}
	log.Printf("loaded accounts: workbuddy=%d %s, traework=%d, qoder=%d, codebuddy=%d, wbintl=%d, zcode=%d, autoclaw=%d from %s", len(wbAuths), cfg.Region, len(trAuths), len(qdAuths), len(cbAuths), len(wbAuthsI), len(zcAuths), len(acAuths), cfg.AuthDir)

	wbPool := pool.New(filepath.Join(stateDir, "state-workbuddy.json"))
	for _, a := range wbAuths {
		wbPool.Add(a)
	}
	trPool := pool.New(filepath.Join(stateDir, "state-traework.json"))
	for _, a := range trAuths {
		trPool.Add(a)
	}
	qdPool := pool.New(filepath.Join(stateDir, "state-qoder.json"))
	for _, a := range qdAuths {
		qoder.EnsureFingerprint(a) // 老凭证补机器指纹
		qdPool.Add(a)
	}
	cbPool := pool.New(filepath.Join(stateDir, "state-codebuddy.json"))
	for _, a := range cbAuths {
		cbPool.Add(a)
	}
	wbIntlPool := pool.New(filepath.Join(stateDir, "state-wbintl.json"))
	for _, a := range wbAuthsI {
		wbIntlPool.Add(a)
	}
	zcPool := pool.New(filepath.Join(stateDir, "state-zcode.json"))
	for _, a := range zcAuths {
		zcPool.Add(a)
	}
	acPool := pool.New(filepath.Join(stateDir, "state-autoclaw.json"))
	for _, a := range acAuths {
		acPool.Add(a)
	}

	wbUp := upstream.New()
	wbUp.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	trUp := traework.New()
	trUp.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	qdUp := qoder.New()
	qdUp.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	cbUp := intl.New()
	cbUp.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	// WorkBuddy 国际版与 CodeBuddy 同协议，仅域名不同
	wbIntlUp := intl.NewWithBase("https://www.workbuddy.ai")
	wbIntlUp.Channel = "wbintl"
	wbIntlUp.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	zcUp := zcode.New()
	zcUp.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	acUp := autoclaw.New()
	acUp.HTTP.Timeout = time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second
	checkinMinutes, err := config.ParseClockTimes(cfg.Schedule.CheckinTimes)
	if err != nil {
		fatal("解析签到时间失败：%v", err)
	}

	wbSch := scheduler.New(scheduler.Config{Pool: wbPool, Upstream: wbUp, Name: "workbuddy", CheckinMinutes: checkinMinutes, KeepaliveHours: cfg.Schedule.KeepaliveHours})
	trSch := scheduler.New(scheduler.Config{Pool: trPool, Upstream: trUp, Name: "traework", CheckinMinutes: checkinMinutes, KeepaliveHours: cfg.Schedule.KeepaliveHours})
	// Qoder 无签到活动：调度器只做 token keepalive（每日 refresh 保活）
	qdSch := scheduler.New(scheduler.Config{Pool: qdPool, Upstream: qdUp, Name: "qoder", CheckinMinutes: nil, KeepaliveHours: cfg.Schedule.KeepaliveHours})
	// 国际版 CodeBuddy 亦无签到活动；token 有效期约一年，keepalive 保持配置一致
	cbSch := scheduler.New(scheduler.Config{Pool: cbPool, Upstream: cbUp, Name: "codebuddy", CheckinMinutes: nil, KeepaliveHours: cfg.Schedule.KeepaliveHours})
	// WorkBuddy 国际版同后端无签到，仅 keepalive
	wbIntlSch := scheduler.New(scheduler.Config{Pool: wbIntlPool, Upstream: wbIntlUp, Name: "wbintl", CheckinMinutes: nil, KeepaliveHours: cfg.Schedule.KeepaliveHours})
	// ZCode 无签到活动（CheckinMinutes=nil）；JWT 无刷新，keepalive 仅为空转
	zcSch := scheduler.New(scheduler.Config{Pool: zcPool, Upstream: zcUp, Name: "zcode", CheckinMinutes: nil, KeepaliveHours: cfg.Schedule.KeepaliveHours})
	// AutoClaw 有每日任务（daily_signin 等）可主动领取，纳入全局签到时间
	acSch := scheduler.New(scheduler.Config{Pool: acPool, Upstream: acUp, Name: "autoclaw", CheckinMinutes: checkinMinutes, KeepaliveHours: cfg.Schedule.KeepaliveHours})

	runtimes := map[provider.Kind]*server.Runtime{
		provider.WorkBuddy: {Kind: provider.WorkBuddy, Pool: wbPool, Upstream: wbUp, StaticModels: server.WorkBuddyStaticModels()},
		provider.TraeWork:  {Kind: provider.TraeWork, Pool: trPool, Upstream: trUp, StaticModels: server.TraeWorkStaticModels()},
		provider.Qoder:     {Kind: provider.Qoder, Pool: qdPool, Upstream: qdUp, StaticModels: qoder.StaticModels()},
		provider.CodeBuddy: {Kind: provider.CodeBuddy, Pool: cbPool, Upstream: cbUp, StaticModels: intl.StaticModels()},
		provider.WBIntl:    {Kind: provider.WBIntl, Pool: wbIntlPool, Upstream: wbIntlUp, StaticModels: intl.StaticModels()},
		provider.ZCode:     {Kind: provider.ZCode, Pool: zcPool, Upstream: zcUp, StaticModels: zcode.StaticModels()},
		provider.AutoClaw:  {Kind: provider.AutoClaw, Pool: acPool, Upstream: acUp, StaticModels: autoclaw.StaticModels()},
	}
	appRuntimes := map[provider.Kind]*app.Runtime{
		provider.WorkBuddy: {Kind: provider.WorkBuddy, Pool: wbPool, Upstream: wbUp, Scheduler: wbSch},
		provider.TraeWork:  {Kind: provider.TraeWork, Pool: trPool, Upstream: trUp, Scheduler: trSch},
		provider.Qoder:     {Kind: provider.Qoder, Pool: qdPool, Upstream: qdUp, Scheduler: qdSch},
		provider.CodeBuddy: {Kind: provider.CodeBuddy, Pool: cbPool, Upstream: cbUp, Scheduler: cbSch},
		provider.WBIntl:    {Kind: provider.WBIntl, Pool: wbIntlPool, Upstream: wbIntlUp, Scheduler: wbIntlSch},
		provider.ZCode:     {Kind: provider.ZCode, Pool: zcPool, Upstream: zcUp, Scheduler: zcSch},
		provider.AutoClaw:  {Kind: provider.AutoClaw, Pool: acPool, Upstream: acUp, Scheduler: acSch},
	}

	appInst, err := app.New(app.Options{
		ConfigPath: cfgPath,
		Config:     cfg,
		Runtimes:   appRuntimes,
	})
	if err != nil {
		fatal("初始化失败：%v", err)
	}
	defer appInst.Close()

	// 调度器结果写日志（无 GUI 推送，日志即面板数据源）
	wbSch.SetCheckinObserver(func(r scheduler.CheckinResult) { appInst.NotifyCheckin("workbuddy", r) })
	trSch.SetCheckinObserver(func(r scheduler.CheckinResult) { appInst.NotifyCheckin("traework", r) })
	qdSch.SetCheckinObserver(func(r scheduler.CheckinResult) { appInst.NotifyCheckin("qoder", r) })
	cbSch.SetCheckinObserver(func(r scheduler.CheckinResult) { appInst.NotifyCheckin("codebuddy", r) })
	wbIntlSch.SetCheckinObserver(func(r scheduler.CheckinResult) { appInst.NotifyCheckin("wbintl", r) })
	zcSch.SetCheckinObserver(func(r scheduler.CheckinResult) { appInst.NotifyCheckin("zcode", r) })
	acSch.SetCheckinObserver(func(r scheduler.CheckinResult) { appInst.NotifyCheckin("autoclaw", r) })
	wbSch.SetRefreshObserver(func(uid string, ok bool, msg string) { appInst.NotifyRefresh("workbuddy", uid, ok, msg) })
	trSch.SetRefreshObserver(func(uid string, ok bool, msg string) { appInst.NotifyRefresh("traework", uid, ok, msg) })
	qdSch.SetRefreshObserver(func(uid string, ok bool, msg string) { appInst.NotifyRefresh("qoder", uid, ok, msg) })
	cbSch.SetRefreshObserver(func(uid string, ok bool, msg string) { appInst.NotifyRefresh("codebuddy", uid, ok, msg) })
	wbIntlSch.SetRefreshObserver(func(uid string, ok bool, msg string) { appInst.NotifyRefresh("wbintl", uid, ok, msg) })
	zcSch.SetRefreshObserver(func(uid string, ok bool, msg string) { appInst.NotifyRefresh("zcode", uid, ok, msg) })
	acSch.SetRefreshObserver(func(uid string, ok bool, msg string) { appInst.NotifyRefresh("autoclaw", uid, ok, msg) })

	// HTTP handler：OpenAI 端点 + Web UI + 管理 API
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		fatal("embed web: %v", err)
	}
	h := server.NewHandler(server.Config{
		Runtimes:     runtimes,
		APIKey:       cfg.APIKey,
		HardCooldown: cfg.HardCreditDur,
		SoftCooldown: cfg.SoftRateDur,
		ErrThreshold: cfg.Cooldown.ErrThresh,
		ErrCooldown:  cfg.ErrCooldownDur,
		WebUI:        sub,
		AttachAPI:    appInst.HandleAPI,
	})
	appInst.SetHandler(h)

	if err := appInst.StartServer(); err != nil {
		fatal("监听 %s 失败：%v\n\n可能已有一个实例正在运行。\n请用托盘“退出”，或先结束占用该端口的进程后再启动。", cfg.Listen.Addr(), err)
	}

	// 调度器后台运行
	sctx, stop := context.WithCancel(context.Background())
	defer stop()
	go wbSch.Run(sctx)
	go trSch.Run(sctx)
	go qdSch.Run(sctx)
	go cbSch.Run(sctx)
	go wbIntlSch.Run(sctx)
	go zcSch.Run(sctx)
	go acSch.Run(sctx)

	// 启动提示（非 --autostart）：系统通知
	if !autostart && !noTray {
		platform.Notify("wild-work 已启动",
			fmt.Sprintf("OpenAI 兼容 API 地址：\nhttp://%s:%d\n\n点击右下角托盘图标或菜单打开主界面。", displayHost(cfg), cfg.Listen.Port))
	}

	if noTray {
		// 无头模式：打印信息，阻塞等待信号
		addr := cfg.Listen.Addr()
		host := displayHost(cfg)
		fmt.Printf("wild-work headless mode started\n")
		fmt.Printf("  OpenAI API:    http://%s:%d/v1\n", host, cfg.Listen.Port)
		fmt.Printf("  API-Key:       %s\n", cfg.APIKey)
		fmt.Printf("  Web UI:        http://%s:%d/\n", host, cfg.Listen.Port)
		fmt.Printf("  Listen:        %s\n", addr)
		fmt.Printf("  Accounts:      workbuddy=%d traework=%d qoder=%d codebuddy=%d wbintl=%d zcode=%d autoclaw=%d\n", len(wbAuths), len(trAuths), len(qdAuths), len(cbAuths), len(wbAuthsI), len(zcAuths), len(acAuths))
		fmt.Printf("\nPress Ctrl+C to exit\n")
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
		<-sig
		fmt.Printf("\nShutting down...\n")
		stop()
		appInst.Stop()
		return
	}

	// 系统托盘（阻塞）。无桌面环境（如 SSH/服务会话）下托盘初始化失败时
	// 捕获 panic，提示用户使用 --no-tray 参数启动。
	func() {
		defer func() {
			if r := recover(); r != nil {
				fmt.Fprintf(os.Stderr, "Tray init failed: %v\n", r)
				fmt.Fprintf(os.Stderr, "Use --no-tray to run in headless mode\n")
				stop()
				appInst.Stop()
				os.Exit(1)
			}
		}()
		systray.Run(trayIconICO, "ZeroGate — 渠道聚合代理", systray.Actions{
		OpenUI: func() {
			_ = platform.OpenURL(fmt.Sprintf("http://%s:%d/", displayHost(cfg), cfg.Listen.Port))
		},
		OpenLog: func() {
			if err := appInst.OpenLogFile(); err != nil {
				log.Printf("open log: %v", err)
			}
		},
		Quit: func() {
			if platform.AskYesNo("wild-work", "确定退出 wild-work 吗？") {
				stop()
				appInst.Stop()
				os.Exit(0)
			}
		},
	})
	}()
}

func displayHost(cfg *config.Config) string {
	if cfg.Listen.Host == "" || cfg.Listen.Host == "0.0.0.0" || cfg.Listen.Host == "::" {
		return "127.0.0.1"
	}
	return cfg.Listen.Host
}


// fatal 记录日志并弹出系统提示后退出。
func fatal(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	log.Printf("%s", msg)
	platform.InfoBox("wild-work", msg)
	os.Exit(1)
}

// migrateStateFiles 旧版 state.json 兼容：旧版 WorkBuddy 状态文件名为 state.json，
// 新版改为 state-workbuddy.json。若旧文件存在且新文件不存在，则重命名。
// TraeWork 状态文件始终是 state-traework.json，无需迁移。
func migrateStateFiles(oldStateFile, stateDir string) {
	newFp := filepath.Join(stateDir, "state-workbuddy.json")
	if _, err := os.Stat(newFp); err == nil {
		return // 新文件已存在
	}
	if _, err := os.Stat(oldStateFile); err != nil {
		return // 旧文件不存在
	}
	// 读旧文件确认是有效的 state JSON
	raw, err := os.ReadFile(oldStateFile)
	if err != nil {
		return
	}
	var sf struct {
		Accounts map[string]json.RawMessage `json:"accounts"`
	}
	if err := json.Unmarshal(raw, &sf); err != nil {
		log.Printf("state.json 解析失败，跳过迁移: %v", err)
		return
	}
	if err := os.WriteFile(newFp, raw, 0o600); err != nil {
		log.Printf("迁移 state.json 失败: %v", err)
		return
	}
	_ = os.Rename(oldStateFile, oldStateFile+".old")
	log.Printf("已迁移 state.json → state-workbuddy.json")
}
