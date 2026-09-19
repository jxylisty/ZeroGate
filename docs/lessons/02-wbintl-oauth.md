# WorkBuddy 国际版（WBIntl）网页登录协议复盘

> 归档自 AGENTS.md 历史记录（2026-09-18 实施）。

---

## 1. 协议流程（与国内版同构，仅换 Host 与 UA）

1. **发起登录**：
   - `POST https://www.workbuddy.ai/v2/plugin/auth/state?platform=CLI`
   - 返回 `{state, authUrl}`
2. **用户授权**：
   - 浏览器打开 `authUrl`（服务端跳转至 codebuddy.cn 统一登录页，正常现象）
3. **轮询 Token**：
   - 轮询 `GET /v2/plugin/auth/token?state=<state>`
   - pending 状态业务码为 `11217`；登录 TTL 600s
   - 成功后返回 `{accessToken, refreshToken, expiresIn}`
4. **获取用户信息**：
   - `GET /v2/plugin/login/account?state=<state>`（带 Bearer AccessToken）拿 `uid` 和 `nickname`

---

## 2. 请求头规范

- User-Agent：`WorkBuddy/5.5.2 WorkBuddy AI/5.5.2 CLI/5.5.2`
- Origin：`https://www.workbuddy.ai`
- 落盘位置：`auths/wbintl-<uid>.json`
