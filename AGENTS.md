# Wild-Work 项目提示词（AGENTS.md）

> 本文件面向 AI Agent 与开发者。**2026-09-18 起，顶部为「目录与入口真相」——接手的 AI 必须先读，否则会被历史遗留文件误导。**

---

## ⚠️ 0A. 目录与入口真相（2026-09-18 统一入口后）

### 唯一事实源：本项目 = `src-true\` 目录

| 层面 | 位置 | 说明 |
|------|------|------|
| 源码 + git 仓库 | `src-true\`（本文件所在） | remote `zero` = github.com/jxylisty/ZeroGate；master 跟踪 zero |
| 常驻程序 | `src-true\daemon.exe` | 监听 `127.0.0.1:7863`，OpenAI 兼容端点 + 管理 API + Web UI |
| 构建+启动 | `src-true\restart.bat` | 杀 7863 → `go build -o daemon.exe ./cmd/wild-work` → 启动（唯一入口链） |
| 运行时数据 | `src-true\auths\`、`src-true\data\`、`src-true\config.json` | 单份；main.go 开头 `os.Chdir(exe 所在目录)` 保证从任何 cwd 启动都落到这里 |
| 根目录 (`D:\wild-work`) | 只有 `启动.bat`（= 跳转 src-true 调 restart.bat）+ 独立资产 | **不是**代码/数据所在地 |

### 禁止事项（防重蹈"双目录割裂"覆辙）

1. **不要在 `D:\wild-work`（仓库外）编译或运行 exe**；不要引用根目录任何 exe（旧的已归档 `_archive\exes\`，不要复活）
2. **不要在根目录新建 auths / data / config.json**——daemon 只认 exe 旁的 `src-true\`
3. **不要删除/绕过 `_archive\`**：2026-09-18 归档的旧根目录运行时（exes、data、auths、config、doubao 算命实验产物），只作备份
4. 根目录 `.env`、`zcode_creds_*.json`、`captcha_node\`、`homework\`、`research\`、`tools\`、`gate_icon.png`、`使用说明.txt` 是独立资产，与 daemon 无关，别动
5. **git push 默认进 `zero`（jxylisty/ZeroGate）**；要推上游 rockswang 需显式 `git push origin`
6. auths/ 含真实凭据（已 gitignore），**绝不入库、绝不打日志、绝不输出到对话/界面**
7. api 端口 7863、API Key 见 `config.json` 的 `api_key`（请求带 `Authorization: Bearer <key>`）；改端口必须同步 restart.bat 的 `_PORT`

### 接手时的标准动作

```bash
# 状态确认
netstat -ano | findstr 7863          # 应在监听（daemon.exe）
curl -H "Authorization: Bearer <api_key>" http://127.0.0.1:7863/status   # 看账号分组与积分
# 改代码后：编译 + 重启（先杀 7863 占用，再 go build -o daemon.exe ./cmd/wild-work，最后启动 --no-tray）
# 验证通过后：git add -A && git commit && git push（进 ZeroGate）
```

### 本节对旧决议的修正

- 原 R4「不设管理 API 鉴权」**已过期**：管理 API 已加 `api_key` 鉴权（`internal/server/handler.go` withAuth）。
- 原「端口 7863」文字表述仍有，但鉴权、路由以代码为准；文档章节可能滞后，**代码优先**。

---

## 0. 项目渊源

Wild-Work 是 WorkBuddy/TraeWork/Qoder 三渠道账号聚合工具，演进历程：

1. **上游 API 仓库**：3 个独立仓库 (`wild-work-buddy-api`, `wild-work-traework-api`, `wild-work-qoder-api`) → 提供各渠道基础 API 封装
2. **v0.1.x (workbuddy-wild)**：Wails GUI 包装器，仅支持 WorkBuddy 单渠道
3. **v0.2.x**：增加 TraeWork 集成，仍用 Wails
4. **v2.0.x (当前 master)**：大改版弃用 Wails，改用**系统托盘 daemon + 浏览器 Web UI**；新增 Qoder 渠道

> ⚠️ v0.2.x 代码已迁移至 `legacy-wails` 分支，不再维护。

## 0. 项目一句话

去掉 wails/WebView2，改为 **系统托盘 daemon + 系统浏览器 Web UI** 的多平台（Win/mac）多渠道账号聚合工具。

## 1. 已定决议项（不要推翻，除非有强理由并更新本节）

| # | 决议 | 说明 |
|---|------|------|
| R1 | **托盘菜单固定，不做动态内容、不做定时/事件刷新** | 用户在自己客户端操作无法捕捉，动态展示无意义 |
| R2 | ~~托盘提供「刷新积分」菜单~~ **已移除**。刷新积分改为 Web UI 面板操作 | 托盘菜单精简为：打开主界面 / 查看日志 / 退出 |
| R3 | 托盘固定菜单项：**打开主界面 / 查看日志 / 退出** | 双击托盘 = 打开主界面；不再弹"已启动"提示框 |
| R4 | **不设管理 API 鉴权** ⚠️已过期（见 0A 修正） | 现按 config.json api_key + Bearer 鉴权 |
| R5 | **Web UI 用纯静态 HTML/CSS/JS**（无前端编译链） | `go:embed` 打进单文件；实用 + 大众审美即可 |
| R6 | 托盘库：**保留 energye/systray**（已跨平台 Win/mac/Linux） | 各菜单项使用不同颜色纯 Go 生成图标，无需外部图标文件 |
| R7 | **移除 wails / WebView2 全部依赖** | 省内存与运行时；平台能力封装进 `internal/platform`（build tag 拆分） |
| R8 | daemon 单进程：一个 `http.Server` 同时服务 OpenAI 端点 + 管理 API + 静态 UI | 沿用 server 现有 ServeMux 扩展 |
| R9 | 核心业务（pool/scheduler/upstream/traework/server/login/config/auth/provider）**整体复用**，格式零迁移 | config.json / auths/ / data/state.json 兼容旧版；旧 state.json 自动迁移到 state-workbuddy.json |
| R10 | 新增渠道扩展方式：实现 `provider.Upstream` 接口 + auth 加载器 + 注册 Runtime | 模型前缀 `channel/<model>` 路由；已实现 WorkBuddy + TraeWork + Qoder 三渠道 |
| R11 | Windows 产物在 WSL 交叉编译（`GOOS=windows CGO_ENABLED=0`，已验证可行）；macOS 产物走 GitHub Actions macos-latest（cgo 必需） | WSL 无法编 darwin cgo；CI 增加 darwin job |
| R12 | **无桌面 Linux 使用 `--no-tray` 参数** | 无参启动在无 DBus 环境托盘 panic 直接 exit 并提示；`--no-tray` 跳过托盘打印信息阻塞等待 Ctrl+C |

## 2. 架构选型（依据）

| 主题 | 选型 | 理由 |
|------|------|------|
| GUI 壳 | **无**（删除 wails） | WebView2 内存开销大 + Windows 绑定；托盘 + 浏览器足够 |
| 托盘 | energye/systray v1.0.3（现有） | 已跨平台；菜单固定方案规避其不可删菜单项限制 |
| 管理后端 | 现有 http.Server 扩展 /api/* | 单端口、复用鉴权中间件（如今带 api_key）、零新服务 |
| Web UI | 纯静态 embed + fetch | 无 Node 构建链，单 exe 双击即用 |
| 登录 | 复用 internal/login + login_trae | 纯 HTTP + 本地回调端口，跨平台 |
| 平台能力 | internal/platform + build tag（windows/darwin/other） | 浏览器无痕/开机自启/消息框/日志/工作区，接口同名 |
| 构建 | WSL 交叉编译 win；CI macos-latest 编 darwin | 见 R11 |

## 3. 托盘菜单设计（当前形态）

```
wild-work
──────────
打开主界面          → 系统浏览器打开 http://<listen>/
查看日志            → 系统默认编辑器打开 data/app.log
──────────
退出                → 退出 daemon（确认框）
```

- 单击/双击/右击：右击弹菜单；**单击与双击 = 打开主界面**
- 各菜单项使用不同颜色纯 Go 生成图标（蓝色=打开、灰色=日志、红色=退出）
- 刷新积分功能已移至 Web UI 面板操作

## 4. Web UI 页面规划（纯静态，一个 index.html + app.js + style.css）

| 页面/区块 | 内容 |
|-----------|------|
| 顶部栏 | 品牌名/版本号、API 地址（点击弹窗配置）、API-Key（点击弹窗修改）、帮助/关于 |
| 账号管理 | 双列卡片网格，账号名/UID/积分/签到状态，图标按钮操作（签到/刷新/停用/删除） |
| 自动签到 | 签到时间（HH:MM 多组）+ 开机自启开关（左右布局） |
| 渠道费率 | 三渠道模型定价表（按渠道分组，合并单元格），刷新按钮 |

管理 API（REST，均挂 `/api/*`）：

```
GET  /api/state                    # 全量状态（账号/积分/签到/配置）
POST /api/login/start              # {channel} → {auth_url}
POST /api/login/cancel
POST /api/account/checkin          # {uid}
POST /api/account/checkin_all
POST /api/account/refresh          # {uid}
POST /api/account/refresh_all
POST /api/account/remove           # {uid}
POST /api/account/disable          # {uid,disabled} 停用/启用
POST /api/account/resource_detail  # {uid} → 积分明细
POST /api/config/checkin_times     # {times:["09:00","21:30"]}
POST /api/config/listen            # {host,port}
POST /api/config/api_key           # {key}
POST /api/config/autostart         # {on:bool}
GET  /api/fees                     # 渠道费率（本地缓存 + 按需刷新）
POST /api/fees/refresh             # 异步刷新费率
GET  /api/logs                     # 最近 300 行日志
POST /api/quit                     # 退出程序
```

## 5. 渠道（已实现 WorkBuddy + TraeWork + Qoder）

1. 新建 `internal/<channel>/` 包，实现 `provider.Upstream` 接口
2. `internal/auth` 增加对应 `Load<Channel>Dir()`（文件名前缀 `<channel>-*.json`）
3. 装配处注册 `server.Runtime{Kind, Pool, Upstream, StaticModels}` + `app.Runtime{..., Scheduler}`
4. 前端渠道选择器加一项；`internal/login_<channel>` 实现登录编排（如需）

> provider.Kind 即模型名前缀；server 按 `channel/<model>` 前缀路由，无需改接口。
> Qoder 渠道无签到活动：`DailyCheckin` 返回错误，调度器只做 token keepalive。

## 6. 关键不变量（改动前必读）

1. `PrepareBody` 三改写勿动：强制 `stream=true`、`tool_choice` 归一化、`developer→system`
2. 日志/面板/消息框**零 token**：不得输出 access/refresh token（调试用假 token）
3. auth 文件嵌套格式 `{auth:{...},account:{...}}`，`internal/auth.Parse` 与 login.SaveAuth 必须一致
4. `config.listen` 兼容新对象格式 + 旧字符串格式 `":7863"`
5. `data/state-*.json` 只增不减字段，向后兼容；旧 state.json 自动迁移
6. 托盘回调必须 goroutine 化
7. `config.example.json` 与 `config.Default()` 同步
8. 上游 HTTP ≥400 错误直接透传原始响应，不包装
9. 定价缓存持久化到 `data/pricing-cache.json`，启动加载，超 1h 自动刷新
10. 无桌面 Linux 必须 `--no-tray`，不带参数 panic 直接 exit 提示
11. **粘性路由**：`pickWithSticky` 优先复用上次账号，直至连续成功请求达 50 次或遭遇错误冷却。成功时 `stickySuccess` 递增计数，错误时 `stickyClear` 清除粘性记录。不使用 credits 阈值（pool 中余额是 stale 数据）。

## 7. 平台能力差异表（internal/platform）

| 能力 | Windows | macOS | Linux |
|------|---------|-------|-------|
| 打开浏览器 | rundll32 url.dll | `open <url>` | xdg-open |
| 系统消息框 | MessageBoxW | osascript display dialog | stderr |
| 开机自启 | 注册表 Run | LaunchAgent plist | 未实现 |
| 打开日志文件 | notepad | open -a TextEdit | xdg-open |
| 确认框 | MessageBoxW YESNO | osascript buttons | 默认否 |
| 无头模式 | --no-tray | --no-tray | --no-tray（推荐） |

## 8. 构建

```bash
# Windows（WSL 交叉编译）
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-H windowsgui" -o dist/wild-work.exe ./cmd/wild-work

# macOS（需 macOS 真机或 CI，cgo 必需）
GOOS=darwin GOARCH=arm64 go build -o dist/wild-work-darwin ./cmd/wild-work

# Linux 无头
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/wild-work-linux ./cmd/wild-work
```

> 日常开发循环用 `restart.bat`（落 `daemon.exe`）；`dist/` 仅发布用。

## 9. 文档索引

- [README.md](README.md) — 用户文档
- [DEVELOPMENT.md](DEVELOPMENT.md) — 开发者文档（面向 AI Agent）
- [HANDOFF.md](HANDOFF.md) — 交接文档（历史记录）