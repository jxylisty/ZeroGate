# 2026-09-19 Qoder 通道：流式 token 计 0（嵌套 SSE 转义 usage 漏抓）与"积分不变"真相

> 现象：Trae 接入 qoder 通道长时间使用，调用流水 token 恒为 0、账号积分纹丝不动。

---

## 1. 两个独立结论

1. **积分不变是上游计量的事实，但"永久免费"结论下早了**：
   - 88 笔请求（84 笔 glm-5.3-flash，真实生成 5-13s/笔）后，上游配额接口
     `/api/v2/quota/usage` 仍报 `用户套餐 total=300 / used=0 / remain=300`（两个账号一致）；
   - 流内 usage 明确 `"billable":false, "credits":0`——上游实时计量也没扣。
   - 但用户记忆中 glm-5.3-flash 在 qoder 是 **0.1× 倍率计费**（限免的是 qwen 系：
     Qwen3.8-Flash 0.1×→0.0×、Qwen3.8-Max 新人限免、Efficient 档对付费用户 0×），
     官方公开文档（docs.qoder.com events 页）查不到 glm-5.3-flash 的现行倍率。
   - **存疑点**：可能 glm-5.3-flash 也在限免活动期、可能扣费结算滞后、也可能扣在
     我们没轮询的另一桶里。→ 结论有效期只到下一次复查；用 Qoder 官方客户端同账号
     的积分页做权威对照，或持续观察网关积分是否开始移动。
2. **流式 token 计 0 是网关 bug**（已修）：qoder 原始流为双层封装
   `data: {"body":"<转义的内层 JSON>"}`，usage 藏在转义字符串里。
   `tokenTrackingReader` 的正则 `"usage":\s*\{` 匹配不到 `\"usage\":\{`（引号前有反斜杠），
   且 usage 内含嵌套 details 对象，非贪婪正则会截断——必须完整 JSON 解析内层。
   非流式路径（`aggregate`）一直解析正确，所以只有流式丢计数。

---

## 2. 修复（internal/server/handler.go tokenTrackingReader.parseLine）

- `data:{"body":...}` 行：解析内层 JSON，取 `usage` 对象逐字段提取
  （`applyUsageMap`，嵌套 details 安全）。
- 原有两条路径（OpenAI 扁平 usage 行 / SOLO 原生 `prompt_tokens` 行）行为不变，
  提取逻辑收敛为 `applyField` 统一入口。
- 回归测试：`internal/server/handler_usage_scan_test.go`（四种流格式各一）。

## 3. 顺带记录

- qoder 上游限流特征：`403 + code 10605 + retryAfterSeconds`（并发排队），
  网关已转写为可重试 429 透传给客户端，客户端自动重试即可，不是故障。
- qoder usage 里的 `credits/original_credits/billable` 字段是上游的**实时计费回执**，
  可用于将来核对计费；当前网关未入库（如需按上游积分记账再加）。
- 官方公开文档查不到 glm-5.3-flash 现行倍率时，以两套上游计量（流内 usage 回执 +
  `/api/v2/quota/usage` 轮询）交叉验证为准，且结论要标注复查日期——计费策略随活动变。
