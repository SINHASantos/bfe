# TC-01 `peak` tier 非流式按 tier 价扣费

## 用例编号与名称

TC-01 `peak` tier 非流式按 tier 价扣费

## 所属场景

SC09 RMB 分时段计费

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证当 `AIConf.ModelTable` 配置了 `Tiers` 与 `TierPrices` 时，BFE 在活跃 tier 时段内按 `TierPrices` 扣减 RMB 配额。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程与嵌入式 Redis。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. 嵌入式 Redis 已启动，并预置 `quota:plan_rmb = 10000000000`（100 元）。
3. mock 后端 `cluster_rmb` 已启动，返回 200 与如下 body：
   ```json
   {
       "usage": {
           "prompt_tokens": 100,
           "completion_tokens": 50,
           "total_tokens": 150
       }
   }
   ```
4. 临时 BFE 配置已生成并加载，`cluster_rmb` 配置 `ModelTable`：
   - `Tiers` 中 `peak` 覆盖全周全天；
   - `deepseek-chat` 的默认 input/output 价格分别为 `0.000001` / `0.000002`；
   - `deepseek-chat` 的 `TierPrices.peak` input/output 价格分别为 `0.000002` / `0.000004`。
5. `ak_user_a` 绑定 RMB 配额计划 `plan_rmb`。

## 配置构造

- `cluster_rmb.AIConf.ModelTable.Tiers[0]`：
  - `Name`: `peak`
  - `TimeRanges[0].Weekdays`: `[0,1,2,3,4,5,6]`
  - `TimeRanges[0].Start`: `00:00`
  - `TimeRanges[0].End`: `23:59`
- `cluster_rmb.AIConf.ModelTable.Models[0]`：
  - `Model`: `deepseek-chat`
  - `Mode`: `chat`
  - `Prices.input_cost_per_token`: `0.000001`
  - `Prices.output_cost_per_token`: `0.000002`
  - `TierPrices.peak.input_cost_per_token`: `0.000002`
  - `TierPrices.peak.output_cost_per_token`: `0.000004`
- `plan_rmb`：
  - `Unit`: `RMB`
  - `Quota`: `10000000000`
  - `RedisKey`: `quota:plan_rmb`

## BFE 请求

发送 1 次 POST 请求：

| 字段 | 值 |
|------|-----|
| Host | `tier.example.org` |
| Path | `/v1/chat/completions` |
| Authorization | `Bearer ak_user_a` |
| Body | `{"model":"deepseek-chat"}` |

## 预期结果

- 响应状态码：200。
- `cluster_rmb` 收到 1 次命中。
- Redis 中 `quota:plan_rmb` 的余额变为：
  - 扣减金额 = `100 * 200 + 50 * 400 = 40000`（0.0004 元）
  - 剩余 = `10000000000 - 40000 = 9999960000`

## 清理

停止 `bfe` 进程、mock 后端与嵌入式 Redis，删除临时目录。
