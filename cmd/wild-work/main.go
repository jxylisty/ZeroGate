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
	"strings"
	"syscall"
	"time"

	"wild-work/internal/app"
	"wild-work/internal/atria"
	"wild-work/internal/audit"
	"wild-work/internal/auth"
	"wild-work/internal/autoclaw"
	"wild-work/internal/clientkey"
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

	// 多渠道运行时：每个渠道一条描述符（渠道注册表），统一走
	// load → prepare → pool → upstream → scheduler 装配流水线。
	// 新增渠道只需在 channels 里加一条描述符，不再散落 main 各处的 10 余行样板。
	stateDir := filepath.Dir(cfg.StateFile)
	// 旧版兼容：迁移 state.json → 分渠道 state-{kind}.json
	migrateStateFiles(cfg.StateFile, stateDir)

	checkinMinutes, err := config.ParseClockTimes(cfg.Schedule.CheckinTimes)
	if err != nil {
		fatal("解析签到时间失败：%v", err)
	}
	upTimeout := time.Duration(cfg.Upstream.TimeoutSeconds) * time.Second

	type channelSpec struct {
		kind         provider.Kind
		name         string // 调度器/通知用的渠道名
		stateFile    string
		load         func() ([]*auth.Auth, error)
		prepare      func([]*auth.Auth) // SyncToDir 前的凭证预处理（可空）
		newUpstream  func() provider.Upstream
		checkin      bool // 参与全局签到时间窗
		staticModels func() []provider.ModelInfo
		afterSync    func(*pool.Pool) // pool 同步后的渠道专属启动项（可空）
		runScheduler bool             // false = 仅创建调度器供面板使用，不跑后台循环
	}
	channels := []channelSpec{
		{
			kind: provider.WorkBuddy, name: "workbuddy", stateFile: "state-workbuddy.json",
			load: func() ([]*auth.Auth, error) { return auth.LoadWorkBuddyDir(cfg.AuthDir, cfg.Region) },
			newUpstream: func() provider.Upstream {
				u := upstream.New()
				u.HTTP.Timeout = upTimeout
				return u
			},
			checkin: true, staticModels: server.WorkBuddyStaticModels, runScheduler: true,
		},
		{
			kind: provider.TraeWork, name: "traework", stateFile: "state-traework.json",
			load: func() ([]*auth.Auth, error) { return auth.LoadTraeDir(cfg.AuthDir) },
			newUpstream: func() provider.Upstream {
				u := traework.New()
				u.HTTP.Timeout = upTimeout
				return u
			},
			checkin: true, staticModels: server.TraeWorkStaticModels, runScheduler: true,
		},
		{
			kind: provider.Qoder, name: "qoder", stateFile: "state-qoder.json",
			load: func() ([]*auth.Auth, error) { return auth.LoadQoderDir(cfg.AuthDir) },
			prepare: func(as []*auth.Auth) {
				for _, a := range as {
					qoder.EnsureFingerprint(a) // 老凭证补机器指纹
				}
			},
			newUpstream: func() provider.Upstream {
				u := qoder.New()
				u.HTTP.Timeout = upTimeout
				return u
			},
			// Qoder 无签到活动：调度器只做 token keepalive（每日 refresh 保活）
			staticModels: qoder.StaticModels, runScheduler: true,
		},
		{
			kind: provider.CodeBuddy, name: "codebuddy", stateFile: "state-codebuddy.json",
			load: func() ([]*auth.Auth, error) { return auth.LoadCodeBuddyDir(cfg.AuthDir) },
			newUpstream: func() provider.Upstream {
				u := intl.New()
				u.HTTP.Timeout = upTimeout
				return u
			},
			// 国际版 CodeBuddy 无签到活动；token 有效期约一年，keepalive 保持配置一致
			staticModels: intl.StaticModels, runScheduler: true,
		},
		{
			kind: provider.WBIntl, name: "wbintl", stateFile: "state-wbintl.json",
			load: func() ([]*auth.Auth, error) { return auth.LoadWBIntlDir(cfg.AuthDir) },
			newUpstream: func() provider.Upstream {
				// WorkBuddy 国际版与 CodeBuddy 同协议，仅域名不同
				u := intl.NewWithBase("https://www.workbuddy.ai")
				u.Channel = "wbintl"
				u.HTTP.Timeout = upTimeout
				return u
			},
			// 同后端无签到，仅 keepalive
			staticModels: intl.StaticModels, runScheduler: true,
		},
		{
			kind: provider.ZCode, name: "zcode", stateFile: "state-zcode.json",
			load: func() ([]*auth.Auth, error) { return auth.LoadZCodeDir(cfg.AuthDir) },
			newUpstream: func() provider.Upstream {
				u := zcode.New()
				u.HTTP.Timeout = upTimeout
				return u
			},
			// ZCode 无签到活动；JWT 无刷新，keepalive 仅为空转
			afterSync: func(p *pool.Pool) {
				zcode.StartWeeklyClaim(context.Background(), p, zcode.BridgeKeyFromEnv())
			},
			staticModels: zcode.StaticModels, runScheduler: true,
		},
		{
			kind: provider.AutoClaw, name: "autoclaw", stateFile: "state-autoclaw.json",
			load: func() ([]*auth.Auth, error) { return auth.LoadAutoClawDir(cfg.AuthDir) },
			newUpstream: func() provider.Upstream {
				u := autoclaw.New()
				u.HTTP.Timeout = upTimeout
				return u
			},
			// 有每日任务（daily_signin 等）可主动领取，纳入全局签到时间
			checkin: true, staticModels: autoclaw.StaticModels, runScheduler: true,
		},
		{
			kind: provider.Atria, name: "atria", stateFile: "state-atria.json",
			load: func() ([]*auth.Auth, error) { return auth.LoadAtriaDir(cfg.AuthDir) },
			newUpstream: func() provider.Upstream {
				u := atria.New()
				u.HTTP.Timeout = upTimeout
				return u
			},
			// Atria 渠道使用静态 API Key，无需签到/keepalive，调度器仅供面板结构使用
			staticModels: atria.StaticModels, runScheduler: false,
		},
	}

	type channelRuntime struct {
		spec  channelSpec
		auths []*auth.Auth
		pool  *pool.Pool
		up    provider.Upstream
		sch   *scheduler.Scheduler
	}
	rts := make([]*channelRuntime, 0, len(channels))
	loadedParts := make([]string, 0, len(channels))
	for _, sp := range channels {
		auths, err := sp.load()
		if err != nil {
			fatal("读取 %s 账号目录失败：%v", sp.name, err)
		}
		if sp.prepare != nil {
			sp.prepare(auths)
		}
		p := pool.New(filepath.Join(stateDir, sp.stateFile))
		p.SyncToDir(auths)
		if sp.afterSync != nil {
			sp.afterSync(p)
		}
		up := sp.newUpstream()
		var minutes []int
		if sp.checkin {
			minutes = checkinMinutes
		}
		sch := scheduler.New(scheduler.Config{Pool: p, Upstream: up, Name: sp.name, CheckinMinutes: minutes, KeepaliveHours: cfg.Schedule.KeepaliveHours})
		rts = append(rts, &channelRuntime{spec: sp, auths: auths, pool: p, up: up, sch: sch})
		loadedParts = append(loadedParts, fmt.Sprintf("%s=%d", sp.name, len(auths)))
	}
	log.Printf("loaded accounts: %s (region=%s) from %s", strings.Join(loadedParts, " "), cfg.Region, cfg.AuthDir)

	runtimes := make(map[provider.Kind]*server.Runtime, len(rts))
	appRuntimes := make(map[provider.Kind]*app.Runtime, len(rts))
	for _, rt := range rts {
		runtimes[rt.spec.kind] = &server.Runtime{Kind: rt.spec.kind, Pool: rt.pool, Upstream: rt.up, StaticModels: rt.spec.staticModels()}
		appRuntimes[rt.spec.kind] = &app.Runtime{Kind: rt.spec.kind, Pool: rt.pool, Upstream: rt.up, Scheduler: rt.sch}
	}

	keyStore := clientkey.New(filepath.Join(stateDir, "keys.json"))
	auditRec := audit.New(filepath.Join(stateDir, "requests.jsonl"), 500)
	defer auditRec.Close()

	appInst, err := app.New(app.Options{
		ConfigPath: cfgPath,
		Config:     cfg,
		Runtimes:   appRuntimes,
		ClientKeys: keyStore,
		AuditRec:   auditRec,
	})
	if err != nil {
		fatal("初始化失败：%v", err)
	}
	defer appInst.Close()

	// 调度器结果写日志（无 GUI 推送，日志即面板数据源）
	for _, rt := range rts {
		name := rt.spec.name
		rt.sch.SetCheckinObserver(func(r scheduler.CheckinResult) { appInst.NotifyCheckin(name, r) })
		rt.sch.SetRefreshObserver(func(uid string, ok bool, msg string) { appInst.NotifyRefresh(name, uid, ok, msg) })
		if name == "workbuddy" {
			rt.sch.SetGrowthObserver(func(r scheduler.GrowthResult) { appInst.RecordGrowth(name, r) })
		}
	}

	// HTTP handler：OpenAI 端点 + Web UI + 管理 API
	sub, err := fs.Sub(webFS, "web")
	if err != nil {
		fatal("embed web: %v", err)
	}
	h := server.NewHandler(server.Config{
		Runtimes:       runtimes,
		APIKey:         cfg.APIKey,
		HardCooldown:   cfg.HardCreditDur,
		SoftCooldown:   cfg.SoftRateDur,
		ErrThreshold:   cfg.Cooldown.ErrThresh,
		ErrCooldown:    cfg.ErrCooldownDur,
		WebUI:          sub,
		AttachAPI:      appInst.HandleAPI,
		ClientKeys:     keyStore,
		AuditRec:       auditRec,
		ModelRedirects: cfg.ModelRedirects,
	})
	appInst.SetHandler(h)

	if err := appInst.StartServer(); err != nil {
		fatal("监听 %s 失败：%v\n\n可能已有一个实例正在运行。\n请用托盘“退出”，或先结束占用该端口的进程后再启动。", cfg.Listen.Addr(), err)
	}

	// 非回环地址告警：绑定 0.0.0.0/局域网 IP 时，匿名可读端点（如 /api/token_usage）
	// 与管理 API 会暴露给局域网内所有设备。
	if h := cfg.Listen.Host; h != "" && h != "127.0.0.1" && h != "localhost" && h != "::1" {
		log.Printf("⚠️ 安全提醒：当前监听 %s（非回环地址）。/api/token_usage 等匿名端点与管理 API 将暴露给局域网；" +
			"如无必要请改回 127.0.0.1（config.json → listen.host）。", cfg.Listen.Addr())
	}

	// 调度器后台运行（runScheduler=false 的渠道仅保留面板结构）
	sctx, stop := context.WithCancel(context.Background())
	defer stop()
	for _, rt := range rts {
		if rt.spec.runScheduler {
			go rt.sch.Run(sctx)
		}
	}

	// 启动提示（非 --autostart）：系统通知
	if !autostart && !noTray {
		platform.Notify("wild-work 已启动",
			fmt.Sprintf("OpenAI 兼容 API 地址：\nhttp://%s:%d\n\n点击右下角托盘图标或菜单打开主界面。", displayHost(cfg), cfg.Listen.Port))
	}

	if noTray {
		// 无头模式：打印信息，阻塞等待信号
		addr := cfg.Listen.Addr()
		host := displayHost(cfg)
		total := 0
		for _, rt := range rts {
			total += len(rt.auths)
		}
		fmt.Printf("wild-work headless mode started\n")
		fmt.Printf("  OpenAI API:    http://%s:%d/v1\n", host, cfg.Listen.Port)
		fmt.Printf("  API-Key:       %s\n", cfg.APIKey)
		fmt.Printf("  Web UI:        http://%s:%d/\n", host, cfg.Listen.Port)
		fmt.Printf("  Listen:        %s\n", addr)
		fmt.Printf("  Accounts:      %s (total=%d)\n", strings.Join(loadedParts, " "), total)
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
