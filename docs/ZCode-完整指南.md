# ZCode 渠道完整指南（2026-09-19 定稿）

> 本文是 wild-work（ZeroGate）ZCode 渠道的唯一完整参考。新对话/新接手者先读
> AGENTS.md 第 7 章，细节以本文为准。所有结论均为 2026-09-17~19 实测。

## 1. 账号映射（用户确认版）

| 显示名 | user_id | 实例/端口 | 额度（09-19） | 凭证目录 |
|---|---|---|---|---|
| 主号bigmodel | 85641786857446263 | acc-big / 18083 | 3 亿 Weekend ✅ | instances/acc-big/creds |
| 主号gmail | ccacfe94-147d-426e-9d8f-ab95fa22f11d | acc-fuka2 / 18086 | 3.95 亿 ✅（09-19 重新 OAuth 登录，显示/优先级正常） | instances/acc-fuka2/creds |
| 副卡gmail | 6ccde037-2b78-4502-9686-9b65529db2ce | acc-fuka / 18084 | 1 亿 Global + 3 亿 Weekend + 每日 800 万 ✅ | instances/acc-fuka/creds |
| 副卡bigmodel | 18201789620455567 | acc-cn / 18085 | 每日 300 万（周末包未投放给它） | instances/acc-cn/creds |
| 副卡zai（第5号） | —（凭证加密，未解析） | acc-logn0200 / 18087 | 2026-09-30 领到 Trust Build 1 亿（此前为"孤立第 5 号"，已登记） | instances/acc-logn0200/creds |
| （历史）18080 实例 | — | 已停用 | 曾被误覆盖凭证 | ~/.zcode-proxy |

**命名纪律**：以 auths/zcode-*.json 的 nickname 为准；uid 与显示名一一对应，勿凭记忆猜。

## 2. 基础设施

- **zcode-proxy**（TriDefender/zcode-api v4.6.6 本地 fork，MIT）：`D:\wild-work\research\zcode-proxy\`
  - fork 改动：①`ZCODE_CREDENTIALS_DIR` 凭证目录隔离（src/auth/store.ts）②claim 加完整身份头（src/claim/client.ts+runtime.ts，修 3001）③`/admin/claim-now` 端点（**有 bug：调用后实例进程退出，勿用！** 用 CLI 方式）
  - 升级上游后需重新应用补丁
- **实例启动**：`bash instances/acc-<名>/start.sh &`（含全部纪律：claim 关、清代理、独立凭证）
- **zcode-proxy 启动必须清代理**：`HTTPS_PROXY= HTTP_PROXY= ALL_PROXY=`（本机 Clash 7897 会致上游断连）
- **Wild-Work daemon**：7863；启动须带 `ZCODE_BRIDGE_URL=http://127.0.0.1:18083 ZCODE_BRIDGE_KEY=sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4`（restart.bat 已内置）
- **模型**：`zcode/glm-5.3-flash`、`zcode/glm-5.3`（经 Wild-Work 自动路由，短命额度优先）

## 3. 领取方法（核心：完整请求头）

### 3.1 协议（对齐 workbuddy2api-hub / ZCode 3.12.3 客户端）

```
POST {zcode.z.ai}/api/v1/zcode-plan/billing/preview?app_version=3.12.3&platform=win32-x64
GET  {zcode.z.ai}/api/v1/zcode-plan/billing/claim          ← 实际为 POST body {"plan_id"}
GET  {zcode.z.ai}/api/v2/plugin/auth/state?platform=CLI     ← 登录（网页授权）
GET  {zcode.z.ai}/v2/plugin/auth/token?state=               ← 登录轮询
GET  {zcode.z.ai}/v2/plugin/login/account?state=            ← 登录取昵称
```

### 3.2 完整身份头（缺任一 → 3001 parameter error！）

```
Authorization: Bearer <JWT>          # billing 平面必须；preview 匿名也 3001（实测）
User-Agent: ZCode/3.12.3
X-ZCode-App-Version: 3.12.3
X-Title: Z Code@cli
X-ZCode-Agent: glm
X-Platform: win32-x64                # preview 的 query platform 参数需一致
X-Release-Channel: production
X-Client-Language: zh-CN
X-Client-Timezone: Asia/Shanghai
X-Os-Category: windows
X-Os-Version: 10.0.26200
X-Device-Mid: <uuid>                 # billing 平面带；LLM 请求永不带！
```

### 3.3 时间窗口（实测）

- **投放**：周五 19:00（卡片出现）→ claim 窗口周五 19:00~晚间
- **激活**：领取后当日 23:00（preview 的 activates=15:00Z）
- **有效期**：至周日 01:00Z（约 2 天）
- **周六无第二轮投放**（09-19 19:00 四号全部 "No claimable plans" 实证）
- **每日名额刷新假设**：未证实；调度已覆盖周五+周六 19:00 兜底

### 3.4 领取命令（CLI 方式，可靠）

```bash
cd /d/wild-work/research/zcode-proxy
ZCODE_CREDENTIALS_DIR="D:/wild-work/research/zcode-proxy/instances/acc-<名>/creds" \
  HTTPS_PROXY= HTTP_PROXY= ALL_PROXY= ./zcode-proxy.exe claim list   # 查看可领
ZCODE_CREDENTIALS_DIR="..." HTTPS_PROXY= ... ./zcode-proxy.exe claim now    # 领取（一次）
```

### 3.5 自动化（双保险）

- **ZeroGate 内置调度** `internal/zcode/weekly_claim.go`：周五+周六 19:00-19:15，
  遍历 instances/acc-* 各执行一次 CLI claim now，结果写 data/app.log
- **Windows 计划任务** `ZCodeClaimWeekly`：每天 19:00 触发 claim-weekly.bat，
  bat 内周五/周六守卫执行
- **已领的号 preview 为空（正常）**——判断持仓查 `billing/balance` 的 `data.plans`！

## 4. 成功案例（同设备多账号领取实证）

- **2026-09-18（周五）19:2x**：**大号 bigmodel（8564）** 与 **主号 gmail（6ccde037）**
  在**同一台电脑、同一个 ZCode 客户端**（以及 zcode-proxy 实例）均成功领取
  Weekend Build 3 亿——**同设备多账号领取互不影响**（设备去重只影响"激活"，
  不影响 claim 领取；领取平面请求不带 X-Device-Mid）
- 主号 gmail 另在官方客户端内点击卡片领取成功（claimManualPlan OK ×2）
- 失败案例：副卡 bigmodel（1820178）周五/周六 preview 均空 = **未获投放**（非请求问题）

## 5. 坑清单（血泪，逐条实测）

1. **3001 parameter error**：身份头不全 或 当前无投放。先补全 3.2 头再判断
2. **3007 captcha verify failed ≠ 会话死亡**：通道活着只差验证码。
   Classify 已修（3007→ErrClient），误判曾致健康账号整池禁用
3. **preview 空 ≠ 没资格**：已领的包不再显示；持仓看 balance.plans
4. **HTTP /admin/claim-now 端点会杀死实例进程**：生产用 CLI 方式，端点弃用
5. **LLM 请求永不带 X-Device-Mid**；billing/claim 平面要带
6. **x-zcode-session-type 只能 main/subagent/other**（曾错用 "start-plan"）
7. **UA 必须带 SDK 后缀**：`ZCode/3.12.3 ai-sdk/anthropic/3.0.81`
8. **流式**：上游先 `thinking_delta`（字段 thinking）后 `text_delta`（字段 text），
   转换器都要处理（已修，否则客户端"1 秒停住无报错"）
9. **风控恢复**：3012 标记停手后 24-72h~1 周衰减；重新 OAuth 登录=全新干净凭证
   （大号/主号gmail 实证）；**旧 JWT 被标不影响新登录**
10. **自动 claim/验证码失败循环 = 账号标记根源**：失败绝不重试，验证只打一次
11. **凭证覆盖事故**：授权前确认浏览器登录的是目标账号（曾把主号实例凭证
    误覆盖为副卡）；auths 凭证误覆盖无备份，只能重新授权
12. **Gemini/其他 agent 占 8080**：zcode-proxy 实例用 18080+ 端口段

## 6. Wild-Work 集成要点

- **桥接**：daemon → 各实例（bridgeUrl）→ zcode.z.ai；协议 Anthropic
- **流式修复**：`internal/zcode/zcode.go` Stream 转换器支持 thinking_delta/text_delta
- **额度显示**：桥接模式查实例 `/quota`（token 数），实例 billing 正常（带全头）
- **并发/隔离**：`betterThan` 最短到期优先；官方客户端独占的号在面板停用
  （zcode-fuka.json enabled=false 当前：副卡gmail 已改回启用？以面板为准）
- **3007→ErrClient**（勿回退 SessionDead）；405/3012→ErrClient（不禁用）

## 7. 自助加号（面板，三种模式）

| 渠道 | 方式 | 后端 |
|---|---|---|
| ZCode | 网页授权 + zai/bigmodel 选择 | provision.go（实例编排，18086 起分配端口） |
| AutoClaw | 手机号+短信验证码表单 | agent-send-code / agent-login（签名头复用） |
| WBIntl/CodeBuddy | 网页授权（StartIntl/PollIntl）+ 粘贴 token 兜底 | workbuddy.ai /v2/plugin/auth/* |

## 8. 速查

```bash
# 重启单个实例
bash /d/wild-work/research/zcode-proxy/instances/acc-<名>/start.sh &
# 重启 daemon（含桥接环境）
cd /d/wild-work/src-true && taskkill //IM daemon.exe //F; sleep 1
ZCODE_BRIDGE_URL=http://127.0.0.1:18083 ZCODE_BRIDGE_KEY=sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4 \
  nohup ./daemon.exe --no-tray > /dev/null 2>&1 &
# 查各号额度（token）
for port in 18083 18084 18085 18086; do curl -s http://127.0.0.1:$port/quota \
  -H "Authorization: Bearer sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4"; done
# 手动领取（指定实例）
ZCODE_CREDENTIALS_DIR=<creds目录> HTTPS_PROXY= HTTP_PROXY= ALL_PROXY= \
  ./zcode-proxy.exe claim now
```

## 9. 待办/未验证

- 副卡gmail（另一台电脑）反代：实例 acc-fuka2/18086 已建，auths 占位已写——
  需用户在浏览器切副卡 gmail 授权一次（注意账号别再拿错）
- 副卡bigmodel 下周五 19:00 自动重试投放
- ZCode 网页版 OAuth 深层逆向（web IDE bundle 的 SSO 端点）——可选工程
- proxyApiKey `sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4` 出现过在 AI 对话中——
  介意可轮换（改各实例 config.yaml + restart.bat）
