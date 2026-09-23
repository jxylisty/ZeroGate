# 08 · 黑框 / 托盘消失 / 打开网页关掉网关自退 三连复盘（2026-09-23）

用户三连痛点：① 多个服务一直弹 cmd 黑框；② ZeroGate 有时不显示在托盘；
③ **打开网页再关掉，ZeroGate 自己就退掉了**（主诉）。三者根因独立，全部修复。

---

## 一、黑框：三条独立链路都在弹

| 黑框来源 | 根因 | 修复 |
|---|---|---|
| daemon 自身 | `restart.bat` 编译漏 `-H windowsgui` → Console 子系统，双击/自启弹框（CI 反而一直是对的） | `restart.bat` 补上；禁止退回 |
| 面板刷新探测 | `bridge.go` 的 `procExists`(tasklist) / `schtaskState`(schtasks) 缺 `CREATE_NO_WINDOW`，每次刷新闪框 | 统一走 `platform.HideWindow`（bridge/zcode/claim/solver 全收紧） |
| 被拉起的服务 | python/cloudflared/zcode-proxy/copilot2api/server.exe 全是 Console 程序 | 子进程创建时加 `CREATE_NO_WINDOW` |

**铁律（AGENTS.md §4.9）**：一切 `exec.Command` 拉起的控制台子进程必须
`platform.HideWindow`；daemon 构建必须 `-H windowsgui`。

附带修复：`--no-tray` 无头模式接回父控制台输出用 `platform.AttachParentConsole()`
（新建，仅无头模式调用；托盘模式调用会在关父控制台时被 CTRL_CLOSE 带走）。

## 二、托盘不显示：三个原因叠加

1. **`restart.bat` 写死 `--no-tray`**：走这入口启动永远无托盘。去掉。
2. **启动欢迎是阻塞式 MessageBox**（`platform.Notify` → Windows MessageBoxW）：
   不点"确定"，`systray.Run` 根本不走到 —— 图标一直不注册。改为写日志。
3. **Win11 折叠区**：新图标默认 `IsPromoted=0` 塞进"隐藏的图标"。新增
   `platform.PromoteTrayIcon()`：按 exe 路径写 `HKCU\Control Panel\NotifyIconSettings`
   的 `IsPromoted=1`，启动时调用（首次可能无子键，Explorer 注册后才生效）。

顺带修了两个隐藏 bug：
- `AutostartEnabled` 查注册表值名 `wild-work`，实际写入是 `ZeroGate` → 面板开关永远假。
  改为扫描 Run 键下所有值（按可执行文件名匹配），删旧值名一并清理。
- 托盘菜单图标用 PNG 喂 Windows `LoadImage(IMAGE_ICON)`（只认 ICO）→ 三个菜单图标
  从未加载成功，日志刷 `unable to load icon from temp file`。改 `systray.miniICO`
  生成 16x16 ICO（含 AND 掩码，字节级校验通过，复现实验见对话）。

## 三、主诉：打开网页关掉，网关自退（托盘循环承载了进程生命）

**症状**：日志最后一行是 `托盘已退出`，无任何报错，进程静默消失。
**机制**：`systray.Run` 的 Windows 消息循环收到外部 `WM_CLOSE`/`WM_DESTROY`
（Explorer 异常、会话事件、外部工具都可能发出）→ 循环结束 → `Run` 返回 →
`main()` 里后面什么都没有 → **main 走完 = 进程正常退出**。崩溃现场无痕
（GUI 子系统 stderr 不可见）。

**修复（架构级）**：
1. **托盘只做显示，绝不承载进程生命**：main 里套监督循环 —— `systray.Run`
   返回 = 意外终止 → 记日志 → 3 秒后重建托盘（实测：外部 WM_CLOSE 后同一 PID
   存活、托盘窗口重建、HTTP 照常服务）。唯一退出 = 托盘菜单「退出」确认后
   os.Exit / fatal()。
2. **崩溃现场兜底**：`windows.SetStdHandle(STD_ERROR_HANDLE, file)` + `os.Stderr`
   重定向到 `data/crash-stderr.log`（实测能抓到 goroutine panic 完整堆栈）；
   所有调度器 goroutine 套 recover。
3. **删除 `/api/quit` 孤儿路由**：旧版 Web UI 遗留，前端从不调用；任何本地进程
   POST 一下就无声杀网关，是"自退"隐患之一。（需远程退出须显式恢复 + 鉴权）

## 四、附带：服务集中管理（开机编排收编）

豆包桥/chatui/Cloudflare 隧道/CLIProxyAPI/copilot2api 原在注册表 Run 键各挂一个
.bat（各弹一个最小化窗口）。已收编进 `internal/bridge`（`Instance.Autostart=true`），
daemon 启动时 `App.StartAutostartBridges` 隐藏窗口统一拉起，已在线跳过。
注册表 5 个冗余自启项已删，**备份 `data/autostart-backup/HKCU-Run-backup.reg`**
（reg export 可回滚）。根目录 `启动全部服务.bat` 简化为只拉 daemon。

**经验**：Windows GUI 常驻程序的进程生命必须与任何"可被外部结束"的 UI 循环解耦；
崩溃要留现场，否则"莫名退出"只能靠猜；CI 对而本地脚本错的情况要全面排查。

## 验证记录
- `go build -ldflags "-H windowsgui"` → PE Subsystem=2（GUI，无黑框）✓
- 模拟 WM_CLOSE 杀托盘循环 → 日志"意外结束（第 1 次）"→ 3s 后托盘重建、
  同一 PID 存活、`/api/bridges` 不间断 ✓
- `/api/quit` → 404 ✓；`go test ./...` 全绿 ✓
- 全服务从零拉起：daemon 拉 3 个、跳过 2 个已在线，全部无窗口 ✓