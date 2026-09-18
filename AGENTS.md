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
7. 上游 HTTP ≥400 错误：**OpenAI 标准 error 格式（含 error.message）原样透传；非标准格式（字节系上游）包装成标准格式**（ZCode 等客户端解析不了非标准体 → reason=unknown；2026-09-18 修复）
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
---

## 7. ZCode 渠道经验（2026-09-17~18 排查结论，接手必读）

### 7.1 阿里云 WAF 风控（3012）机制

- `HTTP 405 + {"code":3012,"msg":"request has been blocked due to unusual activity."}` = **账号级行为标记**，不是封号（封号是 401/403 凭证失效）
- 触发源：自动 claim/领取循环、验证码求解失败循环、高频探测（额度刷新每 60s 打一次 3012 也是在喂养风控）
- 恢复：**该账号停止一切上游请求** 24-72h（顽固者 1 周+）；官方客户端正常活动有助恢复；换 IP/设备/客户端形态**无效**（账号维度）
- 铁律：失败绝不循环重试；验证一律单次；自动 claim 永远关闭
- 账号现状：大小号（zai/bigmodel）被标养号中；Gmail 注册号 `ccacfe94` 带 1 亿 start-plan 卡片可用

### 7.2 plan 与通道区分（最容易踩的坑）

| plan | 端点 | 凭证 | 额度 |
|------|------|------|------|
| **start-plan** | `zcode.z.ai/api/v1/zcode-plan/*` | JWT（Bearer） | **额度卡片（1 亿 Global Build 等）在这里** |
| coding-plan | `api.z.ai`（直连） | API Key | 国际站登录解析出的 API Key 属于它，通常无额度 |

- 额度卡片是 start-plan 资源 → 用 API Key/coding-plan 通道请求会 `429/1113 余额不足`——**这不是号废了，是 plan 配错**
- LLM 端点：`/api/v1/zcode-plan/anthropic/v1/messages`（Anthropic 协议，强制 stream）

### 7.3 请求指纹（ZCode 3.12.3 形态）

- LLM 头族：`HTTP-Referer` `User-Agent: ZCode/{ver} ai-sdk/anthropic/3.0.81` `X-ZCode-App-Version` `X-Title: Z Code@cli` `X-ZCode-Agent: glm`（3.12.3 内联第 8 位）`X-Platform` `X-Release-Channel` `X-Client-Language/Timezone` `X-Os-Category/Version`
- **LLM 请求永不带 `X-Device-Mid`**（那是 control-plane/billing 的 HRt 头族）
- `x-zcode-session-type` 只能 `main`/`subagent`/`other`
- billing（control-plane）对 API 形态很严，正常账号也可能 3012——**不代表对话通道死**
- 对话通道生死判据：无 VP 手打 LLM 端点 → `3007 captcha verify failed` = **通道活**（只差验证码）；`3012` = 账号被标

### 7.4 本机现役方案：zcode-proxy（TriDefender/zcode-api v4.6.6）

- 位置 `D:\wild-work\research\zcode-proxy\`（单文件 exe + config.yaml + 源码，MIT）
- 验证码：**进程内 happy-dom 求解器**（无需浏览器）——captcha_node/jsdom 老方案解出的 VP 无效（3007），已弃用
- 端口 **18080**（8080 被 Gemini 前端占用，勿动）
- 启动（必须清代理，HTTPS_PROXY 会走 Clash 被上游断连）：
  `ZCODE_CLAIM_ENABLED=false HTTPS_PROXY= HTTP_PROXY= ALL_PROXY= ./zcode-proxy.exe --cli serve`
- 登录：`./zcode-proxy.exe auth login zai`（凭证存 `~/.zcode-proxy/credentials.json`，换号重新登录即可）
- 接入：`http://127.0.0.1:18080/v1`，Key 见其 config.yaml `proxyApiKey`，模型 `glm-5.3-flash`/`glm-5.3`
- 详细：`research/zcode-proxy/使用说明.md`；翻车全程 `research/zcode2api-plus/翻车复盘-20260917.md`（zcode2api-plus 3002 已废弃停用）

### 7.5 ZeroGate zcode 渠道与桥接

- `internal/zcode/zcode.go` 现为 jsdom(captcha_node) 直连实现，验证码无效不可用
- 正解：桥接本地 zcode-proxy（Anthropic 协议兼容，换 Base+鉴权即可，详见 zcode.go 桥接模式注释）

### 7.6 多账号接入（2026-09-18 实施）

- **架构**：每账号 = 一个 zcode-proxy 实例（独立端口 + 独立凭证目录 `ZCODE_CREDENTIALS_DIR`），
  Wild-Work 账号文件 `auths/zcode-*.json` 的 `bridgeUrl` 字段路由到对应实例
  （缺省回退全局 `ZCODE_BRIDGE_URL` 环境变量）
- **加号流程**（脚本 `research/zcode-proxy/add-zcode-account.sh`）：
  1. `bash add-zcode-account.sh <账号名> <端口> [zai|bigmodel]` → 生成 auths 账号文件 + 实例启动脚本
  2. 登录该实例：`ZCODE_CREDENTIALS_DIR=<实例creds目录> ./zcode-proxy.exe auth login <provider>`（浏览器授权）
  3. 启动实例：`bash instances/acc-<名>/start.sh &`
  4. 重启 Wild-Work daemon（restart.bat）
- **纪律（防风控）**：每实例 `ZCODE_CLAIM_ENABLED=false`；验证单次；失败不循环
- 注：zcode-proxy 源码本地 fork（research/zcode-proxy），支持 `ZCODE_CREDENTIALS_DIR` 覆盖
  凭证目录（原项目硬编码 ~/.zcode-proxy）；升级上游前需重新应用此补丁（src/auth/store.ts）

### 7.7 每周领取（claim）低频方案（2026-09-18 实施）

- **教训**：原 zcode2api-plus 自动 claim 是"失败→5 分钟重试"循环，把账号喂进 WAF 标记
- **现方案**：Windows 计划任务 `ZCodeClaimWeekly`，每周五 23:30 执行
  `research/zcode-proxy/claim-weekly.bat` → 对每个实例（instances/acc-*）执行**一次**
  `zcode-proxy.exe claim now`（单次命令，失败不重试不循环）
- claim 常驻轮询保持关闭（ZCODE_CLAIM_ENABLED=false）
- 手动触发：`schtasks /run /tn ZCodeClaimWeekly`；删除：`schtasks /delete /tn ZCodeClaimWeekly /f`
- claim 目标为限量/周末体验套餐（"3 亿 3 亿发"的活动包）；`claim list` 可先查看可领项

### 7.8 周末包领取方法（2026-09-18 实测成功，大号已领 3 亿）

#### 机制（破解全过程）
- **投放**：周五开始投放 "ZCode Weekend Build"（zcode-v3-start-plan-wk-0918，
  GLM-5.3-Flash 300M token）；preview 显示的 activates=15:00Z（**北京时间 23:00 生效**）
- **领取接口**：`GET /api/v1/zcode-plan/billing/preview?app_version=&platform=` +
  `POST /api/v1/zcode-plan/billing/claim` body `{plan_id}`（3.12.3 客户端
  `coding-plan-subscription.getManualClaimPlanPreviews` / `claimManualPlan` 同源）
- **3001 之谜（关键坑）**：preview/claim **必须带完整身份头**（X-ZCode-App-Version、
  X-Title、X-ZCode-Agent、HTTP-Referer、X-Platform 等），匿名/裸 JWT 一律 3001
  parameter error——参考项目 v4.6.6 的 claim CLI 缺身份头（bug），2026-09-18 已修
  （src/claim/client.ts + runtime.ts 注入 buildIdentityHeaders，重编译 exe）
- **投放前**：未上架时 preview 返回 3001（不是 404！）——看到 3001 别急着改代码，
  先确认是否已到投放窗口（周五晚）

#### 操作命令（对任意已登录实例）
```bash
cd /d/wild-work/research/zcode-proxy
# 查看可领（需对应实例/凭证已登录）
ZCODE_CREDENTIALS_DIR=<实例creds目录> HTTPS_PROXY= HTTP_PROXY= ALL_PROXY= ./zcode-proxy.exe claim list
# 领取（一次）
ZCODE_CREDENTIALS_DIR=<实例creds目录> HTTPS_PROXY= HTTP_PROXY= ALL_PROXY= ./zcode-proxy.exe claim now
```

#### 自动化
- Windows 计划任务 `ZCodeClaimWeekly`：每周五 19:00 跑 claim-weekly.bat（卡片 19 点投放即领，23:00 激活），
  遍历 instances/acc-* 每个**一次** claim now（失败不重试不循环——防 WAF 标记）
- 领取后 23:00 生效，实例对应账号即可消耗；到期约 2 天后
- 2018-09-18 结果：大号（acc-big 18083）已领成功（Weekend Build 300M，23:00 生效）

- **preview 语义（2026-09-18 晚修正）**：`billing/preview` 只返回**未领**的包——
  返回空列表 = 已领过或当前无投放，**不等于"不在名单"**！
  判断实际持有：查 `billing/balance` 的 `data.plans`（列出全部 plan 及 status，
  即使未激活也显示；激活前 `balances` 数组里没有 bucket，属正常）
- 实例验证各号持仓：手动带完整身份头 GET `billing/balance?app_version=3.12.3&platform=win32-x64`
  （缺头/缺参返回 3001）；或用本机实例 /quota（只回 balances，回 plans 需手动）
#### 用电/防标纪律（再次强调）
- 领取一次就好，不要对同一包重复 claim（claim 返回已领/无包）
- 本地 18080/18083 实例意外关闭后的重启命令见 instances/acc-*/start.sh

### 7.9 周末包领取内置化（2026-09-18 实施）

- **zcode-proxy fork** 新增管理端点 `POST /admin/claim-now`（鉴权 `Bearer <proxyApiKey>`，
  请求体 `{"mode":"list"|"now"}`，默认 now）：实例内执行**一次** preview+claim，
  返回结构化 JSON（ok/plans/claimed/error），失败不重试不循环
- **ZeroGate** `internal/zcode/weekly_claim.go`：`StartWeeklyClaim` goroutine，
  每周五 23:30-23:45 对池中每个带 `BridgeURL` 的账号调一次 `/admin/claim-now`
  （key=ZCODE_BRIDGE_KEY），结果打日志；30s tick 仅在周五窗口+当日未执行才触发
- `internal/pool` 新增 `Auths()`（遍历账号引用，供桥接类功能使用）
- 原 Windows 计划任务 `ZCodeClaimWeekly` 仍保留作为兜底（claim-weekly.bat 遍历实例；
  与新调度重复执行也无害——claim 幂等，重复领取返回无包）
- 周五晚间如有测试需要：手动 `curl -X POST http://127.0.0.1:<实例端口>/admin/claim-now
  -H "Authorization: Bearer sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4"`（确认账号已登录该实例）

### 7.10 面板自助添加 ZCode 账号（2026-09-18 实施）

- 面板 ZCode 分组的「＋」→ 通用登录弹窗打开授权页 → 浏览器授权 → 自动完成：
  - `internal/zcode/provision.go`：分配端口（18086 起）→ 创建 instances/acc-lognXXXX/
    （config.yaml + start.sh + creds/）→ 启动 serve 实例 → 发起 `auth login zai`
    （后台解析授权 URL）→ 凭证文件生成即成功 → 写 auths/zcode-*.json（bridgeUrl 占位）
    → reloadAccounts → login_busy=false
  - 默认 provider=zai（面板暂不选 bigmodel；需要国内号时手动建或用请求体扩展）
  - 环境变量覆盖：`ZCODE_PROXY_EXE` / `ZCODE_PROXY_INSTANCES_DIR`
- 取消：`/api/login/cancel`（实例目录保留，含 serve 进程，凭证可复用）
- 测试残留实例：`instances/acc-logn*`（超时/取消产物），删目录即可

### 7.11 WorkBuddy 国际版（wbintl）网页登录（2026-09-18 实施）

- 参考实现：ardeyouxipianyi/workbuddy2api-hub（README 标"OAuth 一键免客户端登录"）
- 协议（与国内同构，仅换 host/UA）：
  - `POST https://www.workbuddy.ai/v2/plugin/auth/state?platform=CLI` → {state, authUrl}
  - 浏览器打开 authUrl（服务端返回 codebuddy.cn 腾讯统一登录页，正常）
  - 轮询 `GET /v2/plugin/auth/token?state=` → {accessToken, refreshToken, expiresIn}
  - `GET /v2/plugin/login/account?state=`（Bearer）拿 uid/nickname
  - pending 业务码 11217；登录 TTL 600s
- 头：UA `WorkBuddy/5.5.2 WorkBuddy AI/5.5.2 CLI/5.5.2`，Origin www.workbuddy.ai
- ZeroGate：login_pkg 加 StartIntl/PollIntl；StartLoginFor 支持 channel=wbintl
  （面板 WBIntl 组「＋」即网页授权，凭证落 auths/wbintl-<uid>.json）；粘贴 token 表单保留兜底
