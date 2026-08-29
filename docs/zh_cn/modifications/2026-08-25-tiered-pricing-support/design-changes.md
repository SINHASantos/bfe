# BFE 支持分时段/分工作日计费（Tiered Pricing）

## 1. 背景

AI 网关接入的 DeepSeek 等模型已引入峰谷定价：

- **高峰时段**：北京时间周一至周五 `09:00-12:00`、`14:00-18:00`。
- **空闲时段**：其余时间。
- 空闲价格为高峰价格的一半。

当前 BFE 的 RMB 配额成本计算只支持固定的 `input_cost_per_token` / `output_cost_per_token`，无法根据请求发生时刻选择不同单价。同时 `ai-gateway-api` 已完成 provider 与 cluster 的概念分离，provider 级别的时段模板（`time_zone`、`tiers`）通过独立接口维护，模型级别的分时段价格（`tier_prices`）保留在 `/model-prices` 中；BFE 接收到的 `AIConf.ModelTable` 由控制面拼接后下发。

本变更在不破坏现有 RMB / total_token 配额逻辑的前提下，扩展 BFE 对分时段计费的端到端支持。

---

## 2. 需求示例

以 DeepSeek-V4-Pro 为例（每百万 tokens，单位：元）：

| 计费场景 | 高峰时段 | 空闲时段 |
|---------|---------|---------|
| 输入（缓存命中） | 0.30 | 0.15 |
| 输入（缓存未命中） | 9.00 | 4.50 |
| 输出 | 27.00 | 13.50 |

对应每 token 单价（元 / token）：

- 高峰输入（缓存未命中）：`9.0e-6`
- 空闲输入（缓存未命中）：`4.5e-6`
- 高峰缓存命中输入：`3.0e-7`
- 空闲缓存命中输入：`1.5e-7`

配置形态（由 `ai-gateway-api` 拼接后下发给 BFE）：

```json
{
    "AIConf": {
        "Provider": "deepseek",
        "ModelTable": {
            "Currency": "RMB",
            "TimeZone": "Asia/Shanghai",
            "Tiers": [
                {
                    "Name": "peak",
                    "TimeRanges": [
                        { "Weekdays": [1, 2, 3, 4, 5], "Start": "09:00", "End": "12:00" },
                        { "Weekdays": [1, 2, 3, 4, 5], "Start": "14:00", "End": "18:00" }
                    ]
                }
            ],
            "Models": [
                {
                    "Provider": "deepseek",
                    "Model": "deepseek-v4-pro",
                    "Mode": "chat",
                    "Prices": {
                        "input_cost_per_token": 4.5e-06,
                        "output_cost_per_token": 1.35e-05,
                        "cache_read_input_token_cost": 1.5e-07
                    },
                    "TierPrices": {
                        "peak": {
                            "input_cost_per_token": 9.0e-06,
                            "output_cost_per_token": 2.7e-05,
                            "cache_read_input_token_cost": 3.0e-07
                        }
                    }
                }
            ]
        }
    }
}
```

---

## 3. 当前现状

| 层级 | 当前能力 | 不足 |
|---|---|---|
| 数据结构 | `bfe_basic.TokenUsage` 已含 `CacheReadTokens` / `CacheWriteTokens` 等缓存字段 | 缺少分时段计费相关字段（本次复用现有缓存字段） |
| 价格配置 | `ModelPrice.Prices` 仅识别固定价格字段 | 无法按请求时刻选择不同价格；缺少 `TierPrices` |
| 时段配置 | `ModelTable` 无 `TimeZone`、`Tiers` 概念 | 无法定义高峰/空闲时段 |
| Usage 解析 | `UpdateCtxByUsage` 已解析缓存命中 token | 不感知请求发生时刻，无法按时段选择价格 |
| 费用计算 | `calcCostUnits` 已支持缓存拆分计费 | 无法支持分时段计费 |

---

## 4. 变更目标

1. 在 `ModelTable` 中支持 `TimeZone` 与 `Tiers` 时段定义。
2. 在 `ModelPrice` 中支持 `TierPrices` 分时段价格表。
3. 复用现有 `TokenUsage.CacheReadTokens` 字段，支持缓存命中价格按 tier 生效。
4. 改造 `calcCostUnits`，实现按请求时刻匹配 tier 并取价。
5. 未配置 `Tiers` / `TierPrices` 时退化到原有固定价格逻辑，保持向后兼容。
6. **初期约束**：tier name 只支持 `peak`，后续按需扩展。

---

## 5. 变更总览

| 模块 | 主要改动 |
|---|---|
| `bfe_config/bfe_cluster_conf/cluster_conf` | 新增 `TimeRange`、`PriceTier` 结构；扩展 `ModelTable`、`ModelPrice`；`ModelTableCheck` 增加时段校验与 tier 价格定点转换 |
| `bfe_basic` | 复用现有 `TokenUsage.CacheReadTokens` |
| `mod_ai_token_auth` | `calcCostUnits` 按活跃 tier 与缓存命中价格计费 |
| 测试 | 补充时段匹配、缓存计费、固定价格退化等单元测试 |

---

## 6. 详细设计

### 6.1 扩展 `ModelTable` / `ModelPrice`

**文件：** `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go`

新增数据结构：

```go
type TimeRange struct {
    Weekdays []int  // 0=周日, 1=周一 ... 6=周六；为空表示每天
    Start    string // "HH:MM"
    End      string // "HH:MM"，必须 > Start；跨午夜请拆成两段
}

type PriceTier struct {
    Name       string      // 初期只支持 "peak"
    TimeRanges []TimeRange // 命中任意一个即属于该 Tier
}

type ModelPrice struct {
    Provider            string
    Model               string
    BaseModel           string
    Mode                string
    Capabilities        []string
    SupportedParameters []string
    Limits              map[string]interface{}
    Prices              map[string]float64            // 默认价格
    TierPrices          map[string]map[string]float64 // tier name -> 价格表
    Metadata            map[string]interface{}

    // 运行时字段：配置加载阶段预计算定点整数
    pricesInt     map[string]int64
    tierPricesInt map[string]map[string]int64
}

type ModelTable struct {
    Currency string
    TimeZone string       // 默认 "Asia/Shanghai"
    Tiers    []PriceTier  // 时段定义
    Models   []ModelPrice

    priceIndex map[string]map[string]*ModelPrice
    tierIndex  map[string]*PriceTier
    tz         *time.Location
}
```

`ModelTableCheck` 增强：

1. 解析 `TimeZone`，默认 `Asia/Shanghai`。
2. 校验 `Tiers`：名字非空、**初期 `name` 只支持 `peak`**、时间格式正确、`Weekdays` 合法、同一 Tier 内时间不重叠。
3. 将默认 `Prices` 和每个 tier 的价格表分别转成定点整数；`TierPrices` 中的 tier name 不强制要求在 `Tiers` 中已定义（保持与 ai-gateway-api 的松耦合），但**初期 `TierPrices` 的键名只支持 `peak`**。

示例校验逻辑：

```go
func ModelTableCheck(table *ModelTable) error {
    if table.Currency != quota.UnitRMB {
        return fmt.Errorf("currency must be %s", quota.UnitRMB)
    }
    if table.TimeZone == "" {
        table.TimeZone = "Asia/Shanghai"
    }
    loc, err := time.LoadLocation(table.TimeZone)
    if err != nil {
        return fmt.Errorf("invalid TimeZone %s: %v", table.TimeZone, err)
    }
    table.tz = loc

    table.tierIndex = make(map[string]*PriceTier)
    for i := range table.Tiers {
        tier := &table.Tiers[i]
        if tier.Name == "" {
            return errors.New("tier name is empty")
        }
        if tier.Name != "peak" {
            return fmt.Errorf("unsupported tier name %s, only 'peak' is allowed", tier.Name)
        }
        if err := validateTimeRanges(tier.TimeRanges); err != nil {
            return fmt.Errorf("tier %s: %v", tier.Name, err)
        }
        table.tierIndex[tier.Name] = tier
    }

    table.priceIndex = make(map[string]map[string]*ModelPrice)
    for i := range table.Models {
        price := &table.Models[i]
        if price.Model == "" || price.Mode == "" {
            return errors.New("model/mode is empty")
        }

        price.pricesInt = make(map[string]int64)
        for key, val := range price.Prices {
            if val < 0 {
                return fmt.Errorf("negative price %s for model %s", key, price.Model)
            }
            price.pricesInt[key] = quota.RmbToFixedPoint(val)
        }

        price.tierPricesInt = make(map[string]map[string]int64)
        for tierName, tierPriceMap := range price.TierPrices {
            if tierName != "peak" {
                return fmt.Errorf("unsupported tier name %s in TierPrices for model %s, only 'peak' is allowed", tierName, price.Model)
            }
            intMap := make(map[string]int64)
            for key, val := range tierPriceMap {
                if val < 0 {
                    return fmt.Errorf("negative tier price %s for model %s tier %s", key, price.Model, tierName)
                }
                intMap[key] = quota.RmbToFixedPoint(val)
            }
            price.tierPricesInt[tierName] = intMap
        }

        if table.priceIndex[price.Model] == nil {
            table.priceIndex[price.Model] = make(map[string]*ModelPrice)
        }
        if table.priceIndex[price.Model][price.Mode] != nil {
            return fmt.Errorf("duplicate model %s mode %s", price.Model, price.Mode)
        }
        table.priceIndex[price.Model][price.Mode] = price
    }
    return nil
}
```

### 6.2 复用现有 `TokenUsage` 缓存字段

**文件：** `bfe/bfe_basic/request_ai_basic.go`

当前 `TokenUsage` 已包含 `CacheReadTokens` / `CacheWriteTokens` 字段，本次变更直接复用，无需再扩展：

```go
type TokenUsage struct {
    PromptTokens      int64 // 请求侧 Token 数（含 CacheReadTokens）
    CompletionTokens  int64 // 响应侧 Token 数
    CacheReadTokens   int64 // 命中缓存的输入 Token 数
    CacheWriteTokens  int64 // 缓存写入 Token 数
    // ... 其他字段
    UsedQuota         int64 // 已用 Token 配额（unit=total_token 时使用）
    UsedCost          int64 // 已用 RMB 成本，1 单位 = 1e-8 元（unit=RMB 时使用）
}
```

> 说明：变更总览中列出的 `bfe_basic` 改动为“复用”而非“新增”。

### 6.3 Usage 解析

**文件：** `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go`

当前 `UpdateCtxByUsage` 已解析缓存命中 token（`usage.cache_read_tokens` / `usage.cache_read_input_tokens` 等），本次变更无需再改。

### 6.4 运行时时段匹配

**文件：** `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go`

```go
func (table *ModelTable) ActiveTierName(now time.Time) string {
    if table == nil || len(table.Tiers) == 0 {
        return ""
    }
    t := now.In(table.tz)
    wd := int(t.Weekday())
    hour, min := t.Hour(), t.Minute()
    cur := hour*60 + min

    for i := range table.Tiers {
        tier := &table.Tiers[i]
        for _, tr := range tier.TimeRanges {
            if len(tr.Weekdays) > 0 && !containsInt(tr.Weekdays, wd) {
                continue
            }
            start := parseHHMM(tr.Start)
            end := parseHHMM(tr.End)
            if start <= cur && cur < end {
                return tier.Name
            }
        }
    }
    return ""
}

func (p *ModelPrice) GetPriceInt(tier, key string) int64 {
    if tier != "" && p.tierPricesInt != nil {
        if tierMap, ok := p.tierPricesInt[tier]; ok {
            if v, ok := tierMap[key]; ok {
                return v
            }
        }
    }
    if p.pricesInt != nil {
        return p.pricesInt[key]
    }
    return 0
}
```

### 6.5 RMB 费用计算改造

**文件：** `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go`

函数 `calcCostUnits` 根据当前时间匹配活跃 tier，再取对应价格；同时支持缓存命中 / 未命中分层：

```go
func (m *ModuleAITokenAuth) calcCostUnits(req *bfe_basic.Request, serverConf bfe_basic.ServerDataConfInterface, promptTokens, completionTokens int64) int64 {
    aiMeta := req.GetAiBasicInfo()
    if aiMeta == nil {
        return 0
    }
    clusterName := req.Route.ClusterName
    targetModel := aiMeta.TargetModel
    if clusterName == "" || targetModel == "" {
        return 0
    }
    if serverConf == nil {
        return 0
    }
    cluster, err := serverConf.ClusterTableLookup(clusterName)
    if err != nil || cluster == nil || cluster.AIConf == nil || cluster.AIConf.ModelTable == nil {
        log.Logger.Warn("model table not found for cluster %s", clusterName)
        return 0
    }

    entry := cluster_conf.LookupModelPrice(cluster.AIConf.ModelTable, targetModel, "chat")
    if entry == nil {
        log.Logger.Warn("model price not found for cluster %s model %s", clusterName, targetModel)
        return 0
    }

    tierName := cluster.AIConf.ModelTable.ActiveTierName(time.Now())

    cacheReadTokens := aiMeta.GetTokenUsage().CacheReadTokens
    if cacheReadTokens > promptTokens {
        cacheReadTokens = promptTokens
    }
    uncachedTokens := promptTokens - cacheReadTokens

    inputCost := entry.GetPriceInt(tierName, "input_cost_per_token")
    outputCost := entry.GetPriceInt(tierName, "output_cost_per_token")
    cacheReadCost := entry.GetPriceInt(tierName, "cache_read_input_token_cost")

    if inputCost < 0 || outputCost < 0 || cacheReadCost < 0 {
        log.Logger.Warn("invalid model price for cluster %s model %s tier %s", clusterName, targetModel, tierName)
        return 0
    }

    var cost int64
    if cacheReadCost > 0 {
        cost = uncachedTokens*inputCost + cacheReadTokens*cacheReadCost + completionTokens*outputCost
    } else {
        cost = promptTokens*inputCost + completionTokens*outputCost
    }
    return cost
}
```

---

## 7. 计费规则速查

### 7.1 命中 `peak` tier

```
cacheRead  = min(CacheReadTokens, PromptTokens)
uncached   = PromptTokens - cacheRead

cost = uncached × input_cost_per_token
     + cacheRead × cache_read_input_token_cost
     + CompletionTokens × output_cost_per_token
```

其中所有价格字段均取 `tier_prices.peak`，若 `tier_prices.peak` 中未配置某键，则 fallback 到默认 `prices` 中的对应键。

### 7.2 未命中任何 tier（向后兼容 / 空闲时段）

```
cost = PromptTokens × input_cost_per_token
     + CompletionTokens × output_cost_per_token
```

若配置了 `cache_read_input_token_cost`，则同样拆分缓存命中 / 未命中部分。

### 7.3 未配置 `Tiers` / `TierPrices`（完全向后兼容）

行为与现有逻辑完全一致，按固定 `Prices` 计费。

---

## 8. 边界情况与兼容性

| 场景 | 处理建议 |
|---|---|
| `Tiers` 为空 | 无时段匹配，`ActiveTierName` 返回空字符串，使用默认 `Prices` |
| `TierPrices` 为空 | 始终使用默认 `Prices` |
| 命中 tier 但该 tier 未配置某个价格键 | fallback 到默认 `Prices` 中的对应键 |
| `CacheReadTokens > PromptTokens` | 截断为 `PromptTokens`，避免普通 input 为负 |
| `CacheReadTokens` 为负 | 按 0 处理 |
| 未配置 `cache_read_input_token_cost` | 全部按 `input_cost_per_token` 计算 |
| `ModelTable.TimeZone` 为空 | 默认 `Asia/Shanghai` |
| `TierPrices` 中出现非 `peak` 的 tier name | 配置加载阶段拒绝（初期约束） |
| `Tiers` 中出现非 `peak` 的 tier name | 配置加载阶段拒绝（初期约束） |
| ModelMapping / fallback cluster | 按最终 `target_model` + 最终 `cluster` 的 `ModelTable` 计费 |

---

## 9. 测试计划

### 9.1 单元测试

在 `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load_test.go` 中新增：

1. `TestModelTableCheck_Tiers`：验证有效 / 无效时间格式 / 重叠时间范围 / 非 `peak` tier name 拒绝。
2. `TestModelTableCheck_TierPrices`：验证 tier 价格定点整数转换 / 非 `peak` tier name 拒绝。
3. `TestActiveTierName`：验证北京时区周一 10:00 命中 `peak`、周一 13:00 未命中、周六 10:00 未命中、周一 18:00 不命中 `peak`。

在 `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth_test.go` 中新增：

4. `TestCalcCostUnits_Peak`：验证高峰时段按 `tier_prices.peak` 计费正确。
5. `TestCalcCostUnits_OffPeak`：验证非高峰时段 fallback 到默认 `Prices`。
6. `TestCalcCostUnits_Cache`：验证缓存命中 / 未命中分别按对应价格计算。
7. `TestCalcCostUnits_CacheAwareTier`：验证缓存命中价格在 `peak` tier 与默认价格下均正确。

### 9.2 集成测试

新增独立 scenario `bfe/tests/integration/implementation/scenario-SC09-rmb-tiered-pricing/`，测试设计文档见同目录 `test-design.md`。

主要用例（位于 `sc09_rmb_tiered_pricing_test.go`）：

1. `TestTC01_RMBQuotaDeduction_Peak_NonStreaming`：
   - `ModelTable` 配置全天覆盖的 `peak` tier 与 `TierPrices`；
   - 非流式请求命中 `peak`，验证 Redis 按 `tier_prices.peak` 扣费。

2. `TestTC02_RMBQuotaDeduction_NoTier_Fallback`：
   - 相同模型仅配置默认 `Prices`，无 `Tiers`；
   - 验证 Redis 按默认价格扣费，保持向后兼容。

3. `TestTC03_RMBQuotaDeduction_Peak_Cache_NonStreaming`：
   - `peak` tier 下返回含 `cache_read_tokens` 的非流式响应；
   - 验证缓存命中 / 未命中 token 分别按 `peak` cache 价与普通价扣费。

4. `TestTC04_RMBQuotaDeduction_Peak_Cache_Streaming`：
   - 与 TC03 相同，但响应为 SSE 流式；
   - 验证流式场景下最终 usage chunk 也能按 tier cache 价扣费。

运行方式：

```bash
go test ./tests/integration/implementation/scenario-SC09-rmb-tiered-pricing/...
```

---

## 10. 实施步骤建议

1. **配置层**：扩展 `ModelTable` / `ModelPrice` / `TimeRange` / `PriceTier`；增强 `ModelTableCheck`。
2. **计费逻辑**：改造 `calcCostUnits`，实现 tier 匹配与缓存命中价格；同步调用点与单元测试。
4. **测试与回归**：补充配置加载、时段匹配、成本计算单元测试与 RMB 配额集成测试。

---

## 11. 影响范围

| 模块/文件 | 影响 |
|---|---|
| `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go` | 新增时段结构与校验、tier 价格定点转换 |
| `bfe/bfe_basic/request_ai_basic.go` | 复用现有 `TokenUsage.CacheReadTokens` |
| `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go` | Usage 解析与费用计算改造 |
| `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load_test.go` | 新增时段与 tier 价格单元测试 |
| `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth_test.go` | 新增分时段计费与缓存计费单元测试 |
| `bfe/tests/integration/implementation/scenario-SC09-rmb-tiered-pricing/sc09_rmb_tiered_pricing_test.go` | 新增分时段计费集成测试用例 |
| `bfe/tests/integration/implementation/scenario-SC09-rmb-tiered-pricing/test-design.md` | 集成测试设计文档 |
| `bfe/docs/zh_cn/modifications/2026-08-25-tiered-pricing-support/design-changes.md` | 本设计变更文档 |

---

## 12. 兼容性与风险

### 12.1 兼容性

- `ModelTable` / `ModelPrice` 新增字段均为可选，不填时行为与现有逻辑完全一致。
- `TokenUsage.UsedCost`、Lua 扣减逻辑、Redis 定点数存储均不改变。
- Token 配额（`total_token`）扣减逻辑不变。
- Redis key 结构、配置格式均不发生改变。

### 12.2 风险与缓解

| 风险 | 缓解措施 |
|---|---|
| 时区解析失败导致配置加载报错 | `ModelTableCheck` 中校验 `time.LoadLocation`，非法时返回明确错误 |
| `CacheReadTokens` 异常大于 `PromptTokens` | `calcCostUnits` 中校验并截断 |
| 时段规则变更后扣费不一致 | 配置热重载后按新规则执行，旧请求不受影响 |
| 初期约束 `peak` 导致后续扩展时需改校验 | 将 tier name 校验逻辑集中，便于后续放开 |

---

## 13. 参考资料

- `ai-gateway-api/design-docs/sys-design/details/provider与cluster概念分离.md`
- `ai-gateway-api/design-docs/api-define/OpenAPI接口定义/model-prices.md`
- `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go`
- `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go`
- `bfe/bfe_basic/request_ai_basic.go`
