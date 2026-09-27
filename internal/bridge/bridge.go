// Package bridge 本地服务编排：面板「本地桥」Tab 的状态探测 / 启动 / 停止。
// 覆盖四组：模型桥（豆包桥+zcode 实例）、应用（chatui/验证码服务）、
// 自动化（cloudflared 隧道/Antigravity 探针）、外部代理（CLIProxyAPI/
// copilot2api/AutoClaw 客户端，本机自启动，仅支持探测+拉起，不允许面板停止）。
// 停止只针对本包拉起的进程句柄；探测一律走 HTTP/进程查询（幂等，无需句柄）。
package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"

	"zerogate/internal/platform"
)

// Instance 一个可托管的本地服务。
type Instance struct {
	ID     string `json:"id"`     // doubao | acc-big | chatui ...
	Name   string `json:"name"`   // 显示名
	Port   int    `json:"port"`   // 探测端口（0 = 进程探测）
	Kind   string `json:"kind"`   // doubao|zcode|app|mailcode|chatui|tunnel|cpa|copilot|autoclaw
	Group  string `json:"group"`  // bridge|apps|auto|external
	NoStop bool   `json:"no_stop"` // true = 不允许从面板停止（外部托管应用）

	// Autostart 开机自启：daemon 启动时由本编排器统一拉起（隐藏窗口）。
	// 收编原先散落在注册表 Run 键里的 start_bridge.bat / start_chatui.bat 等，
	// 那些脚本各弹一个 cmd 窗口，用户误关还会连带杀掉服务。
	Autostart bool `json:"autostart"`

	desc string // 面板提示语
}

// Desc 描述文案。
func (i Instance) Desc() string { return i.desc }

// GroupNames 分组显示名（面板渲染顺序与此一致）。
var GroupNames = []struct {
	Key  string `json:"key"`
	Name string `json:"name"`
	Desc string `json:"desc"`
}{
	{Key: "bridge", Name: "模型桥", Desc: "网关依赖的本地模型桥，停了对应渠道就断"},
	{Key: "apps", Name: "应用服务", Desc: "对话台与验证码助手等配套 Web 服务"},
	{Key: "auto", Name: "自动化与隧道", Desc: "公网隧道、计划任务探针（外部定时器）"},
	{Key: "external", Name: "外部托管应用", Desc: "开机自启动的常驻应用，面板可探测/拉起，不建议在此停止"},
}

// Manager 桥进程编排器。
type Manager struct {
	instances   []Instance
	zcodeDir    string // D:/wild-work/research/zcode-proxy
	doubaoDir   string // D:/wild-work/research/doubao2api
	doubaoKey   string // /stats 鉴权 Bearer
	bridgeKey   string // zcode /quota 鉴权 Bearer
	cpaKey      string // CLIProxyAPI /v1/models 鉴权 Bearer
	copilotKey  string // copilot2api /v1/models 鉴权 Bearer
	httpClient  *http.Client
	stopCh      chan struct{}
	once        sync.Once
	mu          sync.Mutex
	handles     map[string]*exec.Cmd
	lastAction  map[string]string
	lastActionT map[string]time.Time
}

// Config 构建参数。
type Config struct {
	ZcodeDir   string
	DoubaoDir  string
	DoubaoKey  string
	BridgeKey  string
	CPAKey     string
	CopilotKey string
}

// NewManager 构造编排器，目录缺省用相对工作区路径定位。
func NewManager(cfg Config) *Manager {
	zd := strings.TrimSpace(cfg.ZcodeDir)
	if zd == "" {
		zd = resolveWorkspacePath(filepath.Join("research", "zcode-proxy"))
	}
	dd := strings.TrimSpace(cfg.DoubaoDir)
	if dd == "" {
		dd = resolveWorkspacePath(filepath.Join("research", "doubao2api"))
	}
	dk := strings.TrimSpace(cfg.DoubaoKey)
	if dk == "" {
		dk = "DoubaoAPI"
	}
	ck := strings.TrimSpace(cfg.CPAKey)
	if ck == "" {
		ck = "CodexAPI"
	}
	pk := strings.TrimSpace(cfg.CopilotKey)
	if pk == "" {
		pk = "sk-7199d8a2-72e9-4785-9904-a45aac9594dc"
	}
	m := &Manager{
		instances: []Instance{
			// ── 模型桥 ──
			{ID: "doubao", Name: "豆包桥 (doubao2api)", Port: 7868, Kind: "doubao", Group: "bridge", Autostart: true,
				desc: "豆包视觉 API 桥（research/doubao2api，7868）"},
			{ID: "acc-big", Name: "主号bigmodel", Port: 18083, Kind: "zcode", Group: "bridge",
				desc: "ZCode 桥实例 acc-big（300M Weekend Build）"},
			{ID: "acc-fuka", Name: "副卡gmail", Port: 18084, Kind: "zcode", Group: "bridge",
				desc: "ZCode 桥实例 acc-fuka（300M + 每日）"},
			{ID: "acc-cn", Name: "副卡bigmodel", Port: 18085, Kind: "zcode", Group: "bridge",
				desc: "ZCode 桥实例 acc-cn（每日额度）"},
			{ID: "acc-fuka2", Name: "主号gmail", Port: 18086, Kind: "zcode", Group: "bridge",
				desc: "ZCode 桥实例 acc-fuka2（备用/自用）"},
			// ── 应用服务 ──
			{ID: "chatui", Name: "ChatUI 对话台", Port: 7870, Kind: "chatui", Group: "apps", Autostart: true,
				desc: "本地聚合对话台（tools/chatui，7870；自带记忆/技能/定时任务）"},
			{ID: "mailcode", Name: "邮箱验证码服务", Port: 7864, Kind: "mailcode", Group: "apps",
				desc: "QQ 邮箱 IMAP 抓 Atria 验证码 + 自动收 Key（src-true/tools，7864）"},
			// ── 自动化与隧道 ──
			{ID: "cloudflared", Name: "Cloudflare 隧道", Port: 0, Kind: "tunnel", Group: "auto", Autostart: true,
				desc: "公网入口 https://chatui.my123.bond（cloudflared tunnel run chatui）"},
			{ID: "antigravity", Name: "Antigravity 额度探针", Port: 0, Kind: "probe", Group: "auto",
				desc: "计划任务 AntigravityQuotaProbe：每 15 分钟核对额度/盗用告警（外部 schtasks 触发）"},
			// ── 外部托管应用 ──
			{ID: "cpa", Name: "CLIProxyAPI (gpt_gemini)", Port: 8317, Kind: "cpa", Group: "external", Autostart: true,
				desc: "Codex/Gemini 聚合代理（research/CLIProxyAPI，8317）"},
			{ID: "copilot", Name: "copilot2api (Copilot)", Port: 4141, Kind: "copilot", Group: "external", Autostart: true,
				desc: "GitHub Copilot 代理（research/copilot2api-go，4141 + 控制台 3900）"},
			{ID: "autoclaw", Name: "AutoClaw 客户端", Port: 18789, Kind: "autoclaw", Group: "external", NoStop: true,
				desc: "官方桌面客户端 D:\\autoclaw\\AutoClaw.exe（登录态/本地网关 18789 托管在此，由用户自行启停）"},
		},
		zcodeDir:    zd,
		doubaoDir:   dd,
		doubaoKey:   dk,
		bridgeKey:   cfg.BridgeKey,
		cpaKey:      ck,
		copilotKey:  pk,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
		stopCh:      make(chan struct{}),
		handles:     map[string]*exec.Cmd{},
		lastAction:  map[string]string{},
		lastActionT: map[string]time.Time{},
	}
	go m.logTailLoop()
	return m
}

// Instances 托管实例清单。
func (m *Manager) Instances() []Instance { return m.instances }

// Close 停止后台协程（不杀桥进程：桥是常驻服务，随 daemon 退出而退出反而不对）。
func (m *Manager) Close() { m.once.Do(func() { close(m.stopCh) }) }

// Status 单实例状态。
type Status struct {
	Instance
	Online   bool   `json:"online"`
	Running  bool   `json:"running"`         // 本进程拉起且存活
	Detail   string `json:"detail"`          // 附加信息（模型数/启动时间等）
	Error    string `json:"error,omitempty"` // 最近一次操作错误
	LastAct  string `json:"last_action,omitempty"`
	LastActT string `json:"last_action_at,omitempty"`
}

// ProbeAll 探测全部实例。
func (m *Manager) ProbeAll() []Status {
	out := make([]Status, 0, len(m.instances))
	for _, ins := range m.instances {
		out = append(out, m.probe(ins))
	}
	return out
}

// ProbeOne 探测单个实例。
func (m *Manager) ProbeOne(id string) (Status, error) {
	ins, err := m.find(id)
	if err != nil {
		return Status{}, err
	}
	return m.probe(ins), nil
}

func (m *Manager) find(id string) (Instance, error) {
	for _, ins := range m.instances {
		if ins.ID == id {
			return ins, nil
		}
	}
	return Instance{}, fmt.Errorf("unknown bridge instance %s", id)
}

func (m *Manager) probe(ins Instance) Status {
	st := Status{Instance: ins}
	m.mu.Lock()
	cmd := m.handles[ins.ID]
	act, actT := m.lastAction[ins.ID], m.lastActionT[ins.ID]
	m.mu.Unlock()
	st.Running = cmd != nil && cmd.Process != nil && cmd.ProcessState == nil
	if act != "" {
		st.LastAct, st.LastActT = act, actT.Format("01-02 15:04:05")
	}
	switch ins.Kind {
	case "doubao":
		body, err := m.httpGet(ins, "/stats", m.doubaoKey)
		if err != nil {
			st.Detail = probeFailReason(err)
			return st
		}
		var v struct {
			Service   string         `json:"service"`
			Port      int            `json:"port"`
			StartedAt string         `json:"started_at"`
			ByModel   map[string]any `json:"by_model"`
		}
		if json.Unmarshal(body, &v) == nil {
			st.Online = true
			st.Detail = "服务正常"
			if v.StartedAt != "" {
				st.Detail += " · 启动于 " + v.StartedAt
			}
		}
	case "zcode":
		body, err := m.httpGet(ins, "/quota", m.bridgeKey)
		if err != nil {
			st.Detail = probeFailReason(err)
			return st
		}
		var q struct {
			Balances []struct {
				Currency string `json:"currency"`
				Total    string `json:"total"`
			} `json:"balances"`
		}
		if json.Unmarshal(body, &q) == nil {
			st.Online = true
			if len(q.Balances) > 0 {
				st.Detail = fmt.Sprintf("在线 · %s %s", q.Balances[0].Currency, q.Balances[0].Total)
			} else {
				st.Detail = "在线"
			}
		}
	case "chatui", "cpa", "copilot":
		body, err := m.httpGet(ins, "/v1/models", m.kindKey(ins.Kind))
		if err != nil {
			// 拿不到合法 JSON 响应：端口通就先按"启动中"给出可见反馈
			// （chatui 冷启动 import 慢，/v1/models 前几次可能 404/超时）
			if portAccept(ins.Port) {
				st.Online = true
				st.Detail = "端口已就绪，模型接口预热中"
			} else {
				st.Detail = probeFailReason(err)
			}
			return st
		}
		var v struct {
			Data []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(body, &v) == nil {
			st.Online = true
			st.Detail = fmt.Sprintf("在线 · %d 个模型", len(v.Data))
		}
	case "mailcode":
		// 无公开健康端点：端口通即视为在线
		if portAccept(ins.Port) {
			st.Online = true
			st.Detail = "在线（7864）"
		}
	case "tunnel":
		// cloudflared 无本地 HTTP 探测口：进程在即在线（面板启动的用句柄，自启用进程名匹配）
		if st.Running || procExists("cloudflared.exe") {
			st.Online = true
			st.Detail = "隧道进程运行中 · chatui.my123.bond"
		}
	case "autoclaw":
		if procExists("AutoClaw.exe") && portAccept(ins.Port) {
			st.Online = true
			st.Detail = "客户端运行中 · 本地网关 18789 就绪"
		} else if procExists("AutoClaw.exe") {
			st.Online = true
			st.Detail = "客户端运行中 · 本地网关未就绪（可能登录中）"
		}
	case "probe":
		ok, last := schtaskState("AntigravityQuotaProbe")
		if ok {
			st.Online = true
			st.Detail = "计划任务就绪 · 上次运行 " + last
		} else if last != "" {
			st.Error = "计划任务异常（上次结果 " + last + "）"
		}
	}
	return st
}

func (m *Manager) kindKey(kind string) string {
	switch kind {
	case "cpa":
		return m.cpaKey
	case "copilot":
		return m.copilotKey
	default:
		return ""
	}
}

// probeFailReason 把探测失败的原始错误放进 Detail：面板上直接看出"为什么离线"，
// 而不是只显示 OFFLINE 让人无从判断（2026-09-27）。
func probeFailReason(err error) string {
	if err == nil {
		return ""
	}
	return "离线原因: " + fmt.Sprintf("%.110s", err.Error())
}

func (m *Manager) httpGet(ins Instance, path, key string) ([]byte, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", ins.Port, path)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := m.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	buf := make([]byte, 0, 4096)
	tmp := make([]byte, 1024)
	for {
		n, err := resp.Body.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if err != nil {
			break
		}
		if len(buf) > 1<<20 {
			break
		}
	}
	return buf, nil
}

// hideWindow 隐藏子进程控制台窗口（转调 platform，保持单点实现）。
// daemon 自身以 GUI 子系统构建（无控制台），但被拉起的服务多为控制台程序
// （python.exe / cloudflared.exe / zcode-proxy.exe 等），不设此标志会各自弹出黑框。
func hideWindow(cmd *exec.Cmd) { platform.HideWindow(cmd) }

// resolveWorkspacePath 动态定位工作区路径：优先相对当前目录或父目录，回退到历史盘符。
func resolveWorkspacePath(rel string) string {
	cleanRel := filepath.Clean(rel)
	// 1. 如果当前工作目录直接存在该路径
	if _, err := os.Stat(cleanRel); err == nil {
		if abs, err := filepath.Abs(cleanRel); err == nil {
			return abs
		}
		return cleanRel
	}
	// 2. 如果在 src-true 运行，尝试上级目录 ..
	parentRel := filepath.Join("..", cleanRel)
	if _, err := os.Stat(parentRel); err == nil {
		if abs, err := filepath.Abs(parentRel); err == nil {
			return abs
		}
		return parentRel
	}
	// 3. 回退历史默认绝对路径
	return filepath.Join(`D:\wild-work`, cleanRel)
}

// resolvePython 探测 Python 解释器。preferGui 为 true 时优先 pythonw。
func resolvePython(preferGui bool) string {
	if preferGui {
		if v := os.Getenv("PYTHONW_PATH"); v != "" {
			return v
		}
		for _, candidate := range []string{
			`D:\anaconda\pythonw.exe`,
			`C:\anaconda3\pythonw.exe`,
			`C:\ProgramData\anaconda3\pythonw.exe`,
		} {
			if _, err := os.Stat(candidate); err == nil {
				return candidate
			}
		}
		if p, err := exec.LookPath("pythonw"); err == nil {
			return p
		}
		return "pythonw"
	}

	if v := os.Getenv("PYTHON_PATH"); v != "" {
		return v
	}
	for _, candidate := range []string{
		`D:\anaconda\python.exe`,
		`C:\anaconda3\python.exe`,
		`C:\ProgramData\anaconda3\python.exe`,
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	if p, err := exec.LookPath("python"); err == nil {
		return p
	}
	return "python"
}

// resolveExe 探测外部可执行程序路径。
func resolveExe(name string, fallbackPaths ...string) string {
	if v := os.Getenv(strings.ToUpper(strings.TrimSuffix(name, ".exe")) + "_EXE"); v != "" {
		if _, err := os.Stat(v); err == nil {
			return v
		}
	}
	for _, fb := range fallbackPaths {
		if _, err := os.Stat(fb); err == nil {
			return fb
		}
	}
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	if len(fallbackPaths) > 0 {
		return fallbackPaths[0]
	}
	return name
}

// hiddenOutput 执行一次性探测命令并隐藏其控制台窗口。
// 探测用的 tasklist/schtasks 同样是控制台程序：面板每次刷新都会调用，
// 不加隐藏标志会在每次刷新时闪一个黑框（用户视角=服务"一直在弹 cmd"）。
func hiddenOutput(name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	hideWindow(cmd)
	return cmd.Output()
}

// Start 启动单个服务（已在线则幂等跳过）。
func (m *Manager) Start(ctx context.Context, id string) (Status, error) {
	ins, err := m.find(id)
	if err != nil {
		return Status{}, err
	}
	if ins.Kind == "probe" {
		// 计划任务不由 daemon 拉起：提示如何手动触发
		return m.probe(ins), fmt.Errorf("探针由 Windows 计划任务（每 15 分钟）驱动，无需手动启动")
	}
	if st := m.probe(ins); st.Online {
		m.note(ins.ID, "启动：已在运行，跳过")
		return st, nil
	}
	var cmd *exec.Cmd
	switch ins.Kind {
	case "doubao":
		py := resolvePython(true)
		cmd = exec.Command(py, "bridge.py")
		cmd.Dir = m.doubaoDir
		cmd.Env = append(os.Environ(), "PYTHONPATH="+m.doubaoDir)
	case "zcode":
		exe := filepath.Join(m.zcodeDir, "zcode-proxy.exe")
		accDir := filepath.Join(m.zcodeDir, "instances", ins.ID)
		cfgYaml := filepath.Join(accDir, "config.yaml")
		if _, err := os.Stat(cfgYaml); err != nil {
			return Status{}, fmt.Errorf("实例配置不存在: %s", cfgYaml)
		}
		creds := filepath.Join(accDir, "creds")
		if _, err := os.Stat(creds); err != nil {
			return Status{}, fmt.Errorf("实例凭证目录不存在: %s", creds)
		}
		cmd = exec.Command(exe, "--cli", "serve", cfgYaml)
		cmd.Dir = m.zcodeDir
		cmd.Env = append(os.Environ(),
			"ZCODE_CREDENTIALS_DIR="+creds,
			"ZCODE_CLAIM_ENABLED=false",
			"HTTPS_PROXY=", "HTTP_PROXY=", "ALL_PROXY=",
		)
	case "chatui":
		py := resolvePython(false)
		dir := resolveWorkspacePath(filepath.Join("tools", "chatui"))
		serverScript := filepath.Join(dir, "server.py")
		if _, err := os.Stat(serverScript); err != nil {
			return Status{}, fmt.Errorf("chatui 不存在: %s", serverScript)
		}
		cmd = exec.Command(py, "server.py")
		cmd.Dir = dir
	case "mailcode":
		py := resolvePython(true)
		script := resolveWorkspacePath(filepath.Join("src-true", "tools", "mail_code_server.py"))
		if _, err := os.Stat(script); err != nil {
			script = resolveWorkspacePath(filepath.Join("tools", "mail_code_server.py"))
		}
		if _, err := os.Stat(script); err != nil {
			return Status{}, fmt.Errorf("脚本不存在: %s", script)
		}
		cmd = exec.Command(py, script)
		cmd.Dir = filepath.Dir(script)
	case "tunnel":
		bin := resolveExe("cloudflared.exe", `D:\cloudflared.exe`, `C:\cloudflared.exe`)
		if _, err := os.Stat(bin); err != nil {
			if _, lerr := exec.LookPath("cloudflared"); lerr != nil {
				return Status{}, fmt.Errorf("cloudflared 不存在: %s", bin)
			}
		}
		cmd = exec.Command(bin, "tunnel", "run", "chatui")
		// 必须清代理：Clash 拦 7844 会导致隧道 530/1033
		cmd.Env = []string{
			"HTTP_PROXY=", "HTTPS_PROXY=", "http_proxy=", "https_proxy=", "ALL_PROXY=", "all_proxy=",
			"SYSTEMROOT=" + os.Getenv("SystemRoot"),
			"USERPROFILE=" + os.Getenv("USERPROFILE"),
			"APPDATA=" + os.Getenv("AppData"),
			"LOCALAPPDATA=" + os.Getenv("LocalAppData"),
			"PATH=" + os.Getenv("PATH"),
		}
	case "cpa":
		exe := resolveWorkspacePath(filepath.Join("research", "CLIProxyAPI", "bin", "server.exe"))
		cfgY := resolveWorkspacePath(filepath.Join("research", "CLIProxyAPI", "config.local.yaml"))
		if _, err := os.Stat(exe); err != nil {
			return Status{}, fmt.Errorf("server.exe 不存在: %s", exe)
		}
		cmd = exec.Command(exe, "-config", cfgY)
		cmd.Dir = filepath.Dir(exe)
	case "copilot":
		exe := resolveWorkspacePath(filepath.Join("research", "copilot2api-go", "copilot2api.exe"))
		if _, err := os.Stat(exe); err != nil {
			return Status{}, fmt.Errorf("copilot2api.exe 不存在: %s", exe)
		}
		cmd = exec.Command(exe, "--web-port", "3900", "--proxy-port", "4141")
		cmd.Dir = filepath.Dir(exe)
	case "autoclaw":
		exe := resolveExe("AutoClaw.exe", `D:\autoclaw\AutoClaw.exe`, `C:\autoclaw\AutoClaw.exe`)
		if _, err := os.Stat(exe); err != nil {
			return Status{}, fmt.Errorf("AutoClaw 不存在: %s", exe)
		}
		cmd = exec.Command(exe)
		cmd.Dir = filepath.Dir(exe)
	default:
		return Status{}, fmt.Errorf("unknown kind %s", ins.Kind)
	}
	hideWindow(cmd)
	logFile, lerr := os.OpenFile(filepath.Join(os.TempDir(), "zg-bridge-"+ins.ID+".log"),
		os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if lerr == nil {
		cmd.Stdout = logFile
		cmd.Stderr = logFile
	}
	if err := cmd.Start(); err != nil {
		if logFile != nil {
			_ = logFile.Close()
		}
		m.noteErr(ins.ID, "启动失败: "+err.Error())
		return Status{}, fmt.Errorf("启动 %s 失败: %v", ins.Name, err)
	}
	if ins.Port == 0 {
		// 无端口探测型（cloudflared）：记录句柄即视为运行
		m.mu.Lock()
		m.handles[ins.ID] = cmd
		m.mu.Unlock()
		m.note(ins.ID, "启动成功")
		log.Printf("bridge: %s 已启动 (pid=%d)", ins.ID, cmd.Process.Pid)
		st := m.probe(ins)
		st.Online = true
		return st, nil
	}
	m.mu.Lock()
	m.handles[ins.ID] = cmd
	m.mu.Unlock()
	m.note(ins.ID, "启动成功")
	log.Printf("bridge: %s 已启动 (pid=%d, port=%d)", ins.ID, cmd.Process.Pid, ins.Port)

	waitCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	t := time.NewTicker(500 * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-waitCtx.Done():
			st := m.probe(ins)
			return st, nil
		case <-t.C:
			if st := m.probe(ins); st.Online {
				return st, nil
			}
		}
	}
}

// StartAll 一键全启（串行，跳过已在线；探针为计划任务自动跳过）。
func (m *Manager) StartAll(ctx context.Context) []Status {
	out := make([]Status, 0, len(m.instances))
	for _, ins := range m.instances {
		st, err := m.Start(ctx, ins.ID)
		if err != nil {
			s := Status{Instance: ins, Error: err.Error(), LastAct: "启动失败", LastActT: time.Now().Format("01-02 15:04:05")}
			out = append(out, s)
			continue
		}
		out = append(out, st)
	}
	return out
}

// StartAutostart 开机编排：拉起所有 Autostart 实例（隐藏窗口，非阻塞）。
//
// 这是「ZeroGate 集中管理」的入口 —— 原先豆包桥 / chatui / 隧道 / CLIProxyAPI /
// copilot2api 各自在注册表 Run 键里挂一个 .bat，每个都弹一个 cmd 窗口，关掉窗口
// 还会连带杀掉服务。现在改由 daemon 单点拉起：无窗口、可探测、可从面板启停。
//
// 在后台串行执行：探测要等各服务就绪（最坏 12s/个），不能阻塞 HTTP 服务启动。
// 已在线的实例跳过 —— 桥是常驻服务，按设计随 daemon 退出而不退出（见 Close），
// 所以重启 daemon 后这些进程仍在，探测命中即不再重复拉起。
func (m *Manager) StartAutostart(ctx context.Context) {
	go func() {
		started, skipped := 0, 0
		for _, ins := range m.instances {
			if !ins.Autostart {
				continue
			}
			if st := m.probe(ins); st.Online {
				skipped++
				continue
			}
			if _, err := m.Start(ctx, ins.ID); err != nil {
				log.Printf("bridge autostart: %s 启动失败: %v", ins.ID, err)
				continue
			}
			started++
		}
		log.Printf("bridge autostart: 完成（新拉起 %d 个，已在线跳过 %d 个）", started, skipped)
	}()
}

// Stop 停止服务：仅本进程拉起的进程有句柄可杀；
// 外部/自启动托管（NoStop）拒绝停止，其余外部拉起返回明确提示。
func (m *Manager) Stop(ctx context.Context, id string) (Status, error) {
	ins, err := m.find(id)
	if err != nil {
		return Status{}, err
	}
	if ins.NoStop {
		m.note(ins.ID, "停止：被拒绝（外部托管）")
		return m.probe(ins), fmt.Errorf("%s 由系统自启动托管，面板不提供停止（如确需停用请移除对应开机自启项）", ins.Name)
	}
	m.mu.Lock()
	cmd := m.handles[ins.ID]
	delete(m.handles, ins.ID)
	m.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		m.note(ins.ID, "停止：非本面板拉起，无句柄")
		return m.probe(ins), fmt.Errorf("%s 不是本面板启动的（外部拉起），为避免误伤请用对应脚本 / 任务管理器处理", ins.Name)
	}
	_ = cmd.Process.Kill()
	done := make(chan struct{})
	go func() { _, _ = cmd.Process.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	m.note(ins.ID, "已停止")
	log.Printf("bridge: %s 已停止 (pid=%d)", ins.ID, cmd.Process.Pid)
	return m.probe(ins), nil
}

func (m *Manager) note(id, msg string) {
	m.mu.Lock()
	m.lastAction[id] = msg
	m.lastActionT[id] = time.Now()
	m.mu.Unlock()
}

func (m *Manager) noteErr(id, msg string) { m.note(id, msg) }

// logTailLoop 桥进程退出时回收句柄并记日志（避免 Running 卡 true）。
func (m *Manager) logTailLoop() {
	t := time.NewTicker(15 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-t.C:
			m.mu.Lock()
			for id, cmd := range m.handles {
				if cmd.ProcessState != nil {
					log.Printf("bridge: %s 进程已退出 (pid=%d)", id, cmd.Process.Pid)
					delete(m.handles, id)
				}
			}
			m.mu.Unlock()
		}
	}
}

// ---------------------------------------------------------------------------
// Windows 进程 / 计划任务 / 端口探测（命令行实现，无需管理员权限）
// ---------------------------------------------------------------------------

// procExists 进程名是否存在（tasklist 查询；探测频率为面板点击级，开销可忽略）。
func procExists(name string) bool {
	out, err := hiddenOutput("tasklist", "/FI", "IMAGENAME eq "+name, "/NH", "/FO", "CSV")
	if err != nil {
		return false
	}
	return strings.Contains(decodeGBK(out), name)
}

// portAccept 端口是否可建立 TCP 连接。
func portAccept(port int) bool {
	conn, err := net.DialTimeout("tcp", "127.0.0.1:"+strconv.Itoa(port), 600*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// schtaskState 查询计划任务状态：返回 (上次结果是否为 0, 上次运行时间)。
// 中文系统 schtasks 输出为 GBK 编码，须转码后再匹配标签（兼容中英文系统）。
func schtaskState(name string) (ready bool, lastRun string) {
	out, err := hiddenOutput("schtasks", "/Query", "/TN", name, "/FO", "LIST", "/V")
	if err != nil {
		return false, ""
	}
	text := decodeGBK(out)
	res := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		idx := strings.Index(line, ":")
		if idx <= 0 {
			continue
		}
		k := strings.TrimSpace(line[:idx])
		v := strings.TrimSpace(line[idx+1:])
		lk := strings.ToLower(k)
		if strings.Contains(lk, "last result") || strings.Contains(k, "上次结果") {
			res = v
		}
		if strings.Contains(lk, "last run time") || strings.Contains(k, "上次运行时间") {
			lastRun = v
		}
	}
	return res == "0", lastRun
}

// gbkDecoder 惰性初始化的 GBK 解码器（简体中文系统控制台代码页 936）。
var gbkDecoderOnce sync.Once
var gbkDecoder *encoding.Decoder

func decodeGBK(raw []byte) string {
	if utf8.Valid(raw) {
		return string(raw) // 已是 UTF-8（英文系统或被重定向处理过）
	}
	gbkDecoderOnce.Do(func() {
		gbkDecoder = simplifiedchinese.GBK.NewDecoder()
	})
	decoded, err := gbkDecoder.Bytes(raw)
	if err != nil {
		return string(raw)
	}
	return string(decoded)
}

