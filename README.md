# ZeroGate

> 多渠道 AI 账号聚合网关 —— 把 WorkBuddy(CodeBuddy)、WorkBuddy 国际版、TraeWork、Qoder、ZCode、AutoClaw、Atria、智谱 Flash、Groq 等 14 个渠道的多个账号聚合成一个 OpenAI 兼容 API。单进程常驻，双击启动，托盘管理，浏览器操作。

[![GitHub](https://img.shields.io/badge/GitHub-jxylisty%2FZeroGate-blue)](https://github.com/jxylisty/ZeroGate)
[![Go](https://img.shields.io/badge/Go-1.25%2B-00ADD8)](https://go.dev)
[![Platform](https://img.shields.io/badge/platform-Windows%20%7C%20macOS%20%7C%20Linux-lightgrey)](README.md#开发指南)

---

## 目录

- [功能特性](#功能特性)
- [支持的渠道](#支持的渠道)
- [快速开始](#快速开始)
- [接入 AI 客户端](#接入-ai-客户端)
- [模型路由机制](#模型路由机制)
- [配置参考](#配置参考)
- [API 参考](#api-参考)
- [Web 管理面板](#web-管理面板)
- [数据文件布局](#数据文件布局)
- [架构](#架构)
- [开发指南](#开发指南)
- [常见问题](#常见问题)
- [文档索引](#文档索引)
- [License](#license)

---

## 功能特性

- **OpenAI 兼容代理**：`POST /v1/chat/completions` + `GET /v1/models`，流式/非流式，自动透传 `temperature` / `top_p` / `max_tokens` / `stop` / `reasoning_effort` 等采样参数
- **14 渠道聚合**：云端直连 + 本地桥接双模式，多账号池化调度，模型前缀路由（`qoder/glm-5.3-flash`、`zcode/glm-5.3`……）
- **智能路由**：粘性路由（复用账号提升上游缓存命中率）→ 别名重定向链 → 跨渠道合并优选（到期最近/余额最低）→ 失败自动轮换
- **自动签到与保活**：每日定时签到领额度（含幸运抽奖、成长任务）、token 保活刷新、ZCode Weekend 额度自动领取
- **冷却状态机 + 渠道熔断**：429 短冷却 / 余额耗尽硬冷却 / 连续错误中冷却；整池不可用时渠道级熔断 60s，避免重试风暴加深风控
- **请求审计与用量统计**：每次请求落盘（客户端/模型/渠道/账号/token/耗时/状态码），CSV 用量账本，面板"白嫖节省"按官方单价折算
- **多客户端密钥**：为不同客户端签发独立 API Key，按 Key 统计用量、随时吊销
- **Web 管理面板**：账号管理（登录/签到/刷新/停用/删除）、积分明细、渠道路由配置、审计日志、桥实例管理，纯静态前端随二进制分发
- **系统托盘**：常驻托盘图标（监督循环，托盘崩溃自动重建），双击开面板，右键菜单操作；崩溃现场自动捕获到 `data/crash-stderr.log`
- **developer→system 角色转换**：自动将下游 Agent 发送的 `developer` 角色改写为 `system`，避免上游内容过滤误伤
- **Windows UTF-8 BOM 免疫**：凭证解析自动剥离 BOM，PowerShell 生成的凭证文件不会坏
- **本地服务编排**：豆包桥 / chatui / Cloudflare 隧道 / CLIProxyAPI / copilot2api 等常驻服务随网关自启（隐藏窗口，无 cmd 弹窗）

---

## 支持的渠道

| 渠道前缀 | 名称 | 接入方式 | 说明 |
|---|---|---|---|
| `workbuddy/` | WorkBuddy（CodeBuddy 国内） | 云端直连 | Tencent copilot 后端，OAuth 登录 |
| `codebuddy/` | CodeBuddy 国际版 | 云端直连 | `www.codebuddy.ai` |
| `wbintl/` | WorkBuddy 国际版 | 云端直连 | `www.workbuddy.ai`，面板粘贴 token 添加 |
| `traework/` | TraeWork（Trae SOLO） | 云端直连 | PKCE OAuth，SOLO 专属头 |
| `qoder/` | Qoder | 云端直连 | COSY 签名协议（RSA+AES+MD5），嵌套 SSE |
| `zcode/` | 智谱 ZCode | **本地桥接** 18083~18086 | 阿里滑块验证码 + 严格指纹风控，由本机 `zcode-proxy` 多实例代理 |
| `autoclaw/` | AutoClaw | 云端直连 | 手机号+验证码登录，Z.ai 官方代理通道 |
| `atria/` | Atria Dawn Preview | 云端直连 | 上海人工智能实验室，面板粘贴 Key 批量添加 |
| `glmflash/` | 智谱开放平台 Flash 家族 | 云端直连（OpenAI 兼容） | 永久免费层 |
| `groq/` | Groq | 云端直连（OpenAI 兼容） | Llama/Qwen 免费推理层 |
| `tierflow/` | tierflow.cn | 云端直连（OpenAI 兼容） | 免费聚合，Bearer Key |
| `copilot/` | 本机 copilot2api | 本机服务 4141 | OpenAI 兼容白嫖服务 |
| `doubao/` | 本机豆包桥 | 本机服务 7868 | OpenAI 兼容白嫖服务 |
| `gptgemini/` | 本机 gptgemini | 本机服务 8317 | OpenAI 兼容白嫖服务 |

> 每个渠道支持**多账号**：同名渠道的所有账号进入同一个账号池，由调度器统一挑号、轮换、冷却。可用模型以 `GET /v1/models` 实时返回为准。

---

## 快速开始

### 方式一：下载发行版（Windows）

1. 从 [Releases](https://github.com/jxylisty/ZeroGate/releases) 下载 `ZeroGate.exe`
2. 放到任意目录，双击启动
3. 右下角出现托盘图标，**双击图标** → 浏览器打开 Web 管理面板
4. 点击「+ WorkBuddy」「+ TraeWork」「+ Qoder」等按钮，在弹出的浏览器窗口里登录账号
5. 按[下一节](#接入-ai-客户端)配置你的 AI 客户端

### 方式二：源码构建

需要 Go 1.25+：

```bat
git clone https://github.com/jxylisty/ZeroGate.git
cd ZeroGate
go build -ldflags "-H windowsgui" -o daemon.exe ./cmd/zerogate
daemon.exe
```

> Windows 构建必须带 `-H windowsgui`（GUI 子系统），否则双击/开机自启会弹黑色控制台窗口。开发调试时可加 `--no-tray` 参数在终端前台运行，日志直接打到控制台。

### 命令行参数

| 参数 | 说明 |
|---|---|
| *(无参数)* | 托盘模式：常驻托盘 + Web 面板 |
| `--no-tray` | 无头模式：阻塞前台运行，日志打控制台（Linux 服务器/调试用，Ctrl+C 退出） |
| `--autostart` | 开机自启附带参数：启动时不弹提示 |

### 托盘菜单

- **打开主界面**：浏览器打开管理面板
- **查看日志**：记事本打开 `data/app.log`
- **退出**：确认后退出进程（唯一正常退出路径）

---

## 接入 AI 客户端

所有 OpenAI 兼容客户端统一接入参数：

```
Base URL:  http://127.0.0.1:7863/v1
API Key:   WildWorkAPI        ← config.json 的 api_key，可在面板修改
```

模型 ID 带渠道前缀，如 `qoder/glm-5.3-flash`、`traework/glm-5.3`、`zcode/glm-5.3-flash`。完整列表：

```bash
curl -s -H "Authorization: Bearer WildWorkAPI" http://127.0.0.1:7863/v1/models
```

### curl 冒烟测试

```bash
curl -N -X POST http://127.0.0.1:7863/v1/chat/completions \
  -H "Authorization: Bearer WildWorkAPI" \
  -H "Content-Type: application/json" \
  -d '{"model":"qoder/glm-5.3-flash","stream":true,"messages":[{"role":"user","content":"你好"}]}'
```

### 配置 Pi（models.json）

Pi 不支持自动拉模型列表，需手动编辑 `~/.pi/agent/models.json`（Windows：`C:\Users\<用户名>\.pi\agent\models.json`）：

```json
{
  "providers": {
    "ZeroGate": {
      "name": "ZeroGate",
      "api": "openai-completions",
      "baseUrl": "http://127.0.0.1:7863/v1",
      "apiKey": "WildWorkAPI",
      "models": [
        {
          "id": "workbuddy/glm-5.3",
          "name": "GLM-5.3 (WorkBuddy)",
          "reasoning": true,
          "input": ["text"],
          "contextWindow": 1000000,
          "maxTokens": 48000,
          "compat": { "maxTokensField": "max_tokens" }
        },
        {
          "id": "qoder/glm-5.3-flash",
          "name": "GLM-5.3-Flash (Qoder)",
          "reasoning": true,
          "input": ["text"],
          "contextWindow": 180000,
          "maxTokens": 32000,
          "compat": { "maxTokensField": "max_tokens" }
        },
        {
          "id": "traework/DeepSeek-V4-Pro",
          "name": "DeepSeek V4 Pro (TraeWork)",
          "reasoning": true,
          "input": ["text"],
          "contextWindow": 1000000,
          "maxTokens": 50000,
          "compat": {
            "thinkingFormat": "deepseek",
            "supportsReasoningEffort": true,
            "maxTokensField": "max_tokens"
          }
        }
      ]
    }
  }
}
```

> 上面仅为示例，完整模型清单以 `/v1/models` 为准。

### 其他客户端

Cherry Studio、ChatBox、LobeChat、OpenWebUI、Cline、Claude Code（经 OpenAI 兼容层）等任何支持 OpenAI 兼容 API 的客户端均可直接接入，填入 Base URL + API Key + 模型名即可。

---

## 模型路由机制

一次请求从进入到打上游，经过四层调度：

```
客户端请求 model=X
   │
   ▼
① 别名解析：X 命中 model_redirects → 展开为有序候选链（失败逐个降级）
            X 命中 model_merge    → 跨渠道合并：直接优选到期最近/余额最低的账号
            都没命中              → X 本身就是唯一候选（必须带渠道前缀）
   │
   ▼
② 渠道熔断检查：整池健康数=0 → 置位 60s 熔断，冷却期内快速失败（503），不打上游
   │
   ▼
③ 粘性路由：优先复用上次成功的账号（会话缓存命中率高、上游连接复用）
            连续 50 次后自动换号；遇错立即清除粘性记录
   │
   ▼
④ 挑号轮换：粘性失效后选 healthy 账号；上游报错 → 按 Classify 分类驱动冷却状态机
            → 换下一个账号重试（MaxRotate 次）→ 仍失败则下一候选渠道
```

### 冷却状态机

| 类型 | 触发 | 时长 | 说明 |
|---|---|---|---|
| 硬冷却 `hard_credit` | 余额耗尽 | 12h | 签到/刷新检测到余额恢复自动解冻 |
| 软冷却 `soft_rate` | 上游 429 | 60s | 限速恢复即回池 |
| 错误冷却 `err_cooldown` | 连续错误 ≥ `err_threshold` | 10min | 凭证失效等持续性错误 |
| 渠道熔断 | 整池健康数 = 0 | 60s | 渠道级，冷却期内不转发上游（防重试风暴） |

另有本地小时窗限速：每（渠道，账号）`requests_per_hour`（默认 300）次/小时，超限自动跳号并在耗尽时返回 429 + `Retry-After`。

### 采样参数与思考模式

- `temperature` / `top_p` / `max_tokens` / `stop` 按请求透传（指针判空，`temperature=0` 是合法的确定性设置）
- `reasoning_effort` 归一化后透传（Qoder 白名单 `low` / `medium` / `xhigh`，OpenAI 风格 `high` 自动映射 `xhigh`）
- 思考模型（GLM 等）会**强制移除 `max_tokens`**：上游将 max_tokens 视作思考+正文总预算，客户端设小会导致正文被思考吞掉
- 请求体自动改写：强制 `stream=true`、`tool_choice` 归一化、`developer`→`system`

---

## 配置参考

`config.json`（与 daemon.exe 同目录，首次启动自动生成；完整示例见 [config.example.json](config.example.json)）：

```jsonc
{
  "listen":  { "host": "127.0.0.1", "port": 7863 },
  "api_key": "WildWorkAPI",            // 客户端鉴权 Key，面板可改
  "auth_dir": "./auths",               // 凭证目录（<渠道>-*.json，含真实凭证勿外泄）
  "state_file": "./data/state.json",
  "region": "cn",
  "cooldown": {
    "hard_credit": "12h",              // 余额耗尽硬冷却
    "soft_rate": "60s",                // 429 软冷却
    "err_threshold": 3,                // 连续错误阈值
    "err_cooldown": "10m"              // 错误冷却时长
  },
  "schedule": {
    "checkin_times": ["09:00", "21:00"],  // 每日签到时间点
    "growth_times":  ["00:19"],           // 成长任务时间点
    "keepalive_hours": [22]               // token 保活小时
  },
  "upstream":  { "timeout_seconds": 300 },
  "rate_limit": { "requests_per_hour": 300 },
  "model_redirects": {                   // 别名 → 有序候选链（前缀路由失败自动降级）
    "deepseek-chat": ["codebuddy/deepseek-v4.1-flash", "qoder/glm-5.3-flash", "traework/glm-5.2"],
    "gpt-4o":        ["qoder/gpt-4o", "traework/glm-5.2"]
  },
  "model_merge": {                       // 跨渠道合并：虚拟名 → 候选集，优选到期最近/余额最低
    "glm-5.3-flash": ["qoder/glm-5.3-flash", "zcode/glm-5.3-flash", "autoclaw/glm-5.3-flash"]
  }
}
```

要点：

- `listen` 同时兼容旧字符串格式 `":7863"`
- `model_redirects` / `model_merge` 可在面板「路由配置」在线编辑，热生效并写回配置文件
- 修改端口须同步重启；Windows 下如使用 `restart.bat`，脚本内 `_PORT` 也要改

---

## API 参考

### OpenAI 兼容端点（需 `Authorization: Bearer <api_key>`）

| 端点 | 说明 |
|---|---|
| `POST /v1/chat/completions` | 聊天补全，支持 `stream: true/false`；错误一律按 OpenAI error JSON 格式返回 |
| `GET /v1/models` | 当前全部可用模型（含别名/合并虚拟名） |

### 状态与健康

| 端点 | 鉴权 | 说明 |
|---|---|---|
| `GET /status` | Bearer | 全量账号状态矩阵（渠道/UID/额度/冷却/签到记录） |
| `GET /healthz` | 无 | 健康检查 |
| `GET /api/token_usage` | **匿名** | Token 用量统计（管理面板与用量大屏依赖，刻意不挂鉴权） |

### 管理 API（`/api/*`，内置 CSRF 来源核验）

| 分组 | 端点 |
|---|---|
| 状态 | `GET /api/state`、`GET /api/usage` |
| 登录 | `POST /api/login/start`、`POST /api/login/cancel` |
| 账号 | `POST /api/account/checkin`、`checkin_all`、`refresh`、`refresh_all`、`growth`、`remove`、`disable`、`resource_detail` |
| 成长任务 | `GET /api/growth/summary`、`accounts`、`records`；`POST /api/growth/run_now` |
| AutoClaw | `POST /api/autoclaw/send_code`、`POST /api/autoclaw/login` |
| WBIntl / Atria | `POST /api/wbintl/add`；`POST /api/atria/add`、`add_batch`、`GET /api/atria/list` |
| 桥实例 | `GET /api/bridges`；`POST /api/bridges/start`、`start_all`、`stop` |
| ZCode 领取 | `GET /api/zcode/claim_status`、`POST /api/zcode/claim_now` |
| 豆包续期 | `POST /api/doubao/renew`、`GET /api/doubao/renew_result` |
| 配置 | `POST /api/config/checkin_times`、`listen`、`api_key`、`autostart`；`GET/POST /api/config/growth_times` |
| 费率 | `GET /api/fees`、`POST /api/fees/refresh` |
| 客户端密钥 | `GET /api/clients`；`POST /api/clients/create`、`delete`、`toggle` |
| 审计 | `GET /api/audit/logs`、`summary`、`analytics` |
| 路由 | `GET/POST /api/router/redirects` |
| 日志 | `GET /api/logs` |
| 军火库 | `GET /api/arsenal` |

> `/api/*` 带严格 CSRF 防护（核验 Origin/Referer），防止恶意网页跨域盲打管理接口；`/api/quit` 类无鉴权危险路由已删除。

---

## Web 管理面板

浏览器访问 `http://127.0.0.1:7863/`（或双击托盘图标），全部操作在面板完成：

- **添加账号**：点渠道按钮 → 浏览器窗口登录 → 自动落盘凭证入池（AutoClaw 走手机验证码，WBIntl/Atria 走粘贴 token/Key）
- **账号管理**：卡片显示积分/签到状态/冷却倒计时；图标按钮 = 签到 ✓ / 刷新 ↻ / 停用 ⏸ / 删除 ✕；悬停积分数字看套餐明细
- **路由配置**：在线编辑 `model_redirects`，热生效
- **审计页**：每次请求的客户端/模型/渠道/账号/token/耗时/状态码，支持按客户端 Key 汇总
- **桥实例**：查看/启停本地桥接服务（ZCode 多实例、豆包桥等），显示在线/已拉起/未运行状态
- **刷新费率**：从各上游拉取最新模型定价（缓存 1 小时），面板"白嫖节省"按官方单价折算
- **API 配置**：修改监听地址 / API Key / 开机自启

---

## 数据文件布局

```
<程序目录>/
├── daemon.exe                  # 主程序（GUI 子系统，无控制台窗口）
├── config.json                 # 配置（首次启动自动生成）
├── restart.bat                 # 构建+重启脚本（Windows 开发用）
├── auths/                      # 凭证目录（gitignore，勿外泄）
│   ├── workbuddy-*.json
│   ├── qoder-*.json
│   └── ...
└── data/
    ├── app.log                 # 运行日志（托盘「查看日志」即此文件）
    ├── requests.jsonl          # 请求审计流水（含失败请求与渠道名）
    ├── usage.csv               # 请求数账本
    ├── usage-tokens.csv        # Token 用量账本（day,kind,uid,prompt,completion,cache,model）
    ├── pricing-cache.json      # 渠道费率缓存（>1h 自动刷新）
    ├── state-<channel>.json    # 各渠道账号冷却/签到状态（进程重启恢复）
    ├── growth-history.json     # 成长任务记录
    ├── crash-stderr.log        # panic 崩溃现场捕获
    └── autostart-backup/       # 收编前注册表自启项备份
```

> `state-*.json` 中恢复的历史账号带 `placeholder: true` 标记，必须匹配到 `auths/` 下的真实凭证才会投入调度，杜绝"空壳账号"吃请求。

---

## 架构

```
 AI 客户端（ZCode / Claude Code / Pi / Cherry Studio / curl …）
        │  OpenAI 兼容协议（Bearer <api_key>）
        ▼
┌───────────────────── daemon.exe（单进程，127.0.0.1:7863）─────────────────────┐
│                                                                              │
│  server/handler.go   鉴权 → 别名/合并/前缀路由 → 熔断 → 粘性挑号 → 轮换降级     │
│       │                        │                                             │
│       │   internal/<channel>/  上游适配器（实现 provider.Upstream 接口）       │
│       │     PrepareBody（stream/tool_choice/developer→system）                │
│       │     ChatStream → SSE 解析/错误透传 → Aggregate                        │
│       │                        │                                             │
│  pool/ 账号池+冷却状态机    scheduler/ 签到·保活·解冻    audit/ 请求审计       │
│  limiter/ 小时窗限速        usage/ 用量账本         clientkey/ 多客户端密钥   │
│       │                                                                      │
│  app/  管理 API（/api/* + CSRF 防护）· 登录编排 · 费率缓存 · 桥编排            │
│  systray/ 托盘（监督循环）    cmd/zerogate/web  静态面板（go:embed）           │
└──────────┬───────────────────────────────┬───────────────────────────────────┘
           │ HTTPS 直连                     │ 本地桥接
           ▼                               ▼
  WorkBuddy / TraeWork / Qoder     zcode-proxy 多实例（18083~18086）
  / CodeBuddy / WBIntl / AutoClaw  （ZCode 专属：阿里滑块验证码 + 指纹风控）
  / Atria / GlmFlash / Groq / …
```

技术栈：Go 1.25+ / energye/systray / 纯静态 HTML+CSS+JS（`go:embed` 内嵌，无 Node 构建链）/ 无 wails、无 WebView2。

### 项目渊源

本项目是 [workbuddy-wild](https://github.com/rockswang/workbuddy-wild)（v0.2.x）的重构版（旧仓库已归档）：去掉 wails/WebView2 GUI，改为「系统托盘 + 系统浏览器 Web UI」轻量架构，内存占用更小、跨平台更简单。重构后陆续接入 ZCode、AutoClaw、Atria 等渠道并更名为 ZeroGate。

---

## 开发指南

### 常用命令

```bash
go build ./... && go vet ./... && go test ./...     # 全量校验
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 \
  go build -ldflags "-H windowsgui" -o dist/ZeroGate.exe ./cmd/zerogate   # Windows
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
  go build -o dist/ZeroGate-linux ./cmd/zerogate                          # Linux 无头
GOOS=darwin GOARCH=arm64 go build -o dist/ZeroGate-darwin ./cmd/zerogate  # macOS（cgo）
```

Windows 本机日常开发直接跑 `restart.bat`：杀 7863 端口 → 编译 → 启动，一条龙。

> ⚠️ **本机例外（2026-10-12）**：这台机器 `netstat` / `tasklist` / `taskkill` 三个控制台工具会挂死不返回，`restart.bat` 依赖它们找/杀进程，所以**点重启必卡死**。请改用 PowerShell 原生方式（cwd 为 `src-true`）：
>
> ```powershell
> Get-Process daemon -ErrorAction SilentlyContinue | Stop-Process -Force
> Start-Sleep 2
> $env:ZCODE_BRIDGE_URL='http://127.0.0.1:18083'; $env:ZCODE_BRIDGE_KEY='sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4'
> go build -ldflags "-H windowsgui" -o daemon.exe ./cmd/zerogate
> Start-Process -FilePath "D:\wild-work\src-true\daemon.exe" -WorkingDirectory "D:\wild-work\src-true"
> ```

### 代码地图

```
cmd/zerogate/main.go      # 入口：装配渠道 Runtime → HTTP 服务 → 调度器 → 托盘/无头
cmd/zerogate/web/          # 静态面板（index.html / app.js / style.css / theme.*）
internal/server/           # OpenAI 兼容 handler：路由 + 挑号 + 熔断 + 审计
internal/app/              # 管理 API + 登录编排 + 费率缓存 + 桥编排
internal/pool/             # 账号池：挑号 + 冷却状态机 + state 持久化
internal/scheduler/        # 定时签到 + token 保活 + 冷却解冻
internal/provider/         # Upstream 接口 + Kind 常量 + 共享类型
internal/upstream/         # WorkBuddy(CodeBuddy) 上游
internal/traework/         # TraeWork 上游（SOLO）
internal/qoder/            # Qoder 上游（COSY 签名 + 嵌套 SSE）
internal/auth/             # 凭证解析（嵌套/扁平双形态 + BOM 剥离）+ 原子写回
internal/config/           # 配置加载/校验/写回
internal/{login,login_trae,login_qoder}/  # 各渠道登录编排
internal/bridge/           # 本地常驻服务编排（自启收编）
internal/platform/         # 平台能力（build tag 拆分：win/darwin/other）
internal/systray/          # 跨平台托盘
```

### 新增一个渠道

1. 新建 `internal/<channel>/` 包，实现 `provider.Upstream` 接口（`ChatStream` / `RefreshToken` / `FetchModels` / `FetchModelPricing` / `UserResource` / `DailyCheckin` / `Classify` / `Stream` / `Aggregate`）
2. 新建 `internal/login_<channel>/` 包实现登录编排（或复用面板粘贴 token 模式）
3. 在 `cmd/zerogate/main.go` 注册 Runtime 装配；凭证文件命名 `<channel>-*.json` 放 `auths/`

### 关键不变量（改前必读）

详见 [AGENTS.md](AGENTS.md) 与 [DEVELOPMENT.md](DEVELOPMENT.md)，摘要：

- `PrepareBody` 三改写勿动（`stream=true` / `tool_choice` 归一化 / `developer→system`）
- 日志与面板**零 token**：任何输出不得含 access/refresh token
- `.bat` 脚本必须 CRLF + GBK(936)（`TestBatConventions` 会拦截违规）
- 构建必须 `-H windowsgui`；托盘回调必须 goroutine 化；子进程必须隐藏窗口
- 不可恢复错误（封禁/401）快速返回，渠道熔断优先于账号轮换；失败用 429/503 而非 502

---

## 常见问题

### 1. Windows 提示"未知发布者"或报毒？

本工具没有数字签名，可能被 Windows 安全中心拦截。将 exe 或所在目录加入排除项：

```
设置 → 更新和安全 → Windows 安全中心 → 病毒和威胁防护 → 管理设置 → 排除项 → 添加排除项
```

对本仓库发布包不放心的话，可克隆源码自行构建（见[开发指南](#开发指南)）。

### 2. TraeWork 通用积分不能用

已知问题，原上游项目也没解决。建议多开几个 WorkBuddy 账号，速度快不排队。

### 3. TraeWork DeepSeek V4 Flash 响应慢

官方客户端里这个模型也会排队。建议 `deepseek-chat` 别名链优先走 WorkBuddy/CodeBuddy，DeepSeek Pro 走 TraeWork（`model_redirects` 里调顺序即可）。

### 4. Qoder 模型要流式

Qoder 上游非流式聚合会返回空 content，请确保客户端使用 `stream: true`。

### 5. 请求返回 503 no_healthy_account

该渠道所有账号都在冷却/禁用，或渠道熔断中（60s 自动恢复）。打开面板看账号卡片冷却原因（余额耗尽 → 签到/换号；连续错误 → 查 `data/app.log`）。审计页每次失败请求都有渠道名和错误详情。

### 6. ZCode 渠道不通

ZCode 走本地桥接（127.0.0.1:18083~18086），面板「桥实例」确认实例在线；桥实例由网关自动拉起，也可手动启停。ZCode 渠道的完整坑清单见 [docs/ZCode-完整指南.md](docs/ZCode-完整指南.md)。

### 7. Token 用量统计为 0 或缺历史

`usage-tokens.csv` 加载器逐行容错（新旧列数混存不炸）；如用量异常先检查该文件是否被外部工具改坏。`/api/token_usage` 是匿名端点，大屏 401 属于历史 bug，现行版本已修复。

---

## 文档索引

- [AGENTS.md](AGENTS.md) —— 项目决议与系统守则（面向维护者/Agent，**改代码前必读**）
- [DEVELOPMENT.md](DEVELOPMENT.md) —— 开发者文档：代码地图、上游接口契约、数据流、粘性路由设计
- [docs/ZCode-完整指南.md](docs/ZCode-完整指南.md) —— ZCode 渠道交接总纲（账号映射、身份头清单、领取时间窗、坑清单）
- [docs/TraeWork-api.md](docs/TraeWork-api.md) —— TraeWork 上游接口分析
- [docs/upstream-reverse-engineering.md](docs/upstream-reverse-engineering.md) —— 上游逆向方法论
- [docs/lessons/](docs/lessons/) —— 九篇故障复盘（WAF 风控、BOM 事故、桥卡死、SSE 计量、封号风暴、托盘黑框……每篇都有根因与铁律）

---

## License

MIT — 仅供个人学习使用，请遵守各上游平台服务条款。
