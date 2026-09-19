# ZCode 渠道风控与领取机制经验复盘

> 归档自 AGENTS.md 历史排查记录（2026-09-17~18）。

---

## 1. 阿里云 WAF 风控（3012）机制

- `HTTP 405 + {"code":3012,"msg":"request has been blocked due to unusual activity."}` = **账号级行为标记**，不是封号（封号是 401/403 凭证失效）。
- **触发源**：高频自动 claim/领取循环、验证码求解失败死循环、高频探测（额度刷新每 60s 打一次 3012 会加剧风控）。
- **恢复策略**：**该账号停止一切上游请求** 24-72h（顽固者 1 周+）；官方客户端正常活动有助恢复；换 IP/设备/客户端形态**无效**（标记绑定账号维度）。
- **铁律**：失败绝不循环重试；验证一律单次；自动 claim 保持单次低频。

---

## 2. plan 与通道区分

| plan | 端点 | 凭证 | 额度 |
|---|---|---|---|
| **start-plan** | `zcode.z.ai/api/v1/zcode-plan/*` | JWT（Bearer） | **额度卡片（1 亿 Global Build 等）在这里** |
| coding-plan | `api.z.ai`（直连） | API Key | 国际站登录解析出的 API Key 属于它，通常无额度 |

- 额度卡片是 start-plan 资源 → 用 API Key / coding-plan 通道请求会 `429/1113 余额不足`（不是号废了，是 plan 配错）。
- LLM 端点：`/api/v1/zcode-plan/anthropic/v1/messages`（Anthropic 协议，强制 stream）。

---

## 3. 请求指纹（ZCode 3.12.3 形态）

- LLM 头族：`HTTP-Referer`、`User-Agent: ZCode/{ver} ai-sdk/anthropic/3.0.81`、`X-ZCode-App-Version`、`X-Title: Z Code@cli`、`X-ZCode-Agent: glm`、`X-Platform`、`X-Release-Channel`、`X-Client-Language/Timezone`、`X-Os-Category/Version`。
- **LLM 请求永不带 `X-Device-Mid`**（那是 control-plane/billing 的 HRt 头族）。
- `x-zcode-session-type` 只能 `main`/`subagent`/`other`。
- billing 对 API 形态很严，正常账号也可能 3012（不代表对话通道死）。
- 对话通道生死判据：无 VP 手打 LLM 端点 → `3007 captcha verify failed` = **通道活**（只差验证码）；`3012` = 账号被标。

---

## 4. 本机现役方案：zcode-proxy（TriDefender/zcode-api v4.6.6）

- 位置：`D:\wild-work\research\zcode-proxy\`（单文件 exe + config.yaml + 源码，MIT）。
- 验证码：**进程内 happy-dom 求解器**（无需浏览器）——captcha_node/jsdom 老方案解出的 VP 无效（3007），已弃用。
- 启动注意：必须清代理，HTTPS_PROXY 会走 Clash 被上游断连。
  `ZCODE_CLAIM_ENABLED=false HTTPS_PROXY= HTTP_PROXY= ALL_PROXY= ./zcode-proxy.exe --cli serve`
- 登录命令：`./zcode-proxy.exe auth login zai`（凭证存 `~/.zcode-proxy/credentials.json`）。

---

## 5. 周末包领取（Claim）机制与踩坑

- **投放规律**：周五开始投放 "ZCode Weekend Build"（GLM-5.3-Flash 300M token）；activates=15:00Z（**北京时间 23:00 生效**）。
- **接口**：`GET /api/v1/zcode-plan/billing/preview` + `POST /api/v1/zcode-plan/billing/claim`（body `{plan_id}`）。
- **3001 报错根因**：preview/claim **必须带完整身份头**（X-ZCode-App-Version、X-Title、X-ZCode-Agent 等），裸请求一律 3001 parameter error。
- **preview 语义**：`billing/preview` 只返回**未领**的包；返回空列表 = 已领过或当前无投放，不等于“不在名单”。
- **查询实际持仓**：查 `billing/balance` 的 `data.plans`。
