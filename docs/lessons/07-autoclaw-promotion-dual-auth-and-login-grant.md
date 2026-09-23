# 07 - AutoClaw 登录礼（promotion-reward）双鉴权头契约与领取机制复盘

> 2026-09-22 凌晨。起因：用户反馈"积分加油站当天有效、手动登录领到 +3 万，但 daemon 没领到，说明接口不对"。

## 1. 活动体系全景（promotion-config 的 modals）

登录礼不在 task-complete 体系（task-list 里只有 daily_signin 等 4 个任务），而在
`GET /autoclaw-proxy/proxy/autoclaw-promotion-config` 返回的 `data.modals` 弹窗列表里：

| modal_id | 内容 | 窗口换算（UTC+8） |
|---|---|---|
| `Token_Rush_M9W4_2` | **周一登录领 2 亿 / 周二登录领 3 亿 tokens** | 09-22 00:00 ~ 09-24 23:59，intervalDays=2 |
| `M9W3`（积分加油站第二期） | 登录领 2 亿 tokens（周一至周三），付费周四至周六再得 8 千万 | 09-12 ~ 09-14 |
| `0907_credits_station` | 前一期积分加油站（每日 5000-10000 积分） | 已过期 |

关键误解澄清：**"领取"是"登录行为触达"语义**——modal 是 `btnType:"link"`（按钮跳活动页），
客户端在**会话发消息**（`sendReward` 事件）或**定时弹窗确认**（`timingReward`）时才调
`grantPromotionReward` 落账。每天首次打开客户端 = 触达 = 服务端把当日份额标记为可领，
之后任意一次 promotion-reward 调用（无论从客户端还是 API）都会把份额落进钱包。

## 2. 本体复盘：为什么 daemon 领不到（核心坑：双鉴权头）

客户端 `app.asar` 逆向结论（`grantPromotionReward` → `getNewbieGuideAuthHeaders`）：

```js
headers.authorization = "Bearer " + token;      // commonHeaders 里带
headers["X-Authorization"] = "Bearer " + token; // extraHeaders 再带一次
```

**promotion-reward 必须"双头齐备"**：
- 只有 `authorization`（daemon 旧姿势）→ HTTP 错误 `{"message":"invalid request params"}`（非 200）
- 只有 `X-Authorization` → `{"error":"Invalid token"}`
- **双头都有 → HTTP 200 + `{code:0,data:{points:N,success:true}}` 信封**

而 promotion-config（读配置）单 `authorization` 就能过——同模块两个端点鉴权头要求不同，
这是照抄 config 头姿势到 reward 端点时踩的坑。

## 3. 已落地的修复（internal/autoclaw/autoclaw.go）

1. `promotionConfigHeaders` 增加 `X-Authorization` 头（config/reward 共用，config 不受影响）。
2. `claimPromotionReward` 非 200 响应改为记日志（原为完全静默，导致排查无据）。
3. `points=0 + success=true` 视为幂等已领，单独记日志行，不误报成功。
4. 领取日志补 `uid`（原来恒为 `?`，多账号无法归因）。

验证：09-22 00:42，5424 号 `daily_signin pts=200 done=true` + 活动扫描全部走通。

## 4. 积分"当天有效"的真相

`GET /agent-assetmgr/api/v1/points/expiring?biz_app_id=autoclaw` 实测：

```json
{"data":{"expiring_points":33618,"total_points":34018,
         "expire_time_value":3,"expire_time_type":"day"}}
```

**3 天滚动过期**，非自然日清零。1a09 余额 31160→10280 的"暴跌"是旧积分批量到期，
不是丢失。哨兵观察余额时必须同时看 expiring，否则会误判。

## 5. 观察与运维要点

- 面板「本地桥」外部托管组的 AutoClaw 客户端绿灯 = 登录触达在岗，timingReward 类份额靠它。
- daemon 每天 checkin 窗口自动做活动扫描；活动刷新（如 Token_Rush intervalDays=2 轮换）在
  窗口起点后任意时间点出现，扫描幂等可补领。
- 观察哨计划任务（00:05 主哨 / 07:30 副哨）结果落盘 `data/autoclaw-3e-test.log`，
  确认领取或连续两天无变化后应删除。
- `_probe/probe_asar_*.py` 与 `_probe/probe_modal_windows.py` 可复跑复查契约。

## 6. 最终决议（2026-09-22 收网）

用户决策：**放弃逆向跟进活动接口，活动份额手动登录领取；自动化底线 = 固定签到必须稳定。**
理由：活动接口每次活动都在变（modal_id / 奖励类型 / 鉴权细节），单号收益低，逆向成本不划算；
只有多号规模化时才值得重新投入。

已按此收网：
- 两个观察哨计划任务（00:05 主哨 / 07:30 副哨）已删除，`data/autoclaw-3e-test.log` 停止更新。
- daemon 每日 checkin 窗口里的活动扫描保留（顺带领固定类份额，领不到不影响签到）。
- 签到可靠性核验通过：调度 `checkin_times ["00:01","21:00"]` 双保险 + 签到前 2h 阈值
  刷 token + session dead 自动重试一次 + 21:00 兜底补签；失败走日志与面板告警。

## 7. 双号同日封禁事件复盘（2026-09-23）

> 现象：用户反馈"昨天还能用，今天调用流水都看不到"。诊断发现两号两种死法叠加，且暴露了 daemon 两处自愈盲区。

### 7.1 事件时间线

- 09-22 21:37 用户从面板手动停用 5424 → 当晚 keepalive 22:00 跳过（`reason=disabled`）→ 09-23 00:01 签到跳过
- 09-23 10:56 起 1a09 聊天接口开始返回 403 `{"code":410004,"message":"账号已被封禁"}`（钱包/签到接口仍正常）
- 09-23 12:20 用户重新启用 5424 → 聊天 401 `Invalid token`（停用期间 token 被上游吊销，auth 文件 expiresAt 却显示 10 月中旬——文件时间与上游真实状态脱节）
- 12:24-12:29 聊天忙窗空转 270s 放弃；签到也 401 失败且**没有触发自愈刷新**

### 7.2 排障与救援

1. 写 `_probe/probe_refresh_5424.py` 直测 refresh 端点（`POST /userapi/v1/refresh`，
   注意不是 `/agent-userapi/...`）：refresh_token 仍有效，HTTP 200 换新 token。
2. **refresh_token 会轮换**（rotated: True），但同一旧 RT 实测允许复用一次——第一次脚本
   忘了落盘，第二次重跑成功救回。教训：**探针脚本必须先备份再落盘，成功即写文件**。
3. 新 token 写回 `auths/autoclaw-5424….json`（嵌套 `auth.accessToken/refreshToken`，备份 .bak2）
   → 重启 daemon 生效 → 签到成功 +200 分（余额 1800→2000）。
4. token 修复后真相浮出：**5424 聊天也返回 410004 封禁**（此前 401 掩盖了封号状态）。
   结论：平台侧批量封号，签到/余额接口不受影响，聊天通道全灭，daemon 无法自救。

### 7.3 暴露的两处 daemon 盲区（已修复）

**盲区 1：claimTask 裸错误导致签到自愈失效**（`internal/autoclaw/autoclaw.go`）

调度器 `isSessionDead()` 靠 `errors.As(err, &ue)` 识别 `*provider.Error` 才会触发
"刷 token → 重签"自愈链（scheduler.go）。但 `claimTask` 把 401 包成
`fmt.Errorf("task-complete http %d: %s")` 裸错误——分类信息丢失，自永不触发。
同批修复 `RefreshToken`（http 非 200）和 `scopeBalance`（scope http）两处同类裸错误，
统一返回 `&provider.Error{Kind: Classify(...), Status: ..., Msg: ...}`。
消费方（handler refresh 分支 / RunKeepaliveNow）均已确认兼容：refresh 401 现在会正确
自动禁用死号，网络错误仍走 Cooldown。

**规则：上游 HTTP ≥400 一律包装 `*provider.Error`，禁止裸 `fmt.Errorf` 丢分类。**

**盲区 2：410004 封禁被当忙窗空转**（`isFailoverable`）

原逻辑把所有 403 当"忙窗/额度"重试，封禁号在忙窗里空转 270 秒才放弃。
修复：403 且 body 含 `410004`/`封禁` → 不可重试立即回报。验证：请求 0.3s 失败
（原 270s），每号只打 1 次 attempt。

### 7.4 后续观察点

- 两号积分/签到功能正常（1a09 remain=6974，5424 remain=2000），封号疑似只封聊天通道；
  每日签到继续跑，若解封聊天即自动恢复。
- 若平台后续对签到接口也封禁，修复后的 `*provider.Error` 链路会正确触发刷新/禁用并告警。
