# 计划：修复「模型探针风暴 + 死渠道仍被挂牌」导致 qoder 整池不可用

## 一、现象与证据

- `POST /v1/chat/completions`，`model=qoder/qwen3.7-max` →
  `502 all accounts unavailable (cooling/disabled) after 8 candidates`（8 个候选全冷却/禁用）
- `/v1/models` 全量探针请求 → `500`
- 症状：模型列表里能点，点开必失败；越点越糟

## 二、根因（已定位到代码）

### R1 探针无请求合并，且恒定打同一个账号

- [handler.go](../internal/server/handler.go) L645-701 `fetchRuntimeModels`：缓存未命中即
  `acct := rt.Pool.Pick()` → `rt.Upstream.FetchModels(acct)`
- `Pool.Pick()` 是**确定性**挑选（[pool.go](../internal/pool/pool.go) L182 `betterThan`）——
  同一个渠道永远选中同一个账号
- 缓存参数：成功 1h / 失败 5min（handler.go L542-543）
- ⇒ 任何并发或高频 `/v1/models` 在缓存填充前会打出 **N 倍上游请求，且全部落在同一个号**上
  → 上游 429 / 10605 / 410004 → 该号冷却 → 反复几次整池不可用
- 探针失败路径（L675-686）只写 `rt.lastFail`、不惩罚账号，**但没有任何请求合并**，风暴真实存在

### R2 探测失败时用静态表冒充「活着」，死渠道继续挂牌

- L530-532：`infos = rt.StaticModels` 兜底
- L526-528：只跳过 `len(rt.Pool.List()) == 0` 的渠道，**不读 healthy**
- L542-553 `aliasAlive` 只判断「名单里有没有」，不判断账号是否可用
- ⇒ 账号全冷却/禁用时，该渠道的模型与 alias 仍出现在 `/v1/models`，客户端点开必 502

### R3 账号状态与模型可见性完全脱钩

- `Pool.Ban`（pool.go L274-292）：410004 一次即 24h 冷却，累计 3 次永久禁用
- 但 `modelList()` 从不读 `cooling / disabled` ⇒ 挂牌结果与实际可用性不一致

## 三、改动方案（4 项）

### 改动 1｜探针加请求合并 + 轮换 + 最小间隔

文件：`internal/server/handler.go`（L645-701）、`Runtime` 结构（L30-46）

- `Runtime` 增加 `probing bool` + `probingAt time.Time`（用现有 `rt.mu` 保护）
- 进入时若 `probing` 已置位 → 直接返回 `rt.models` 缓存，**不再发第二次上游请求**
- 探针选号改 `PickExcluding(tried)` 轮换，新增 `Runtime.probeRR atomic.Uint64`
- 新增常量 `modelsFetchMinInterval = 30 * time.Second` 作硬下限；失败退避沿用现有 5min

### 改动 2｜模型列表以「账号健康」为准，彻底隐藏死渠道

- `modelList()` 的 `liveByKind` 收集处（L518-548）增加：
  `healthy, total := rt.Pool.HealthyCount()`；`healthy == 0 && total > 0` → **该渠道不写入 liveByKind**
- `fetchRuntimeModels` 返回 nil 时，`StaticModels` 仅在「渠道健康但从未探测成功」时兜底，
  不再充当「活着」的依据
- `aliasAlive`（L542-553）增加渠道健康前置条件
- 效果：qoder 整池冷却 → `qoder/*` 及指向它的 alias 从 `/v1/models` 消失；
  上游恢复后自动回来（**不删配置**）

### 改动 3｜探针错误零惩罚（显式化 + 测试锁定）

- `fetchRuntimeModels` 不触碰池状态（现状如此），补注释写明这是**刻意**的
- 新增测试断言：探针返回 4xx/5xx 后账号的 `cooling / disabled / err_count` 全部不变

### 改动 4｜410004 与模型可见性联动

- handler.go L912 `case provider.ErrBanned` 之后，**立即清空该 rt 的探针缓存**
  （`rt.models = nil; rt.fetched = time.Time{}`），避免继续用旧名单挂牌
- 「累计 3 次自动禁用」策略保持不变

## 四、测试

新增 `internal/server/handler_models_test.go`：

| 测试 | 断言 |
|---|---|
| `TestModelListHidesChannelWhenAllAccountsCooling` | 账号全部 Cooldown → `/v1/models` 不含该渠道模型与 alias |
| `TestModelProbeSingleflight` | 并发 10 次 `/v1/models`，fake upstream 计数断言 `FetchModels` 调用 ≤ 2 |
| `TestModelProbeDoesNotPenalizeAccount` | 探针 500 后池状态零变化 |
| `TestModelProbeRotatesAccounts` | 连续两次探针命中不同 uid |

既有 `internal/qoder` 测试保持全绿。

## 五、验证步骤

1. `gofmt -l ./...` 无输出
2. `go build ./...`
3. `go vet ./...`
4. `go test ./internal/server/ ./internal/pool/ ./internal/qoder/`
5. **不重启 daemon**（红线）；需要真机验证时先征求同意
6. 全绿后单 commit 提交（conventional commit + 中文正文写清现象/根因/实证）

## 六、风险与回滚

- **风险 1**：`healthy == 0` 隐藏模型会让面板「看起来少了模型」——属预期（本来点开就 502），上游恢复自动回来
- **风险 2**：`probing` 若在持锁期间发上游请求会阻塞调度 —— 实现采用「置位 → 释放锁 → 请求 → 写回」
- **风险 3**：最小间隔 30s 可能让首启后短暂列表为空 —— 用 StaticModels 仅作首启兜底规避
- **回滚**：单 commit，`git revert` 即可

## 七、待确认

1. 探针最小间隔取 **30s** 是否合适？（现状为 0，仅靠 1h 成功缓存 + 5min 失败退避）
2. 面板是否要给「已冷却 / 已禁用」的账号加显式标记？（现有「已停用」标记可直接复用）