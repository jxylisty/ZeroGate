# ZeroGate 项目提示词（AGENTS.md）

> 本文件面向接手本仓库的 AI Agent / 开发者。**先读顶部「目录与入口真相」，再动代码。**

---

## ⚠️ 0A. 目录与入口真相（2026-09-18 统一入口后）

### 唯一事实源：代码与数据都在 `src-true\` 目录

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
3. **不要删除/绕过 `_archive\`**：2026-09-18 归档的旧根目录运行时（exes、data、auths、config、doubao 实验产物），只作备份
4. 根目录 `.env`、`zcode_creds_*.json`、`captcha_node\`、`homework\`、`research\`、`tools\`、`gate_icon.png`、`使用说明.txt` 是独立资产，与 daemon 无关，别动
5. **git push 默认进 `zero`（jxylisty/ZeroGate）**
6. auths/ 含真实凭据（已 gitignore），**绝不入库、绝不打日志、绝不输出到对话/界面**
7. API 端口 7863、API Key 见 `config.json` 的 `api_key`（请求带 `Authorization: Bearer <key>`）；改端口必须同步 restart.bat 的 `_PORT`

### 接手时的标准动作

```bash
# 状态确认
netstat -ano | findstr 7863          # 应在监听（daemon.exe）
curl -H "Authorization: Bearer <api_key>" http://127.0.0.1:7863/status   # 看账号分组与积分
# 改代码后：编译 + 重启（先杀 7863 占用，再 go build -o daemon.exe ./cmd/wild-work，最后启动 --no-tray）
# 验证通过后：git add -A && git commit && git push（进 ZeroGate）
```

### 命名现状

- 仓库/项目名已更名 **ZeroGate**；代码内标识（module 名、产物 `wild-work.exe`、部分日志前缀）仍沿用旧名，属历史遗留，**暂不做全量改名**，等用户明确要求再动。

---

## 1. 项目定位

ZeroGate 是一个**多渠道账号聚合网关**：把 CodeBuddy(WorkBuddy)/TraeWork/Qoder/AutoClaw/ZCode/WBIntl 多个渠道的账号聚合成一个 OpenAI 兼容 API，系统托盘常驻 daemon + 浏览器 Web UI 管理，自动签到 + token 保活 + 积分刷新。

架构是 **单进程 daemon**：一个 `http.Server` 同时服务 OpenAI 兼容端点、管理 API 和静态 Web UI；无前端编译链（`go:embed` 纯静态 HTML/CSS/JS）。

## 2. 渠道扩展方式（已实现 7 个渠道）

1. 新建 `internal/<channel>/` 包，实现 `provider.Upstream` 接口
2. `internal/auth` 增加对应 `Load<Channel>Dir()`（文件名前缀 `<channel>-*.json`）
3. 装配处注册 `server.Runtime{Kind, Pool, Upstream, StaticModels}` + `app.Runtime{..., Scheduler}`
4. 前端渠道选择器加一项；`internal/login_<channel>` 实现登录编排（如需）

> provider.Kind 即模型名前缀；server 按 `channel/<model>` 前缀路由，无需改接口。
> Qoder 渠道无签到活动：`DailyCheckin` 返回错误，调度器只做 token keepalive。
> auth 文件嵌套格式 `{auth:{...},account:{...}}`，`internal/auth.Parse` 兼容嵌套/扁平两种形态（LoadAutoClawDir 等加载器统一走 Parse）。

## 3. 对外端点（以 internal/server/handler.go 为准）

- `POST /v1/chat/completions`、`GET /v1/models` — OpenAI 兼容（`Authorization: Bearer <api_key>`）
- `GET /status` — 全量账号状态（分组、积分、冷却、签到）
- `GET /api/token_usage`、`GET /healthz`
- 其余管理端点（签到/刷新/费率/登录/配置）见 handler.go 路由注册

## 4. 关键不变量（改动前必读）

1. `PrepareBody`（upstream 渠道）四改写勿动：强制 `stream=true`、`tool_choice` 归一化、`developer→system`、**删 `max_tokens`**（GLM 等思考模型把 max_tokens 当思考+正文总预算且思考优先，客户端传的 max_tokens 会被思考占满、正文为空 → ZCode 报 empty_model_response；2026-09-18 实测修复）
2. 日志/面板/消息框**零 token**：不得输出 access/refresh token（调试用假 token）
3. `config.listen` 兼容新对象格式 + 旧字符串格式 `":7863"`
4. `data/state-*.json` 只增不减字段，向后兼容
5. 托盘回调必须 goroutine 化
6. `config.example.json` 与 `config.Default()` 同步
7. 上游 HTTP ≥400 错误直接透传原始响应，不包装
8. 定价缓存持久化到 `data/pricing-cache.json`，启动加载，超 1h 自动刷新
9. 无桌面 Linux 必须 `--no-tray`，不带参数托盘 panic 直接 exit 提示
10. **粘性路由**：`pickWithSticky` 优先复用上次账号，直至连续成功请求达 50 次或遭遇错误冷却。成功时 `stickySuccess` 递增计数，错误时 `stickyClear` 清除粘性记录。不使用 credits 阈值（pool 中余额是 stale 数据）。

## 5. 平台能力差异表（internal/platform）

| 能力 | Windows | macOS | Linux |
|------|---------|-------|-------|
| 打开浏览器 | rundll32 url.dll | `open <url>` | xdg-open |
| 系统消息框 | MessageBoxW | osascript display dialog | stderr |
| 开机自启 | 注册表 Run | LaunchAgent plist | 未实现 |
| 打开日志文件 | notepad | open -a TextEdit | xdg-open |
| 确认框 | MessageBoxW YESNO | osascript buttons | 默认否 |
| 无头模式 | --no-tray | --no-tray | --no-tray（推荐） |

## 6. 构建

日常开发循环用 `restart.bat`（落 `daemon.exe`）；`dist/` 仅发布用：

```bash
# Windows
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-H windowsgui" -o dist/wild-work.exe ./cmd/wild-work
# macOS（需 macOS 真机或 CI，cgo 必需）
GOOS=darwin GOARCH=arm64 go build -o dist/wild-work-darwin ./cmd/wild-work
# Linux 无头
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o dist/wild-work-linux ./cmd/wild-work
```

> 产物名 `wild-work.exe` 为历史遗留，代码全面更名 ZeroGate 前沿用。