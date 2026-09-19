# ZeroGate 项目指南（AGENTS.md）

> 本文件面向接手本仓库的 AI Agent 与开发者。**先读顶部「目录与入口真相」，再动代码。**

---

## ⚠️ 0A. 目录与入口真相（2026-09-18 统一入口）

### 唯一事实源：代码与数据都在 `src-true\` 目录

| 层面 | 位置 | 说明 |
|---|---|---|
| 源码 + git 仓库 | `src-true\`（本文件所在） | remote `zero` = `github.com/jxylisty/ZeroGate`；master 跟踪 zero |
| 常驻程序 | `src-true\daemon.exe` | 监听 `127.0.0.1:7863`，OpenAI 兼容端点 + 管理 API + Web UI |
| 构建与启动 | `src-true\restart.bat` | 杀 7863 → `go build -o daemon.exe ./cmd/wild-work` → 启动（唯一入口链） |
| 运行时数据 | `src-true\auths\`、`src-true\data\`、`src-true\config.json` | 单份；main.go 开头固定工作目录，从任何路径启动都指向这里 |
| 根目录 (`D:\wild-work`) | 仅有 `启动.bat`（调用 restart.bat）+ 独立资产 | **不是**代码或数据存放地 |

### 禁止事项（防重蹈覆辙）

1. **严禁在根目录编译或运行任何 exe**，不得新建 auths / data / config.json（daemon 只认 `src-true\`）。
2. **严禁删除或引用 `_archive\`**：归档的旧根目录运行时仅作历史备份。
3. 根目录与 daemon 无关的独立资产勿动：`.env`、`captcha_node\`、`research\`、`tools\` 等。
4. `auths/` 含真实凭证（已 gitignore），**绝不入库、绝不打日志、绝不回显到前端**。
5. API 端口 7863、API Key 见 `config.json`；修改端口须同步 `restart.bat` 中的 `_PORT`。

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
3. 启动装配：在 `cmd/wild-work/main.go` 注册 `server.Runtime` 与 `app.Runtime`。
4. 账号接入模式：
   - **云端直连（proxy/direct）**：WorkBuddy、TraeWork、Qoder、CodeBuddy、WBIntl、AutoClaw、Atria 均直接发起 HTTPS 请求上云。
   - **本地桥接（bridge）**：ZCode 因阿里滑块验证码与严格指纹风控，由本地独立多开的 `zcode-proxy` 实例（18083~18086）负责代理桥接。

---

## 3. 核心 API 端点

- `POST /v1/chat/completions`、`GET /v1/models`：标准 OpenAI 兼容接口（`Authorization: Bearer <api_key>`）。
- `GET /status`：全量账号状态矩阵（渠道、UID、额度、冷却状态、签到记录）。
- `GET /api/token_usage`、`GET /healthz`：Token 用量审计与健康检查。
  - `/api/token_usage` **匿名可读**（管理面板和用量大屏都不带 Bearer key，别再挂 withAuth——2026-09-19 修过一次 401 导致大屏恒为 0）。
  - 用量记录器落盘 `data/usage-tokens.csv`，列：`day,kind,uid,prompt,completion,cache_read,cache_write[,model]`（model 为 2026-09-19 新增的末列，旧行无此列时 `/api/token_usage` 的 `models` 里记作 `"<渠道> (历史)"`）。
  - **CSV 加载器必须逐行容错**（`FieldsPerRecord = -1` + 跳坏行，usage.go load/loadTokens）：Go 的 `csv.ReadAll()` 默认要求全文件列数一致，2026-09-19 给 tokens.csv 加 model 列后新旧混存曾把全部历史清零一次（用户报告"关机重启数据全丢"实为加载失败）；以后再给这两个 CSV 加列不需要改加载器。
  - 面板"白嫖节省"按 `app.js` 的 `MODEL_PRICES`（各模型官方 API 单价，2026-09 检索）× 按模型用量折算；接了新模型记得在表里补一条价格。

---

## 4. 关键系统守则（改动必读）

1. **Windows UTF-8 BOM 免疫**：
   所有凭证加载统一走 `auth.Parse`，内部自动执行 `bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))`，防止 Windows / PowerShell 文件头破坏 JSON 解析。
2. **账号池占位符隔离**：
   `data/state-*.json` 恢复的历史记录初始带有 `placeholder: true`。必须经由 `Add` 或 `SyncToDir` 匹配到真实 `auths/*.json` 凭证后方可投入调度，**严禁调度无凭证空壳**。
3. **服务启动使用 SyncToDir**：
   `cmd/wild-work/main.go` 启动时各渠道必须通过 `pool.SyncToDir(auths)` 初始化，自动清除已删除的旧账号。
4. **思考模型 max_tokens 策略**：
   在 `upstream.PrepareBody` 中强制移除 `max_tokens`（GLM 等模型将 max_tokens 视作思考+正文总预算且优先消耗给思考，客户端设置过小会导致正文被吞）。
5. **错误状态码透传规范**：
   上游 HTTP ≥400 错误：标准 OpenAI error 格式原样透传；非标准错误体统一包装为标准格式，防止下游客户端崩溃。
6. **粘性路由（Sticky Routing）**：
   `pickWithSticky` 优先复用上一成功账号（单号上限 50 次或遇错清除），成功请求由 `stickySuccess` 递增。

---

## 5. 现役账号与实例映射速查

| 显示名称 | UID | 接入方式 / 端口 | 备注 / 主要额度 |
|---|---|---|---|
| **主号bigmodel** | `zcode-big` | 桥接 `http://127.0.0.1:18083` | 智谱 300M Weekend Build |
| **副卡gmail** | `zcode-fuka` | 桥接 `http://127.0.0.1:18084` | 智谱 300M Weekend Build + 每日 |
| **副卡bigmodel** | `zcode-cn` | 桥接 `http://127.0.0.1:18085` | 智谱 每日额度 |
| **主号gmail** | `zcode-fuka2` | 桥接 `http://127.0.0.1:18086` | 智谱 备用/自用实例 |
| **其他平台** | 各自 UID | 直连官方云端 API | TraeWork / Qoder / WorkBuddy / CodeBuddy / Atria |

> ⚠️ 注：历史 `18080` 端口已废弃关停，默认桥接兜底端口为 `18083`。

---

## 6. 经验教训与故障复盘（Docs / Lessons）

深入的逆向过程、协议分析与历史排查复盘已统一归档至 **`docs/lessons/`**：

- 📘 [`docs/lessons/01-zcode-waf-and-claim.md`](file:///d:/wild-work/src-true/docs/lessons/01-zcode-waf-and-claim.md)：ZCode 阿里 WAF 3012 风控机制、Start-Plan 区分与 Weekend Claim 领取破解。
- 📘 [`docs/lessons/02-wbintl-oauth.md`](file:///d:/wild-work/src-true/docs/lessons/02-wbintl-oauth.md)：WorkBuddy 国际版（WBIntl）网页授权与 OAuth 协议细节。
- 📘 [`docs/lessons/03-bom-and-bridge-port-postmortem.md`](file:///d:/wild-work/src-true/docs/lessons/03-bom-and-bridge-port-postmortem.md)：2026-09-19 Windows BOM 破坏凭证解析、空壳占位符调度与 18080 废弃端口级联冷却故障全流程复盘。
- 📘 [`docs/lessons/04-zcode-bridge-stall-and-routing-hardening.md`](file:///d:/wild-work/src-true/docs/lessons/04-zcode-bridge-stall-and-routing-hardening.md)：2026-09-19 晚 ZCode 桥接签发卡死 → 120 秒"假成功"（200/0 token）复盘；零 token 长流熔断、zcode 渠道到期升序轮转、账号停用需代理层+网关层一致等加固与铁律。
- 📘 [`docs/lessons/05-qoder-nested-sse-usage.md`](file:///d:/wild-work/src-true/docs/lessons/05-qoder-nested-sse-usage.md)：2026-09-19 Qoder 流式 token 计 0 根因（嵌套 SSE 转义 usage 漏抓）与修复；上游 `billable:false` 才是"积分不变"真相。
