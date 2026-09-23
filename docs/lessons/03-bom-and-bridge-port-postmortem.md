# 2026-09-19 故障复盘：Windows UTF-8 BOM 与空壳占位符引发的级联冷却

---

## 1. 故障表现

客户端调用 `zcode/glm-5.3-flash` 报错：
```
502 all accounts/candidates unavailable (cooling/disabled): 
Post "http://127.0.0.1:18080/v1/chat/completions": dial tcp 127.0.0.1:18080: connectex: No connection could be made because the target machine actively refused it.
```

---

## 2. 根本原因链条

1. **Windows UTF-8 BOM 导致凭证文件全部漏载**：
   - 使用 PowerShell 脚本重写或用 Windows 工具编辑 `auths/zcode-*.json` 时，在文件开头静默写入了 `\ufeff`（UTF-8 BOM）。
   - Go 标准库 `encoding/json` 严格遵守 RFC 8259，解析时抛出 `invalid character '\ufeff' looking for beginning of value`。
   - `auth.LoadZCodeDir` 遇到错误静默忽略，导致网关装载的 ZCode 账号列表为空（0 个）。

2. **状态文件空壳占位符被调度**：
   - 账号池从 `data/state-zcode.json` 恢复历史额度数据时，构建了 `&auth.Auth{UID: uid}` 占位符。
   - 因为没有成功从 `auths/` 加载凭证，这些占位符的 `FilePath` 和 `BridgeURL` 均为 `""`。
   - 调度器 `pool.Pick()` 仅检查了 `disabled` 和 `until`，未检查凭证有效性，将空壳占位符分配给请求。

3. **回退至已废弃的 18080 端口引发级联冷却**：
   - 占位符 `BridgeURL` 为空，代码回退到全局兜底 `ZCODE_BRIDGE_URL`（旧配置写为已关停的 `18080`）。
   - 实际实例端口为 `18083~18086`，发往 18080 的连接被系统拒接。
   - 网关错误重试机制将池内所有 4 个账号轮换打挂，全部置入 10 分钟连续错误冷却，最终向上抛出 502 错误。

---

## 3. 防范与彻底修复

1. **JSON 解析器通用剥离 BOM**（[`internal/auth/auth.go`](file:///d:/wild-work/src-true/internal/auth/auth.go)）：
   ```go
   func Parse(raw []byte) (*Auth, error) {
       raw = bytes.TrimPrefix(raw, []byte("\xef\xbb\xbf"))
       // ...
   }
   ```
   所有 8 个渠道统一通过 `auth.Parse` 解析，全平台彻底免疫 Windows UTF-8 BOM。

2. **账号池占位符隔离**（[`internal/pool/pool.go`](file:///d:/wild-work/src-true/internal/pool/pool.go)）：
   - `entry` 增加 `placeholder: true` 标记（由 `pool.load()` 赋予，仅当收到真实 `Add` 或 `SyncToDir` 时解除）。
   - `healthy()` 判定：若 `e.placeholder == true` 则直接视为不健康，**绝对禁止将无真实凭证的空壳账号派发给请求**。

3. **启动对齐全量改用 SyncToDir**（[`cmd/zerogate/main.go`](file:///d:/wild-work/src-true/cmd/zerogate/main.go)）：
   - 启动时所有平台统一通过 `pool.SyncToDir(auths)` 初始化，磁盘上已被删除的陈旧账号直接从内存剔除。

4. **兜底端口修正**（[`internal/zcode/zcode.go`](file:///d:/wild-work/src-true/internal/zcode/zcode.go) 与 [`restart.bat`](file:///d:/wild-work/src-true/restart.bat)）：
   - 默认 BridgeURL 改为活跃的 18083 实例。
