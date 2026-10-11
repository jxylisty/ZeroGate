# ZeroGate 项目指南（AGENTS.md）

> 本文件面向接手本仓库的 AI Agent 与开发者。**动手前先读 0A/0B/0C 三条红线**：
> 目录与入口真相（0A）、提交与还原点约定（0B）、**不得随意重启/关闭 daemon（0C）**。

---

## ⚠️ 0A. 目录与入口真相（2026-09-18 统一入口）

### 唯一事实源：代码与数据都在 `src-true\` 目录

| 层面 | 位置 | 说明 |
|---|---|---|
| 源码 + git 仓库 | `src-true\`（本文件所在） | remote `zero` = `github.com/jxylisty/ZeroGate`；master 跟踪 zero |
| 常驻程序 | `src-true\daemon.exe` | 监听 `127.0.0.1:7863`，OpenAI 兼容端点 + 管理 API + Web UI |
| 构建与启动 | `src-true\restart.bat` | 杀 7863 → `go build -ldflags "-H windowsgui" -o daemon.exe ./cmd/zerogate` → 启动（唯一入口链；**必须带 `-H windowsgui`**，否则双击/自启弹黑框）。⚠️ 本机 `netstat`/`taskkill` 挂死致脚本卡死，重启方式见 **0C 第 5 条**（PowerShell 原生） |
| 运行时数据 | `src-true\auths\`、`src-true\data\`、`src-true\config.json` | 单份；main.go 开头固定工作目录，从任何路径启动都指向这里 |
| 工作区根目录 | `d:\wild-work\` | 仅有 `启动.bat`（调用 `src-true\restart.bat`）+ 独立资产；**不是**代码或数据存放地（根目录无 git，多仓库容器，见根 `AGENTS.md`/`OWNERS.md`） |

### 禁止事项（防重蹈覆辙）

1. **严禁在根目录编译或运行任何 exe**，不得新建 auths / data / config.json（daemon 只认 `src-true\`）。
2. **严禁删除或引用 `_archive\`**：归档的旧根目录运行时仅作历史备份。
3. 根目录与 daemon 无关的独立资产勿动：`.env`、`captcha_node\`、`research\`、`tools\` 等。
4. `auths/` 含真实凭证（已 gitignore），**绝不入库、绝不打日志、绝不回显到前端**。
5. API 端口 7863、API Key 见 `config.json`；修改端口须同步 `restart.bat` 中的 `_PORT`。
6. **不得随意重启/关闭 daemon** —— 会掐断在途反代会话（可能把 agent 自己杀掉），详见 **0C**。

---

## ⚠️ 0B. 提交与还原点约定（多会话协作，2026-09-29 新增）

> 背景：本项目由**多个 AI 会话并行**修改。未提交的改动等于没有还原点——
> 出了问题时无法回滚，也分不清哪次会话改了什么。因此本仓库**默认提交**。

1. **默认直接提交，不必等用户开口**：完成一项修复/改动、且自测通过
   （Go 改动跑 `go build ./...` + 相关单测）后，agent 应**立即 commit**，
   不要攒着或只报告「未提交」。**需要重启 daemon 才能验证的改动，不要自己重启**
   —— 先按 0C 征得用户同意（重启会掐断在途反代会话，可能把 agent 自己杀掉）。
2. **一次改动一个 commit**：不要把互不相关的修复塞进同一个 commit；
   文档/配置随对应代码一起提交。
3. **提交信息**：conventional commit 前缀 + 中文，例如
   `fix(bridge): zcode 桥探活改打本地 /health，修复在线徽章误判离线`；
   正文写清「为什么改」和实证依据（复现现象、验证结果）。
4. **提交前必查**（红线）：`git status --short` + `git diff --stat`，确认
   **没有**凭证（`auths/`、`instances/`、`config.json*`）、**没有**大体积二进制
   （`*.exe` 备份等）混入。这些应由 `.gitignore` 覆盖；一旦发现未忽略，
   **先修 `.gitignore` 再提交**，不要把文件加进去。
5. **只提交，不 push**：推送到 remote `zero`（`github.com/jxylisty/ZeroGate`）
   仅在用户明确要求时执行。
6. **提交后回报**：给出 commit hash + 一句话改动摘要，并说明**是否要重启才生效**
   （默认不擅自重启 —— 见 0C）。

---

## ⚠️ 0C. 不得随意重启 / 关闭 daemon（2026-09-29 用户明确要求）

> **本网关自己的模型流量也走它自己反代。** 重启或关闭 daemon 会掐断所有在途
> 反代会话 —— 包括**当前这个 AI 会话正在用的模型连接**。关掉就可能把自己杀死，
> 会话直接断、排查上下文全丢。这是最贵的一种失误：它不可回滚。

1. **默认不重启**：改完先 `go build ./...` + 相关单测自测。**只有必须重启才能验证**
   的改动，才在动手前问用户「现在可以重启 daemon 吗」，得到同意再重启。
2. **禁止拿杀进程「试试看」**：`taskkill daemon.exe` / `Stop-Process` / 反复跑
   `restart.bat` 都不得作为试探手段。`/api/quit` 已删除（详见 4.11），**不要恢复**。
3. **重启是有损操作**：会清空粘性路由与渠道熔断状态、打断面板正在看的请求、
   并让 `internal/bridge` 拉起的桥实例短暂不可用（zcode 4 实例会掉线重连）。
4. **哪些改动要重启才生效**：`internal/**`、`cmd/**`、以及 `cmd/zerogate/web/**`
   （Web UI 经 `go:embed` 打进 exe，改 `app.js`/`style.css` 同样必须重启）。
   只有 `docs/**`、`AGENTS.md`、`research/**` 不需要。
5. **确需重启时**：默认走 `restart.bat`（唯一入口链，必须带 `-H windowsgui`）。
   **本机例外（2026-10-12）**：本机 `netstat` / `tasklist` / `taskkill` 三个控制台工具
   **全部会挂死不返回**（`tasklist` 杀掉又冒新进程），而 `restart.bat` → `restart-gate.bat`
   正是靠 `netstat | findstr` + `taskkill` 找/杀进程，所以在这台机器上**点重启必卡死**。
   本机请改用 PowerShell 原生方式（等价于脚本三步，已验证可用）：

   ```powershell
   # cwd: D:\wild-work\src-true
   Get-Process daemon -ErrorAction SilentlyContinue | Stop-Process -Force   # ① 杀旧进程
   Start-Sleep 2
   $env:ZCODE_BRIDGE_URL='http://127.0.0.1:18083'                          # ② 桥变量（与 restart-gate.bat 一致）
   $env:ZCODE_BRIDGE_KEY='sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4'
   go build -ldflags "-H windowsgui" -o daemon.exe ./cmd/zerogate           # ③ 构建（必须 -H windowsgui）
   Start-Process -FilePath "D:\wild-work\src-true\daemon.exe" -WorkingDirectory "D:\wild-work\src-true"
   ```

   重启后立刻核对 `GET /healthz` 与 `GET /api/bridges`（zcode 实例应显示在线）。
   注意：本机 `Get-NetTCPConnection` 也很慢/会挂，不要用它找监听 PID。

---

## 1. 项目定位与架构

ZeroGate 是多渠道 AI 账号聚合网关：将 WorkBuddy / TraeWork / Qoder / CodeBuddy / WBIntl / ZCode / AutoClaw / Atria 多个渠道的账号聚合成一个统一的 OpenAI 兼容 API。

- **单进程 Daemon**：一个 `http.Server` 同时支撑 OpenAI 兼容接口、管理 API 与静态 Web UI。
- **纯静态前端**：`go:embed` 打包 HTML/CSS/JS，无独立 Node.js 前端构建链。

---

## 2. 渠道扩展与路由规范

系统通过 `provider.Kind` 统一路由，客户端通过 `model: "<channel>/<model>"`（如 `zcode/glm-5.3-flash`）指定渠道：

1. `internal/<channel>/`：实现 `provider.Upstream` 接口（`ChatStream`、`RefreshToken`、`Classify` 等）。
2. `internal/auth`：增加对应的 `Load<Channel>Dir()`，文件命名约定为 `<channel>-*.json`。
3. 启动装配：在 `cmd/zerogate/main.go` 注册 `server.Runtime` 与 `app.Runtime`。
4. 账号接入模式：
   - **云端直连（proxy/direct）**：WorkBuddy、TraeWork、Qoder、CodeBuddy、WBIntl、AutoClaw、Atria 均直接发起 HTTPS 请求上云。
   - **本地桥接（bridge）**：ZCode 因阿里滑块验证码与严格指纹风控，由本地独立多开的 `zcode-proxy` 实例（18083~18086）负责代理桥接。

---

## 3. 核心 API 端点

- `POST /v1/chat/completions`、`GET /v1/models`：标准 OpenAI 兼容接口（`Authorization: Bearer <api_key>`）。
- `GET /status`：全量账号状态矩阵（渠道、UID、额度、冷却状态、签到记录）。
- `GET /api/token_usage`、`GET /healthz`：Token 用量审计与健康检查。
  - `/api/token_usage` **匿名可读** —— 别再给它挂 withAuth（2026-09-19 曾 401 导致用量大屏恒为 0）。
  - 落盘 `data/usage-tokens.csv`，列 `day,kind,uid,prompt,completion,cache_read,cache_write[,model]`。
  - **CSV 加载器必须逐行容错**（`FieldsPerRecord = -1` + 跳坏行）：Go 的 `csv.ReadAll()` 默认要求全文件列数一致，加列后新旧混存会让整表加载失败、历史看起来「全丢」（2026-09-19 踩过）。以后再给这两个 CSV 加列无需改加载器。
  - 面板「白嫖节省」按 `app.js` 的 `MODEL_PRICES`（官方单价，2026-09 检索）× 用量折算；接新模型记得补价格。

---

## 4. 关键系统守则（改动必读）

1. **Windows UTF-8 BOM 免疫**：
   所有凭证加载统一走 `auth.Parse`，内部自动执行 `bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))`，防止 Windows / PowerShell 文件头破坏 JSON 解析。
2. **账号池占位符隔离**：
   `data/state-*.json` 恢复的历史记录初始带有 `placeholder: true`。必须经由 `Add` 或 `SyncToDir` 匹配到真实 `auths/*.json` 凭证后方可投入调度，**严禁调度无凭证空壳**。
3. **服务启动使用 SyncToDir**：
   `cmd/zerogate/main.go` 启动时各渠道必须通过 `pool.SyncToDir(auths)` 初始化，自动清除已删除的旧账号。
4. **思考模型 max_tokens 策略**：
   在 `upstream.PrepareBody` 中强制移除 `max_tokens`（GLM 等模型将 max_tokens 视作思考+正文总预算且优先消耗给思考，客户端设置过小会导致正文被吞）。
5. **错误状态码透传规范**：
   上游 HTTP ≥400 错误：标准 OpenAI error 格式原样透传；非标准错误体统一包装为标准格式，防止下游客户端崩溃。
6. **粘性路由（Sticky Routing）**：
   `pickWithSticky` 优先复用上一成功账号（单号上限 50 次或遇错清除），成功请求由 `stickySuccess` 递增。
7. **GUI 子系统构建（勿退回控制台）**：
   daemon 必须以 `-H windowsgui` 构建（CI 已带，`restart.bat` 已带）。控制台子系统会让双击/开机自启弹黑框，误关连带杀服务。
   `--no-tray` 无头模式由 `platform.AttachParentConsole()` 接回父控制台；**托盘模式绝不调用**（关父控制台会投递 CTRL_CLOSE_EVENT 退出进程）。
8. **本地桥开机编排（2026-09-23 收编）**：
   豆包桥 / chatui / Cloudflare 隧道 / CLIProxyAPI / copilot2api 已从注册表 Run 里的 .bat 收编进 `internal/bridge`（`Instance.Autostart=true`），daemon 启动时 `App.StartAutostartBridges` 隐藏窗口统一拉起。旧自启项已删除，备份在 `data/autostart-backup/HKCU-Run-backup.reg`（可回滚）。**以后新增常驻服务优先挂到 bridge 编排，别再加注册表 .bat。**
9. **子进程必须隐藏窗口**：
   一切 `exec.Command` 拉起的控制台子进程（服务 / tasklist / schtasks / node solver / zcode-proxy CLI）必须走 `platform.HideWindow`（`CREATE_NO_WINDOW`），否则面板刷新探测会闪黑框。
10. **失败要快速返回 + 渠道级熔断（2026-09-23 AutoClaw 封号事故铁律，详见 lessons/09）**：
    - **不可恢复错误（410004 封禁 / 401 凭证失效）必须立即中断**，绝不长时间重试 —— 上游把"持续重试"判定为恶意请求（`9cf661f` 把重试上限改成 270s 时间窗，即事故放大器）。
    - **渠道级熔断优先于账号级轮换**：整池健康数为 0 时 `Handler` 自动置位 60s 熔断，期间不转发上游（`breakerOpen/Trip/Reset` + `Pool.HealthyCount`）。
    - 失败返回用 **429/503 + 明确文案**，不要用会被客户端误读为"稍后能好"的 502（客户端会自动重试形成风暴）。
    - **审计必须覆盖失败路径并带 `Channel`**，否则事后查不出是谁在打哪个渠道。
11. **托盘与进程生命解耦（2026-09-23，详见 lessons/08）**：
    - **`systray.Run` 返回 ≠ 退出进程**：main 是监督循环，托盘消息循环意外结束（外部 WM_CLOSE/WM_QUIT、Explorer 异常等）→ 记日志 → 3 秒后重建托盘，网关服务不受影响。唯一退出路径 = 托盘菜单「退出」（AskYesNo 后 os.Exit）或 `fatal()`。**别再让 main() 在 systray.Run 后直接结束。**
    - 菜单图标必须用 ICO（`systray.miniICO`）——Windows `LoadImage(IMAGE_ICON)` 不认 PNG，喂 PNG 静默失败；`platform.PromoteTrayIcon()` 把图标从 Win11 折叠区提到常驻区。
    - 启动提示用写日志，**不要用 `platform.Notify`**（Windows 上是阻塞的 MessageBoxW，不点确认托盘就一直不出来）。
    - **崩溃现场兜底**：main 开头 `windows.SetStdHandle(STD_ERROR_HANDLE, ...)` + `os.Stderr` 重定向到 `data/crash-stderr.log`（实测能抓到 goroutine panic 堆栈）；调度器 goroutine 全部套 recover。
    - **`/api/quit` 已删除**（2026-09-23）：孤儿路由，任何本地进程 POST 一下就无声杀网关。需要远程退出时须显式恢复并加鉴权。
12. **脚本文件约定：`.bat` 必须 CRLF 行尾 + GBK(936) 编码（2026-09-27 踩坑）**：
    本机 ACP/OEMCP = 936，cmd.exe 按「当前控制台代码页」逐行解析批处理。若 `.bat` 被存成 **UTF-8 或 LF 行尾**，cmd 会把中文/行读错位，产生看似随机的 `'xxx' 不是内部或外部命令` / `此时不应有 &` —— 典型现象是"昨天还好、今天莫名报错"，极耗排查时间。
    - 涉及文件：`src-true/restart.bat`、根目录 `启动.bat`、`启动全部服务.bat`。
    - **改动这些脚本后必须回读校验**（CRLF + 非 UTF-8）；`go test ./cmd/zerogate/` 的 `TestBatConventions` 会直接拦截违规文件。
    - 编辑器 / `Set-Content` / PowerShell 默认保存为 UTF-8+LF，**勿直接保存**。`.gitattributes` 只兜底行尾、`.editorconfig` 无法指定 GBK，**编码只能靠约定 + 断言**。
    - Agent 在 PowerShell 里调用这些 `.bat` 时需先 `chcp 936`：否则子进程 cmd 会沿用被宿主设为 65001 的控制台代码页，导致 GBK 脚本再次被读错。

---

## 5. 现役账号与实例映射速查

| 显示名称 | 实例目录 · UID | 接入方式 / 端口（上游域） | 备注 / 主要额度 |
|---|---|---|---|
| **主号bigmodel** | `acc-big` · `zcode-big` | 桥接 `http://127.0.0.1:18083`（bigmodel） | 智谱 300M Weekend Build |
| **副卡gmail** | `acc-fuka` · `zcode-fuka` | 桥接 `http://127.0.0.1:18084`（zai） | 智谱 300M Weekend Build + 每日 |
| **副卡bigmodel** | `acc-cn` · `zcode-cn` | 桥接 `http://127.0.0.1:18085`（bigmodel） | 智谱 每日额度 |
| **主号gmail** | `acc-fuka2` · `zcode-fuka2` | 桥接 `http://127.0.0.1:18086`（zai） | 智谱 备用/自用实例 |
| **其他平台** | 各自 UID | 直连官方云端 API | TraeWork / Qoder / WorkBuddy / CodeBuddy / Atria |

> ⚠️ 注：历史 `18080` 端口已废弃关停，默认桥接兜底端口为 `18083`。
> 实例目录 = `research/zcode-proxy/instances/<名字>`（`creds/` 存加密凭证 + `config.yaml` 存该实例的 `deviceMid`/`appVersion`/`provider`）。

### zcode 桥运维要点（2026-09-29 实证）

- **探活打本地 `/health`，不要打 `/quota`**：`/quota` 要等上游计费接口（易 10s 超时），会把已启动的桥误判成离线，面板徽章于是从「在线」翻转成「启动中」。`internal/bridge` 已改用 `/health`。
- **手动领取必须把 `ZCODE_PROXY_CONFIG` 指向实例自己的 `config.yaml`**：否则所有实例共用根 config 的同一个 `X-Device-Mid`，活动网关按设备去重 → 第一个号领到后其余实例全被静默判成 `no_claim`（漏领）。`internal/zcode/weekly_claim.go` 已修。

  ```
  $env:ZCODE_PROXY_CONFIG="instances/<名字>/config.yaml"
  $env:ZCODE_CREDENTIALS_DIR="instances/<名字>/creds"
  zcode-proxy.exe claim list      # 仅预览；claim now 才实际领取
  ```

- 桥实例本身**不要随手动重启**：4 个实例由 `internal/bridge` 统一托管（`Autostart`），重启实例请走面板「本地桥」Tab 的启动/停止。

---

## 6. 经验教训与故障复盘（Docs / Lessons）

深入的逆向过程、协议分析与历史排查复盘统一归档在 **`docs/lessons/`**（下方链接为相对本仓库的路径）：

- [`01-zcode-waf-and-claim.md`](docs/lessons/01-zcode-waf-and-claim.md)：ZCode 阿里 WAF 3012 风控机制、Start-Plan 区分与 Weekend Claim 领取破解。
- [`02-wbintl-oauth.md`](docs/lessons/02-wbintl-oauth.md)：WorkBuddy 国际版（WBIntl）网页授权与 OAuth 协议细节。
- [`03-bom-and-bridge-port-postmortem.md`](docs/lessons/03-bom-and-bridge-port-postmortem.md)：2026-09-19 Windows BOM 破坏凭证解析 + 空壳占位符调度 + 18080 废弃端口级联冷却。
- [`04-zcode-bridge-stall-and-routing-hardening.md`](docs/lessons/04-zcode-bridge-stall-and-routing-hardening.md)：2026-09-19 ZCode 桥接签发卡死 → 120 秒「假成功」（200/0 token）；零 token 长流熔断、到期升序轮转等加固（见 4.10）。
- [`05-qoder-nested-sse-usage.md`](docs/lessons/05-qoder-nested-sse-usage.md)：2026-09-19 Qoder 流式 token 计 0 根因（嵌套 SSE 转义 usage 漏抓）与修复。
- [`06-autoclaw-contract-inversion-and-vip-wall.md`](docs/lessons/06-autoclaw-contract-inversion-and-vip-wall.md)：2026-09-21 AutoClaw 上游契约反转（`X-Harness-Type` 变 400、系统 banner 变必需）导致 daemon 全请求超时 + 高峰免费通道 810000 VIP 墙。
- [`07-autoclaw-promotion-dual-auth-and-login-grant.md`](docs/lessons/07-autoclaw-promotion-dual-auth-and-login-grant.md)：2026-09-22 AutoClaw 登录礼领取契约（promotion-reward 需 `authorization` + `X-Authorization` 双头；3 天滚动过期）。
- [`08-tray-exit-black-box-postmortem.md`](docs/lessons/08-tray-exit-black-box-postmortem.md)：2026-09-23 黑框三条链路 + 托盘不显示三因 + 「关掉网页网关自退」根因（托盘消息循环结束 → main 走完 → 静默退出）。
- [`09-autoclaw-ban-retry-storm.md`](docs/lessons/09-autoclaw-ban-retry-storm.md)：2026-09-23 AutoClaw 封号事故 —— 不可恢复错误长时间重试被上游判定为恶意请求（铁律见 4.10）。
- [`ZCode-完整指南.md`](docs/ZCode-完整指南.md)：ZCode 渠道交接总纲（账号映射、billing/preview/claim 身份头清单、领取时间窗、坑清单、实例速查命令）。
