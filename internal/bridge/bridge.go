// Package bridge 本地服务编排：面板「本地桥」Tab 的状态探测 / 启动 / 停止。
// 覆盖四组：模型桥（豆包桥+zcode 实例）、应用（chatui/验证码服务）、
// 自动化（cloudflared 隧道/Antigravity 探针）、外部代理（CLIProxyAPI/
// copilot2api/AutoClaw 客户端，本机自启动，仅支持探测+拉起，不允许面板停止）。
// 停止只针对本包拉起的进程句柄；探测一律走 HTTP/进程查询（幂等，无需句柄）。
package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"

	"zerogate/internal/platform"
)

// Instance 一个可托管的本地服务。
type Instance struct {
	ID     string `json:"id"`      // doubao | acc-big | chatui ...
	Name   string `json:"name"`    // 显示名
	Port   int    `json:"port"`    // 探测端口（0 = 进程探测）
	Kind   string `json:"kind"`    // doubao|zcode|app|mailcode|chatui|tunnel|cpa|copilot|autoclaw
	Group  string `json:"group"`   // bridge|apps|auto|external
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
	staticInstances []Instance // 静态清单（doubao/apps/auto/external）；zcode 实例动态扫描
	authDir         string     // ZeroGate auths 目录（读 zcode 账号昵称）
	zcodeDir        string     // D:/wild-work/research/zcode-proxy
	doubaoDir       string     // D:/wild-work/research/doubao2api
	doubaoKey       string     // /stats 鉴权 Bearer
	bridgeKey       string     // zcode /quota 鉴权 Bearer
	cpaKey          string     // CLIProxyAPI /v1/models 鉴权 Bearer
	copilotKey      string     // copilot2api /v1/models 鉴权 Bearer
	httpClient      *http.Client
	stopCh          chan struct{}
	once            sync.Once
	mu              sync.Mutex
	handles         map[string]*exec.Cmd
	lastAction      map[string]string
	lastActionT     map[string]time.Time

	// storm 记录（zcode captcha mint storm 自动重启 watchdog）：
	// stormCount 记录每实例 serve.log 中累计命中的 storm 行数，
	// rebootAt 记录每实例最近一次自动重启时间（防抖：10 分钟内不重复）。
	stormCount    map[string]int
	stormRebootAt map[string]time.Time

	// halfDead 记录每实例「端口可连但 /health 无响应」的连续轮数（半死桥自动重启）。
	halfDead    map[string]int
	probeClient *http.Client // 半死探测专用短超时客户端（避免每次等满 10s）
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
		staticInstances: []Instance{
			// ── 模型桥 ──
			{ID: "doubao", Name: "豆包桥 (doubao2api)", Port: 7868, Kind: "doubao", Group: "bridge", Autostart: true,
				desc: "豆包视觉 API 桥（research/doubao2api，7868）"},
			// zcode 实例（acc-*）由 zcodeInstances() 扫描 zcode-proxy/instances/ 动态生成：
			// 新增账号只需建目录 + auths，面板刷新即可见，无需改代码重编译。
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
		authDir:       resolveWorkspacePath("auths"),
		zcodeDir:      zd,
		doubaoDir:     dd,
		doubaoKey:     dk,
		bridgeKey:     cfg.BridgeKey,
		cpaKey:        ck,
		copilotKey:    pk,
		httpClient:    &http.Client{Timeout: 10 * time.Second},
		stopCh:        make(chan struct{}),
		handles:       map[string]*exec.Cmd{},
		lastAction:    map[string]string{},
		lastActionT:   map[string]time.Time{},
		stormCount:    map[string]int{},
		stormRebootAt: map[string]time.Time{},
		halfDead:      map[string]int{},
		probeClient:   &http.Client{Timeout: 3 * time.Second},
	}
	go m.logTailLoop()
	go m.stormWatchLoop()
	return m
}

// Instances 托管实例清单（静态清单 + 动态扫描的 zcode 实例）。
func (m *Manager) Instances() []Instance { return m.allInstances() }

// allInstances 组装完整清单：把动态扫描出的 zcode 实例插到 doubao 之后（模型桥组内）。
// 每次调用重新扫描，因此新增/删除账号目录后无需重启 daemon 即可在面板生效。
func (m *Manager) allInstances() []Instance {
	zc := m.zcodeInstances()
	out := make([]Instance, 0, len(m.staticInstances)+len(zc))
	inserted := false
	for _, ins := range m.staticInstances {
		out = append(out, ins)
		if !inserted && ins.ID == "doubao" {
			out = append(out, zc...)
			inserted = true
		}
	}
	if !inserted {
		out = append(zc, out...)
	}
	return out
}

// zcodeInstances 扫描 zcode-proxy/instances/acc-* 目录生成 zcode 桥实例。
// 只认同时具备 config.yaml（含 server.port）与 creds/ 的目录，避免把登录残骸当实例。
func (m *Manager) zcodeInstances() []Instance {
	base := filepath.Join(m.zcodeDir, "instances")
	entries, err := os.ReadDir(base)
	if err != nil {
		return nil
	}
	out := make([]Instance, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "acc-") {
			continue
		}
		id := e.Name()
		dir := filepath.Join(base, id)
		if _, err := os.Stat(filepath.Join(dir, "creds")); err != nil {
			continue
		}
		port := yamlServerPort(filepath.Join(dir, "config.yaml"))
		if port <= 0 {
			continue
		}
		out = append(out, Instance{
			ID:    id,
			Name:  m.zcodeDisplayName(id),
			Port:  port,
			Kind:  "zcode",
			Group: "bridge",
			desc:  "ZCode 桥实例 " + id,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Port < out[j].Port })
	return out
}

// zcodeDisplayName 取账号昵称：auths/zcode-<后缀>.json 的 nickname，缺失时回落实例 ID。
func (m *Manager) zcodeDisplayName(instanceID string) string {
	suffix := strings.TrimPrefix(instanceID, "acc-")
	raw, err := os.ReadFile(filepath.Join(m.authDir, "zcode-"+suffix+".json"))
	if err != nil {
		return instanceID
	}
	var rec struct {
		Nickname string `json:"nickname"`
	}
	if json.Unmarshal(raw, &rec) != nil || strings.TrimSpace(rec.Nickname) == "" {
		return instanceID
	}
	return rec.Nickname
}

// yamlServerPort 从实例 config.yaml 里取 server.port（格式固定，按行取首个 port: 数字）。
func yamlServerPort(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(raw), "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, "port:") {
			continue
		}
		v := strings.TrimSpace(strings.TrimPrefix(t, "port:"))
		v = strings.Trim(v, `"'`)
		if p, err := strconv.Atoi(v); err == nil && p > 0 && p < 65536 {
			return p
		}
	}
	return 0
}

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

// ProbeAll 探测全部实例（并发探活）。
func (m *Manager) ProbeAll() []Status {
	insts := m.allInstances()
	out := make([]Status, len(insts))
	var wg sync.WaitGroup
	for i, ins := range insts {
		wg.Add(1)
		go func(idx int, instance Instance) {
			defer wg.Done()
			out[idx] = m.probe(instance)
		}(i, ins)
	}
	wg.Wait()
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
	for _, ins := range m.allInstances() {
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
		// 探活打桥自身的本地 /health（秒回、不依赖上游），而不是 /quota。
		// /quota 要等上游计费接口，10s 超时后会把「已启动的桥」误判成离线，
		// 面板徽章于是从「在线」翻转成「启动中」——即用户反馈的怪现象（2026-09-28）。
		// 用短超时 client：半死桥（端口可连但 /health 不响应）时面板刷新不至于被拖 10s。
		body, err := m.httpGetClient(m.probeClient, ins, "/health", m.bridgeKey)
		if err != nil {
			// 健康端点没应答但端口已监听：桥正在起（监听早于路由就绪），
			// 按在线给出反馈，避免「明明起来了却显示未运行/启动中」。
			if portAccept(ins.Port) {
				st.Online = true
				st.Detail = "端口已就绪，健康接口预热中"
			} else {
				st.Detail = probeFailReason(err)
			}
			return st
		}
		var h struct {
			Status   string `json:"status"`
			Provider string `json:"provider"`
		}
		if json.Unmarshal(body, &h) == nil && h.Status == "ok" {
			st.Online = true
			if h.Provider != "" {
				st.Detail = "在线 · " + h.Provider
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
	return m.httpGetClient(m.httpClient, ins, path, key)
}

// httpGetClient 用指定 client 发起探测 GET。zcode 的 /health 是纯本地端点，
// 用短超时 client，半死桥（端口可连但不响应）时把面板刷新的最坏耗时从 10s 压到 3s。
func (m *Manager) httpGetClient(client *http.Client, ins Instance, path, key string) ([]byte, error) {
	url := fmt.Sprintf("http://127.0.0.1:%d%s", ins.Port, path)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := client.Do(req)
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
// 探测用的 netstat/schtasks 是控制台程序：面板每次刷新都会调用，
// 不加隐藏标志会在每次刷新时闪一个黑框（用户视角=服务"一直在弹 cmd"）。
//
// 必须带超时：外部工具会挂死。2026-10-12 实测本机 tasklist 全部卡住不返回，
// 面板「本地桥」列表因此永久转圈（探活同步等在这条 cmd.Output 上）。
// 超时后 Kill 子进程 + WaitDelay 兜底关闭管道，保证探活最坏也是「状态未知」而非卡死。
func hiddenOutput(name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.WaitDelay = 2 * time.Second // 子进程被杀后其孙进程仍持有管道时强制收尾
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
	insts := m.allInstances()
	out := make([]Status, 0, len(insts))
	for _, ins := range insts {
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
		for _, ins := range m.allInstances() {
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
		// daemon 重启后句柄丢失但常驻桥仍在跑：端口型实例按监听端口定位 PID 兜底，
		// 否则这类「在线但无句柄」的服务在面板上既无启动（防重复拉起）也无停止按钮。
		if ins.Port > 0 {
			if pid := pidListeningOnPort(ins.Port); pid > 0 {
				return m.killByPID(ins, pid)
			}
			m.note(ins.ID, "停止：端口无监听进程")
			return m.probe(ins), fmt.Errorf("%s 端口 %d 上没有监听的进程，可能已自行退出", ins.Name, ins.Port)
		}
		m.note(ins.ID, "停止：未找到本面板拉起的进程")
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

// killByPID 按 PID 终止进程并等待退出（句柄兜底路径共用）。
func (m *Manager) killByPID(ins Instance, pid int) (Status, error) {
	p, err := os.FindProcess(pid)
	if err == nil {
		_ = p.Kill()
		done := make(chan struct{})
		go func() { _, _ = p.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}
	m.note(ins.ID, fmt.Sprintf("已停止 (pid=%d，按端口定位)", pid))
	log.Printf("bridge: %s 已停止 (pid=%d)", ins.ID, pid)
	return m.probe(ins), nil
}

// pidListeningOnPort 查询监听指定端口的进程 PID（0 = 未找到）。
//
// 为什么不用 netstat：2026-10-12 本机 netstat 与 tasklist 一样会挂死不返回，
// 面板「停止」按钮和 storm watchdog 都会卡满 hiddenOutput 的 8s 超时且杀不掉进程。
// 这里改用 iphlpapi!GetExtendedTcpTable 原生枚举内核监听表（纯内存查询、微秒级、
// 不可能被外部工具卡住），先查 IPv4 再查 IPv6（服务以 :port 双栈监听时落在 v6 表）。
func pidListeningOnPort(port int) int {
	if pid := lookupListenerPID(port, windows.AF_INET,
		unsafe.Sizeof(mibTCPRowOwnerPID{}),
		unsafe.Offsetof(mibTCPRowOwnerPID{}.LocalPort),
		unsafe.Offsetof(mibTCPRowOwnerPID{}.OwningPID)); pid > 0 {
		return pid
	}
	return lookupListenerPID(port, windows.AF_INET6,
		unsafe.Sizeof(mibTCP6RowOwnerPID{}),
		unsafe.Offsetof(mibTCP6RowOwnerPID{}.LocalPort),
		unsafe.Offsetof(mibTCP6RowOwnerPID{}.OwningPID))
}

// TCP_TABLE_OWNER_PID_LISTENER：只取处于 LISTENING 状态的监听项，避免误杀
// 恰好连着该端口的客户端进程。
const tcpTableOwnerPidListener = 3

var procGetExtendedTcpTable = windows.NewLazySystemDLL("iphlpapi.dll").NewProc("GetExtendedTcpTable")

// mibTCPRowOwnerPID 对应 iphlpapi!MIB_TCPROW_OWNER_PID（IPv4，全 uint32 无填充）。
type mibTCPRowOwnerPID struct {
	State      uint32
	LocalAddr  uint32
	LocalPort  uint32
	RemoteAddr uint32
	RemotePort uint32
	OwningPID  uint32
}

// mibTCP6RowOwnerPID 对应 iphlpapi!MIB_TCP6ROW_OWNER_PID（IPv6）。
type mibTCP6RowOwnerPID struct {
	LocalAddr     [16]byte
	LocalScopeID  uint32
	LocalPort     uint32
	RemoteAddr    [16]byte
	RemoteScopeID uint32
	RemotePort    uint32
	State         uint32
	OwningPID     uint32
}

// lookupListenerPID 在指定地址族的监听表里查目标端口对应的属主 PID。
// rowSize / portOff / pidOff 使 IPv4/IPv6 两套行结构共用同一段解析逻辑。
func lookupListenerPID(port int, family uint32, rowSize, portOff, pidOff uintptr) int {
	size := uint32(64 << 10)
	for {
		buf := make([]byte, size)
		ret, _, _ := procGetExtendedTcpTable.Call(
			uintptr(unsafe.Pointer(&buf[0])),
			uintptr(unsafe.Pointer(&size)),
			0, // bOrder = FALSE
			uintptr(family),
			tcpTableOwnerPidListener,
			0,
		)
		if windows.Errno(ret) == windows.ERROR_INSUFFICIENT_BUFFER {
			if size == 0 {
				return 0
			}
			continue // size 已被内核写回为所需字节数，扩容重试
		}
		if ret != 0 {
			return 0
		}
		n := *(*uint32)(unsafe.Pointer(&buf[0]))
		base := uintptr(unsafe.Pointer(&buf[0])) + unsafe.Sizeof(uint32(0))
		for i := uint32(0); i < n; i++ {
			row := base + uintptr(i)*rowSize
			// dwLocalPort 为网络字节序（网络序 16 位在低 16 位），转回主机序
			lp := *(*uint32)(unsafe.Pointer(row + portOff))
			if int(lp&0xff)<<8|int(lp>>8&0xff) != port {
				continue
			}
			return int(*(*uint32)(unsafe.Pointer(row + pidOff)))
		}
		return 0
	}
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

// stormRestartThreshold 单轮检查窗口内新增 storm 次数达到该值即触发重启。
// 桥自身每轮 storm 都会先做 IP reset（约 1~2 分钟生效）；连续两轮检查（60s 间隔）
// 仍在爆 storm 说明 IP reset 没缓过来，此时重启实例是唯一有效手段
// （2026-10-11 凌晨 acc-big/fuka/fuka2 三实例同时 stall，手动重启后 ttft 143s→7s）。
const stormRestartThreshold = 2

// stormRebootDebounce 同一实例两次自动重启的最小间隔。
const stormRebootDebounce = 10 * time.Minute

// stormWatchLoop 周期扫描各 zcode 实例 serve.log 中的 captcha storm 行，
// 新增达到阈值时自动重启该实例（用户不再需要半夜手动点重启）。
// 只扫 zcode 实例（serve.log 是 zcode-proxy 专属日志），且只数「新增」，
// 日志轮转/重启导致的行数回退天然安全（count 比对取 max 防误触发）。
func (m *Manager) stormWatchLoop() {
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-m.stopCh:
			return
		case <-t.C:
			m.stormCheck()
			m.halfDeadCheck()
		}
	}
}

func (m *Manager) stormCheck() {
	for _, ins := range m.zcodeInstances() {
		logPath := filepath.Join(m.zcodeDir, "instances", ins.ID, "serve.log")
		raw, err := os.ReadFile(logPath)
		if err != nil {
			continue
		}
		count := bytes.Count(raw, []byte("[captcha] mint storm detected"))
		m.mu.Lock()
		prev := m.stormCount[ins.ID]
		m.stormCount[ins.ID] = count
		lastReboot := m.stormRebootAt[ins.ID]
		m.mu.Unlock()
		if count <= prev {
			continue // 无新增（含日志轮转回退），跳过
		}
		delta := count - prev
		if delta < stormRestartThreshold {
			log.Printf("bridge storm-watch: %s 新增 %d 次 storm（阈值 %d，继续观察）", ins.ID, delta, stormRestartThreshold)
			continue
		}
		if time.Since(lastReboot) < stormRebootDebounce {
			log.Printf("bridge storm-watch: %s 新增 %d 次 storm，但距上次自动重启仅 %s，跳过",
				ins.ID, delta, time.Since(lastReboot).Round(time.Second))
			continue
		}
		m.stormRestart(ins, fmt.Sprintf("captcha storm 持续爆发（≥%d 次/分钟窗口）", stormRestartThreshold))
	}
}

// halfDeadThreshold 连续 N 轮「端口可连但 /health 无响应」即判定半死并重启。
const halfDeadThreshold = 2

// halfDeadCheck 检测「半死桥」：TCP 端口能连上，但 /health 永不响应。
//
// 症状（2026-10-12 实测）：进程还活着、端口处于 LISTENING，/health 请求却永远
// 不返回——面板探活撞满 10s 超时，显示成「端口已就绪，健康接口预热中」（看起来
// 在线），网关却可能把请求路由过去导致整条链路挂住。这类桥重启是唯一有效手段。
// 判定需连续两轮命中（间隔 60s），避开正在冷启动预热中的桥；并复用 stormRebootAt
// 做 10 分钟防抖，与 storm 重启共享同一冷静期，防止两套 watchdog 互相打架。
func (m *Manager) halfDeadCheck() {
	for _, ins := range m.zcodeInstances() {
		if !portAccept(ins.Port) || m.zcodeHealthOK(ins) {
			// 端口未监听（本就没起）或健康接口正常：清零计数
			m.mu.Lock()
			delete(m.halfDead, ins.ID)
			m.mu.Unlock()
			continue
		}
		m.mu.Lock()
		m.halfDead[ins.ID]++
		n := m.halfDead[ins.ID]
		lastReboot := m.stormRebootAt[ins.ID]
		m.mu.Unlock()
		if n < halfDeadThreshold {
			log.Printf("bridge half-dead: %s 端口 %d 可连但 /health 无响应（第 %d 轮，阈值 %d）",
				ins.ID, ins.Port, n, halfDeadThreshold)
			continue
		}
		if time.Since(lastReboot) < stormRebootDebounce {
			log.Printf("bridge half-dead: %s 半死持续，但距上次自动重启仅 %s，跳过",
				ins.ID, time.Since(lastReboot).Round(time.Second))
			continue
		}
		m.mu.Lock()
		delete(m.halfDead, ins.ID)
		m.mu.Unlock()
		log.Printf("bridge half-dead: %s /health 连续 %d 轮无响应，判定半死，自动重启实例", ins.ID, n)
		m.stormRestart(ins, "健康接口无响应（半死）")
	}
}

// zcodeHealthOK 短超时探活 zcode 桥的 /health（半死探测专用，区别于面板 10s 探活）。
func (m *Manager) zcodeHealthOK(ins Instance) bool {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/health", ins.Port), nil)
	if err != nil {
		return false
	}
	if m.bridgeKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.bridgeKey)
	}
	resp, err := m.probeClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false
	}
	var h struct {
		Status string `json:"status"`
	}
	return json.NewDecoder(resp.Body).Decode(&h) == nil && h.Status == "ok"
}

// stormRestart 自动重启单个 zcode 实例：Stop（按端口兜底杀）→ 间隔 2s → Start。
// reason 说明触发原因（captcha storm / 半死），写入日志与面板「最近操作」。
// 全程记录失败只写日志不强求（下一轮检查会再试）。
func (m *Manager) stormRestart(ins Instance, reason string) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	m.mu.Lock()
	m.stormRebootAt[ins.ID] = time.Now()
	m.mu.Unlock()
	log.Printf("bridge watchdog: %s %s，自动重启实例", ins.ID, reason)
	m.note(ins.ID, fmt.Sprintf("自动重启：%s（watchdog，%s）", reason, time.Now().Format("15:04:05")))

	if _, err := m.Stop(ctx, ins.ID); err != nil {
		log.Printf("bridge storm-watch: %s 停止失败（可能本来就没在跑，继续尝试拉起）: %v", ins.ID, err)
	}
	time.Sleep(2 * time.Second)
	st, err := m.Start(ctx, ins.ID)
	if err != nil {
		log.Printf("bridge storm-watch: %s 重启失败: %v", ins.ID, err)
		m.noteErr(ins.ID, "自动重启失败: "+err.Error())
		return
	}
	log.Printf("bridge storm-watch: %s 重启完成 online=%v", ins.ID, st.Online)
}

// ---------------------------------------------------------------------------
// Windows 进程 / 计划任务 / 端口探测（命令行实现，无需管理员权限）
// ---------------------------------------------------------------------------

// procExists 进程名是否存在（原生 ToolHelp 快照，不走 tasklist 子进程）。
//
// 为什么不用 tasklist：2026-10-12 本机 tasklist 全部挂死不返回（杀掉又冒出新的），
// 而面板探活是同步串行调用，于是「本地桥」列表永久转圈。原生快照是内存枚举，
// 微秒级且不可能被外部工具卡住，同时省掉每次刷新一个控制台子进程。
func procExists(name string) bool {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snap)
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	if err := windows.Process32First(snap, &e); err != nil {
		return false
	}
	for {
		if strings.EqualFold(windows.UTF16ToString(e.ExeFile[:]), name) {
			return true
		}
		if err := windows.Process32Next(snap, &e); err != nil {
			return false
		}
	}
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
