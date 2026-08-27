# 修复 DeepSeek 忙时 cache 扣款不一致（issue #101）

## 1. 背景

线上收到 [ai-gateway-api#101](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/101) 反馈：同一 DeepSeek 流量在 DeepSeek 控制台扣款约 ¥0.09，而 AI Gateway（BFE）后台扣款 ¥0.2245，差额约 ¥0.13。

经过对账，三次请求的关键数据如下：

| 指标 | R1 | R2 | R3 | 合计 |
|------|------|------|------|------|
| `prompt_tokens` | 33,023 | 35,867 | 36,584 | 105,474 |
| — 其中 cache hit | 13,696 | 34,048 | 36,352 | 84,096 (79.7%) |
| — 其中 cache miss | 19,327 | 1,819 | 232 | 21,378 |
| `completion_tokens` | 1,065 | 497 | 1,149 | 2,711 |
| BFE 扣款（¥） | 0.071371 | 0.074219 | 0.078913 | **0.224503** |

问题核心：**BFE 把 84,096 个 cache-hit token 按输入全价计费**。本方案聚焦在 BFE 侧让 DeepSeek 的 cache 命中字段能被正确识别并写入 `TokenUsage.CacheReadTokens`，后续 cache 拆分计费、忙时 tier 生效等由既有逻辑或配置链路负责。

---

## 2. BFE 侧根因分析

### 2.1 Usage 解析层不认识 DeepSeek 的 cache 字段

当前 BFE 在三个地方解析 usage：

- 非流式：`bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go:114-168` 的 `UpdateCtxByUsage`
- 流式：`bfe_modules/mod_body_process/llm_util.go:123-172` 的 `SSEEvent.GetQuotaUsage`
- 非流式（`RawEvent`）：`bfe_modules/mod_body_process/body_process.go:422-471` 的 `RawEvent.GetQuotaUsage`

它们目前只识别：

- `usage.cache_read_tokens` / `usage.cache_write_tokens`
- Claude 兼容字段：`usage.cache_read_input_tokens` / `usage.cache_creation_input_tokens`

DeepSeek 实际返回的是：

- `usage.prompt_cache_hit_tokens`
- `usage.prompt_tokens_details.cached_tokens`

由于这两个字段没有被解析，`TokenUsage.CacheReadTokens` 始终为 0，后续计费时 cache hit token 被全额计入普通 input。

### 2.2 其他根因（不在本次 BFE 代码修复范围内）

- **`cache_read_input_token_cost = 0` 与“未配置”无法区分**：需要 `model_prices` 配置侧显式、正确地维护 DeepSeek cache 单价。
- **忙时 tier 未生效**：需要控制面 / `conf-agent` 把 provider 的 `tiers` 正确拼入 cluster 的 `ModelTable.Tiers`。

本方案只解决 **2.1**，让 BFE 拿到正确的 `CacheReadTokens`，为 2.2 的正确计费提供前提数据。

---

## 3. 变更目标

1. **识别 DeepSeek cache 字段**：让非流式、流式、RawEvent 三处 usage 解析都支持 `usage.prompt_cache_hit_tokens` 和 `usage.prompt_tokens_details.cached_tokens`。

---

## 4. 变更总览

| 模块 | 主要改动 |
|------|----------|
| `mod_ai_token_auth` | `UpdateCtxByUsage` 解析 DeepSeek cache 字段 |
| `mod_body_process` | `SSEEvent.GetQuotaUsage` / `RawEvent.GetQuotaUsage` 解析 DeepSeek cache 字段 |
| 测试 | 补充 DeepSeek cache 字段解析的单元测试与集成测试 |

---

## 5. 详细设计

### 5.1 解析 DeepSeek cache 字段

在三处 usage 解析中，保持现有字段优先，当 `cacheRead == 0` 时再尝试 DeepSeek 字段：

```go
// 1. 现有字段
cacheRead = gjson.GetBytes(data, "usage.cache_read_tokens").Int()

// 2. DeepSeek 字段兜底
if cacheRead == 0 {
    cacheRead = gjson.GetBytes(data, "usage.prompt_cache_hit_tokens").Int()
}
if cacheRead == 0 {
    cacheRead = gjson.GetBytes(data, "usage.prompt_tokens_details.cached_tokens").Int()
}

// 3. Claude 字段兜底（保留现有逻辑）
if cacheRead == 0 {
    cacheRead = gjson.GetBytes(data, "usage.cache_read_input_tokens").Int()
}
```

需要修改的文件：

- `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go`：`UpdateCtxByUsage`
- `bfe/bfe_modules/mod_body_process/llm_util.go`：`SSEEvent.GetQuotaUsage`
- `bfe/bfe_modules/mod_body_process/body_process.go`：`RawEvent.GetQuotaUsage`

### 5.2 字段优先级与兼容性

- 若后端同时返回 `cache_read_tokens` 和 `prompt_cache_hit_tokens`，以非零的现有字段为准，避免重复计算。
- 新增字段仅影响 `CacheReadTokens` 的取值，不影响 `PromptTokens`、`CompletionTokens`、`UsedQuota` 等既有字段。
- 后端未返回任何 cache 字段时，行为与现在完全一致。

---

## 6. 计费公式（依赖既有 cache 拆分逻辑）

当 `CacheReadTokens` 被正确填充后，BFE 现有 `calcChatCost` 会按以下公式计费：

```
cacheRead = min(CacheReadTokens, PromptTokens)
normalInput = PromptTokens - cacheRead

cost = normalInput × input_cost_per_token
     + cacheRead × cache_read_input_token_cost
     + CompletionTokens × output_cost_per_token
```

> 注：本方案不修改计费公式本身，只保证 `CacheReadTokens` 来自 DeepSeek 的真实返回。

---

## 7. 边界情况

| 场景 | 处理 |
|------|------|
| 后端返回 `usage.prompt_cache_hit_tokens` | 解析为 `CacheReadTokens` |
| 后端返回 `usage.prompt_tokens_details.cached_tokens` | 解析为 `CacheReadTokens` |
| 同时返回多个 cache 字段 | 以第一个非零值为准 |
| 后端未返回任何 cache 字段 | `CacheReadTokens = 0`，行为不变 |
| `CacheReadTokens > PromptTokens` | 由 `calcChatCost` 截断为 `PromptTokens` |
| 流式响应中 cache usage 在最后一个 chunk | 最后一个非 guess 事件覆盖，与现有逻辑一致 |

---

## 8. 测试计划

### 8.1 单元测试

#### `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth_test.go`

1. `TestUpdateCtxByUsage_DeepSeekCache`：验证 `usage.prompt_cache_hit_tokens` 被正确解析为 `CacheReadTokens`。
2. `TestUpdateCtxByUsage_DeepSeekCacheDetails`：验证 `usage.prompt_tokens_details.cached_tokens` 被正确解析为 `CacheReadTokens`。
3. `TestUpdateCtxByUsage_DeepSeekCachePriority`：多个字段同时存在时，优先使用非零的现有字段。

#### `bfe/bfe_modules/mod_body_process/content_quota_usage_test.go`

4. `TestQuotaUsageProcessorProcessWithDeepSeekCache`：非流式 `RawEvent` 返回 `usage.prompt_cache_hit_tokens`，`TokenUsage.CacheReadTokens` 正确。
5. `TestQuotaUsageProcessorProcessWithDeepSeekCacheDetails`：非流式 `RawEvent` 返回 `usage.prompt_tokens_details.cached_tokens`，`TokenUsage.CacheReadTokens` 正确。

#### `bfe/bfe_modules/mod_body_process/llm_util_test.go`（如不存在则新增）

6. `TestSSEEventGetQuotaUsage_DeepSeekCache`：SSE 最终 chunk 含 `usage.prompt_cache_hit_tokens`，`CacheReadTokens` 正确。
7. `TestSSEEventGetQuotaUsage_DeepSeekCacheDetails`：SSE 最终 chunk 含 `usage.prompt_tokens_details.cached_tokens`，`CacheReadTokens` 正确。

### 8.2 集成测试

在 `bfe/tests/integration/implementation/scenario-SC09-rmb-tiered-pricing/` 中新增：

1. `TestTC05_RMBQuotaDeduction_DeepSeekCacheField_NonStreaming`：
   - mock 后端返回 `usage.prompt_tokens_details.cached_tokens`；
   - `ModelTable` 配置 `cache_read_input_token_cost`；
   - 验证 Redis 按 cache 拆分公式扣减。

2. `TestTC06_RMBQuotaDeduction_DeepSeekCacheField_Streaming`：
   - 与 TC05 相同，但响应为 SSE 流；
   - 验证流式场景下 DeepSeek cache 字段同样生效。

在 `bfe/tests/integration/测试设计文档/scenario-SC09-RMB分时段计费/` 中补充对应 `TC-05`、`TC-06` 设计文档。

---

## 9. 影响范围

| 文件 | 影响 |
|------|------|
| `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go` | 非流式 usage 解析增加 DeepSeek cache 字段 |
| `bfe/bfe_modules/mod_body_process/llm_util.go` | 流式 usage 解析增加 DeepSeek cache 字段 |
| `bfe/bfe_modules/mod_body_process/body_process.go` | `RawEvent` usage 解析增加 DeepSeek cache 字段 |
| 相关 `*_test.go` | 新增/补充测试 |
| `bfe/docs/zh_cn/modifications/2026-08-27-deepseek-busy-hour-billing-fix/design-changes.md` | 本设计文档 |

---

## 10. 兼容性与风险

### 10.1 兼容性

- 未返回 DeepSeek cache 字段的后端行为完全不变。
- 已有 `cache_read_tokens` / `cache_write_tokens` 字段的模型行为不变。
- Token 配额（`total_token`）扣减逻辑不变。
- Redis 定点数存储、key 结构不变。

### 10.2 风险与缓解

| 风险 | 缓解措施 |
|------|----------|
| DeepSeek 字段解析优先级与现有字段冲突 | 仅当现有字段为 0 时才 fallback，避免重复计费 |
| `prompt_tokens_details.cached_tokens` 为嵌套对象 | 使用 `gjson` 路径 `usage.prompt_tokens_details.cached_tokens` 直接取值 |
| 不同 DeepSeek 版本返回字段不一致 | 同时支持 `prompt_cache_hit_tokens` 和 `prompt_tokens_details.cached_tokens` |

---

## 11. 实施步骤建议

1. **Usage 解析**：在三处解析函数中增加 DeepSeek cache 字段兜底。
2. **单元测试**：补充 `UpdateCtxByUsage`、`SSEEvent.GetQuotaUsage`、`RawEvent.GetQuotaUsage` 的 DeepSeek 字段测试。
3. **集成测试**：在 SC09 中新增非流式/流式 DeepSeek cache 字段扣费验证。
4. **端到端验证**：重放 issue #101 中的请求，确认 `CacheReadTokens` 被正确填充；最终扣款是否匹配 DeepSeek 控制台还依赖 `model_prices` 与 provider tiers 的正确配置。

---

## 12. 参考资料

- [ai-gateway-api#101](https://github.com/rainway-ai-gateway/ai-gateway-api/issues/101)
- `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go`
- `bfe/bfe_modules/mod_body_process/llm_util.go`
- `bfe/bfe_modules/mod_body_process/body_process.go`
- `bfe/docs/zh_cn/modifications/2026-08-22-cache-billing-support/design-changes.md`
- `bfe/docs/zh_cn/modifications/2026-08-25-tiered-pricing-support/design-changes.md`
