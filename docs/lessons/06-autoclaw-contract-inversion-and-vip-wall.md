# 2026-09-21 AutoClaw：上游契约反转（harness 头失效 + banner 必需）与高峰期免费通道 VIP 拦截实测

> 现象：① daemon 的 AutoClaw 渠道所有请求超时转圈；② 用户在 AutoClaw 客户端用
> glm-5.3-flash 高峰期被反复要求"开通 VIP/连续包月"，体感"挤牙膏"。
> 触发排查：评估开源项目 github.com/eequaled/GLM_proxy 的 autoclaw 实现，
> 借用本仓库账号做交叉矩阵实测，意外发现上游协议契约已反转。

---

## 1. 结论一：上游契约反转（今天早上还好的协议，下午就打不通）

**同一天内实测对比（2026-09-21）**，同一 token、同一域（`autoglm-acceleration-api.zhipuai.cn`）：

| 请求形态 | 结果 |
|---|---|
| ZeroGate 现行协议（`X-Harness-Type: zcode` + 无系统 banner） | **400 invalid request** |
| GLM_proxy 协议（无 harness 头 + 系统提示词 banner） | **200 出流** |
| ZeroGate 头去掉 harness + 加 banner | **200 出流**（修复方案验证通过） |

头字段级二分（从 200 的 GP 头逐个替换成 ZG 头字段）锁定两条契约：

1. **`X-Harness-Type: zcode` 从"豁免白名单"变成"直接 400"**。
   `autoclaw.go` 文件头与 `proxyHeaders` 注释里写的"缺失会 400 invalid request"
   是**旧契约**，现已反转：带上反而 400。
2. **系统提示词 banner 从"非必需"变成"必需"**。请求体第一条 system 消息必须是：
   ```
   You are a personal assistant running inside OpenClaw.\n## Tooling
   ```

其余头（X-Channel/X-Version/X-Lang/Accept/x_trace_id 等）两种取值均被容忍，不敏感。
⚠️ 老坑复现提醒：模型路由仍是**双标识**——`X-Request-Model` 发带前缀路由 ID
（如 `zai_glm-5.3-flash`），`body.model` 发剥前缀模型（`glm-5.3-flash`），
只发对其中一个照样 400「非法模型」。

**daemon 侧症状与根因的映射**：旧协议打上游必 400 → `ChatStream` 的忙窗自愈
把 400 当可重试错误，270 秒窗口内循环重试 → 客户端侧表现 = 请求挂到超时"装死"。
本日 15:42 走 daemon `/v1/chat/completions` 三种模型全部 120s 超时，与此一致。

## 2. 结论二：高峰期"要 VIP"是免费通道的日额度墙，积分通道不受影响

15:38~15:51（晚高峰）实测：

| 通道 | 结果 |
|---|---|
| `zai_auto-fast`（免费 fast 通道） | **403 code 810000**："Auto-Fast 模型免费额度已用完，24 小时后会进行重置，开通连续包月会员即可继续使用。" |
| `zai_auto`（积分自动选型）连发 3 次 | 全部 200，首响应 0.9s / 2.9s / 1.2s，零排队零忙窗 |
| `zai_glm-5.3-flash`（积分/会员通道，双标识） | 200，首响应 1.6s |

要点：

- **API 层没有"排队"机制**，免费额度耗尽就是硬拦截 403(810000)，文案直接推会员。
  客户端 UI 把这类拦截渲染成"排队中 + 开通 VIP 优先"，即用户看到的"挤牙膏急死人"。
  （客户端把 flash 档位的日常请求路由到免费桶 → 桶空 → 弹 VIP。）
- **积分计费通道与免费桶独立**：daemon 两个号积分充足（31,160 / 21,593，当日签到成功），
  高峰期走 `zai_auto` / `zai_glm-5.3-flash` 完全顺畅。**应对 = 让用户侧模型固定走积分
  路由，别落在 auto-fast 上**；免费桶等 24h 重置即可，不必充钱。
- code 810000（免费额度墙）与 810001（pay-view 忙窗）语义不同：前者是**日额度耗尽**，
  重试无意义（`isFailoverable` 未来可考虑对 810000 直接短路，不烧 270s 重试窗）。

## 3. 结论三：GLM_proxy（eequaled/GLM_proxy）评估——协议可用，域名不可达

- **协议本体正确**：无 harness 头 + AUTOCLAW_SYSTEM_BANNER + `X-Authorization`
  + `X-Agent-Id: main` + `X-Request-Model` 路由头。用本仓库账号 token 实测 200 出流。
- **致命点**：其 `lib/core.js` 硬编码上游 `https://autoglm-api.autoglm.ai/...`
  本机 **405 nginx 死主机页**（该域未挂代理路径）。要直接跑它，把 `UPSTREAM_BASE`
  换成 `https://autoglm-acceleration-api.zhipuai.cn/autoclaw-proxy/proxy/autoclaw` 即可。
- 它的本地 WS 回落路径有效：`127.0.0.1:18789` 桌面端网关在监听（实测连通）。
- token 来源差异：GLM_proxy 读 `~/.openclaw-autoclaw/request-headers.json` 的
  `headers["X-Authorization"]`（桌面端 token，实测也是 21593 积分、与 daemon 号 2 同账号）；
  本仓库读 `auths/autoclaw-*.json` 嵌套 `auth.accessToken`。两套 token 同域均可鉴权。

## 4. 修复落地（2026-09-21 16:40 完成，daemon 全量回归通过）

`internal/autoclaw/autoclaw.go` 实际改动（方案即上文验证的两点，另加一条短路）：

1. `proxyHeaders`：删除 `X-Harness-Type: zcode`，注释标注"契约反转，严禁加回"；
2. `ChatStream` 组包处：检测 messages 中无 banner 时，将 `AutoClawSystemBanner`
   作为**独立的第一条 system 消息前置**（不动客户端自带 system 内容；
   共存姿势实测：独立前置与拼接进首条 system 均 200，无 banner 400）；
3. 新增常量 `AutoClawSystemBanner`（文件头注释同步更新为新契约）；
4. 连带修复：上游 403 **810000**（免费桶日额度墙）在忙窗重试前**短路**——
   日级硬额度重试 270s 无意义，直接返回中文提示
   （"额度已用完，24 小时后重置；请改用积分通道模型"），
   客户端从挂死 120s+ 变为 **0.2s 秒回**明确原因。

**修复后 daemon 链路回归（16:44）**：
`autoclaw/zai_auto` → 200 PONG (1.2s)；`autoclaw/glm-5.3-flash` → 200 PONG (2.7s)；
`autoclaw/zai_auto-fast` → 502 秒回额度提示（0.2s，符合预期）。
`taskCompleteHeaders` 的反向经验仍有效：task-complete 接口**不能带** X-Client-Type /
X-Harness-Type——不同端点契约不同，别互相套用。

## 5. 顺带记录

- 复测工具：`_tmp/test_peak_probe.py`（余额+套餐+对话）、`test_channel_split.py`
  （免费桶 vs 积分桶）、`test_peak_burst.py`（高峰连发）、`test_header_bisect.py`
  （头字段二分）、`test_daemon_peak.py`（daemon 链路）。均不回显凭证。
- 账号 JWT 过期轮换很快（数小时级）：auths 里的 token 上午能用、下午 401 属正常，
  daemon 自己会 refresh（`/userapi/v1/refresh`）；外部脚本测试前先刷新或复用 daemon。
- 外部测试脚本读 daemon 状态需 API key（config.json），`/status` 结构为
  `{"accounts":{"autoclaw":[...credits/cooling/last_checkin...]}}`。
- 本日 09:00 两号签到均成功（reward_points 正常），签到/任务/积分查询链路未受协议反转影响
  ——**只有 chat/completions 的契约变了**，userapi 签名头（X-Auth-Sign）体系不受影响。
