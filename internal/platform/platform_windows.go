//go:build windows

package platform

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

var (
	user32 = windows.NewLazySystemDLL("user32.dll")
	procSystemParametersInfo = user32.NewProc("SystemParametersInfoW")
	kernel32                 = windows.NewLazySystemDLL("kernel32.dll")
	procAttachConsole        = kernel32.NewProc("AttachConsole")
)

const spiGetWorkArea = 0x0030

// AttachParentConsole 把标准流接回父进程控制台（仅 --no-tray 调试模式调用）。
//
// daemon 以 -H=windowsgui 构建（GUI 子系统），双击/自启时不再弹控制台黑框；
// 代价是终端里跑 --no-tray 也看不到 fmt.Printf 输出，故无头模式显式附加回来。
//
// ⚠️ 托盘模式**绝不能**调用：附加控制台后，用户关掉那个 cmd 窗口会向本进程投递
// CTRL_CLOSE_EVENT，Go 默认处理是直接退出 —— 正好制造"误触关闭导致服务全停"。
// 托盘模式日志一律走 data/app.log（app.New 已接管 log 输出）。
func AttachParentConsole() {
	const attachParentProcess = ^uintptr(0) // (DWORD)-1
	if r, _, _ := procAttachConsole.Call(attachParentProcess); r == 0 {
		return // 无父控制台（双击/计划任务），保持无窗口
	}
	// 必须重开 CONOUT$ 并替换标准流：AttachConsole 只给控制台句柄，
	// 进程原有的 stdout/stderr 句柄仍是无效的，不替换则 printf 静默丢失。
	if f, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
		os.Stdout, os.Stderr = f, f
	}
	if f, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
		os.Stdin = f
	}
}

// WorkArea 返回主显示器工作区（不含任务栏）。
func WorkArea() (int32, int32, int32, int32) {
	var rect struct{ Left, Top, Right, Bottom int32 }
	procSystemParametersInfo.Call(uintptr(spiGetWorkArea), 0, uintptr(unsafe.Pointer(&rect)), 0)
	return rect.Left, rect.Top, rect.Right - rect.Left, rect.Bottom - rect.Top
}

// InfoBox 弹信息提示框。
func InfoBox(title, msg string) {
	msgBox(title, msg, 0x40 /* MB_ICONINFORMATION */)
}

// Notify 弹信息提示框。
func Notify(title, msg string) { InfoBox(title, msg) }

// AskYesNo 弹"是/否"框，true=是。
func AskYesNo(title, msg string) bool {
	return msgBox(title, msg, 0x24 /* MB_YESNO|MB_ICONQUESTION */) == 6 /* IDYES */
}

func msgBox(title, msg string, flags uintptr) uintptr {
	proc := user32.NewProc("MessageBoxW")
	caption, _ := syscall.UTF16PtrFromString(title)
	text, _ := syscall.UTF16PtrFromString(msg)
	// MB_TOPMOST(0x40000) + MB_SETFOREGROUND(0x10000) + MB_DEFAULT_DESKTOP_ONLY(0x20000)
	// 强制主显示器前台显示，避免双显示器下弹窗位置飘忽
	res, _, _ := proc.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(caption)), flags|0x40000|0x10000|0x20000)
	return res
}

// ---------------------------------------------------------------------------
// 浏览器
// ---------------------------------------------------------------------------

var browserSpecs = []struct {
	progID string
	exe    string
	flag   string
}{
	{"ChromeHTML", "chrome.exe", "--incognito"},
	{"MSEdgeHTM", "msedge.exe", "--inprivate"},
	{"EdgeChromiumHTM", "msedge.exe", "--inprivate"},
	{"FirefoxURL", "firefox.exe", "-private-window"},
}

// DefaultBrowserIncognito 探测默认浏览器的无痕启动方式。
func DefaultBrowserIncognito() (exePath, flag string, ok bool) {
	pid := defaultProgID()
	for _, b := range browserSpecs {
		if pid == "" || strings.EqualFold(pid, b.progID) {
			if p := findBrowserExe(b.exe); p != "" {
				return p, b.flag, true
			}
		}
	}
	for _, b := range browserSpecs {
		if p := findBrowserExe(b.exe); p != "" {
			return p, b.flag, true
		}
	}
	return "", "", false
}

func defaultProgID() string {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\Shell\Associations\UrlAssociations\http\UserChoice`,
		registry.QUERY_VALUE)
	if err != nil {
		return ""
	}
	defer k.Close()
	v, _, err := k.GetStringValue("ProgId")
	if err != nil {
		return ""
	}
	return v
}

func findBrowserExe(exe string) string {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE,
		`SOFTWARE\Microsoft\Windows\CurrentVersion\App Paths\`+exe,
		registry.QUERY_VALUE)
	if err == nil {
		v, _, err2 := k.GetStringValue("")
		k.Close()
		if err2 == nil && v != "" && fileExists(v) {
			return v
		}
	}
	rel := map[string][]string{
		"chrome.exe":  {"Google", "Chrome", "Application", "chrome.exe"},
		"msedge.exe":  {"Microsoft", "Edge", "Application", "msedge.exe"},
		"firefox.exe": {"Mozilla", "Firefox", "firefox.exe"},
	}[exe]
	for _, base := range []string{
		os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("LocalAppData"),
	} {
		if base == "" || rel == nil {
			continue
		}
		cand := filepath.Join(append([]string{base}, rel...)...)
		if fileExists(cand) {
			return cand
		}
	}
	return ""
}

// LaunchIncognito 以无痕模式启动浏览器打开 url。
func LaunchIncognito(exe, flag, url string) error {
	cmd := exec.Command(exe, flag, url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

// HideWindow 让 cmd 启动的子进程不弹控制台窗口。
//
// daemon 自身以 GUI 子系统构建（无控制台），但被拉起的服务与探测命令
// （python.exe / cloudflared.exe / zcode-proxy.exe / tasklist / schtasks 等）
// 多为控制台程序：不设此标志，每个都会弹出黑框，用户误关还会连带杀掉服务。
func HideWindow(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
}

// OpenURL 用系统默认方式打开 URL。
func OpenURL(url string) error {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd.Start()
}

// ---------------------------------------------------------------------------
// 开机自启（注册表 Run 键）
// ---------------------------------------------------------------------------

const autostartKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// autostartValue 当前写入的注册表值名。历史版本曾用 "wild-work"，
// 而 2026-09-18 统一入口时实际写入的是 "ZeroGate"；按旧名查询会让面板
// 永远显示"未开启"，故读取侧改为扫描全部值（见 AutostartEnabled）。
const autostartValue = "ZeroGate"

// legacyAutostartValue 历史值名，写入/删除时一并清理，避免残留两条自启项。
const legacyAutostartValue = "wild-work"

// runValueExe 从 Run 键值里提取可执行文件路径。
// 值形如 `"C:\path\a.exe" --args` 或 `D:\path\a.exe --args`。
func runValueExe(v string) string {
	p := strings.TrimSpace(v)
	if p == "" {
		return ""
	}
	if p[0] == '"' {
		if i := strings.IndexByte(p[1:], '"'); i >= 0 {
			return p[1 : 1+i]
		}
		return p[1:]
	}
	if i := strings.IndexAny(p, " \t"); i >= 0 {
		return p[:i]
	}
	return p
}

// deleteAutostartValues 删除 Run 键下所有指向当前 exe 的值（含历史值名）。
func deleteAutostartValues(k registry.Key) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	want := strings.ToLower(filepath.Base(exe))
	names, err := k.ReadValueNames(-1)
	if err != nil {
		// 无值可读（键为空）时至少清掉已知的两个值名
		for _, n := range []string{autostartValue, legacyAutostartValue} {
			if err := k.DeleteValue(n); err != nil && err != registry.ErrNotExist {
				return err
			}
		}
		return nil
	}
	for _, n := range names {
		v, _, err := k.GetStringValue(n)
		if err != nil {
			continue
		}
		if strings.ToLower(filepath.Base(runValueExe(v))) == want {
			if err := k.DeleteValue(n); err != nil && err != registry.ErrNotExist {
				return err
			}
		}
	}
	return nil
}

// SetAutostart 设置/取消当前 exe 的开机自启。
func SetAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.SET_VALUE|registry.QUERY_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		return deleteAutostartValues(k)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// 先清历史值名，避免同一程序在 Run 键里留两条
	_ = k.DeleteValue(legacyAutostartValue)
	return k.SetStringValue(autostartValue, `"`+exe+`" --autostart`)
}

// AutostartEnabled 查询当前 exe 是否已设置开机自启。
// 扫描 Run 键下所有值（按 exe 文件名匹配），而非只查固定值名：
// 改名（wild-work → ZeroGate）或迁移目录后仍能识别既有自启项，避免面板与实际不符。
// 按文件名而非完整路径匹配：升级/迁移目录后注册表指向旧路径时仍视为已开启。
func AutostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, autostartKey, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	want := strings.ToLower(filepath.Base(exe))
	names, err := k.ReadValueNames(-1)
	if err != nil {
		return false
	}
	for _, n := range names {
		v, _, err := k.GetStringValue(n)
		if err != nil {
			continue
		}
		if strings.ToLower(filepath.Base(runValueExe(v))) == want {
			return true
		}
	}
	return false
}

// PromoteTrayIcon 把托盘图标从 Windows 11 的「隐藏的图标」折叠区提升到任务栏常驻区。
//
// Windows 11 对新注册的托盘图标默认 IsPromoted=0（塞进折叠区），用户看不到图标
// 就会以为程序没启动 —— 这是"托盘有时候不显示"的直接原因之一。
// 此处按可执行文件路径定位 HKCU\Control Panel\NotifyIconSettings 下的对应子键并置 1。
//
// 该子键由 explorer 在图标首次注册后才创建：首次运行可能尚未存在，跳过即可，
// 下次启动就能命中。纯 HKCU 写入，无需管理员；任何失败都静默忽略（仅影响观感）。
func PromoteTrayIcon() {
	exe, err := os.Executable()
	if err != nil {
		return
	}
	exe = filepath.Clean(exe)

	root, err := registry.OpenKey(registry.CURRENT_USER,
		`Control Panel\NotifyIconSettings`, registry.QUERY_VALUE|registry.ENUMERATE_SUB_KEYS)
	if err != nil {
		return
	}
	defer root.Close()
	names, err := root.ReadSubKeyNames(-1)
	if err != nil {
		return
	}
	for _, name := range names {
		sub, err := registry.OpenKey(root, name, registry.QUERY_VALUE|registry.SET_VALUE)
		if err != nil {
			continue
		}
		path, _, err := sub.GetStringValue("ExecutablePath")
		if err == nil && strings.EqualFold(filepath.Clean(path), exe) {
			if v, _, err := sub.GetIntegerValue("IsPromoted"); err != nil || v != 1 {
				_ = sub.SetDWordValue("IsPromoted", 1)
			}
		}
		sub.Close()
	}
}

// OpenWithTextEditor 用记事本打开文件。
func OpenWithTextEditor(path string) error {
	cmd := exec.Command("notepad.exe", path)
	return cmd.Start()
}

func fileExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

