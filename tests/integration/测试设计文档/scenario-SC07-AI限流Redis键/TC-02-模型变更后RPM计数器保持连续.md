# TC-02 模型变更后 RPM 计数器保持连续

## 用例编号与名称

TC-02 模型变更后 RPM 计数器保持连续

## 所属场景

SC07 AI 限流 Redis 键

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证 `redis_key` 不变时，模型变更不会导致计数器重置。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程与嵌入式 Redis。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. 嵌入式 Redis 已启动。
3. mock 后端 `cluster_ratelimit` 已启动，返回 200。
4. 临时 BFE 配置已生成并加载，包含 `mod_ai_rate_limit` 规则：
   - 规则名：`rule-1`
   - 模型：`gpt-4`
   - `redis_key`: `RL_RPM_rlp-0001_0`
   - `MaxRequests`: 1
   - `WindowMinutes`: 1
   - `Burst`: 1

## 配置构造

- `cluster_ratelimit.AIConf`：
  - `Keys`: `[{"Name":"ratelimit-key","Key":"sk-ratelimit-key","Weight":100}]`
- 限流规则 RPM（初始）：
  - `Name`: `rule-1`
  - `Models`: `["gpt-4"]`
  - `RedisKey`: `RL_RPM_rlp-0001_0`
  - `MaxRequests`: 1
  - `WindowMinutes`: 1
  - `Burst`: 1

## BFE 请求

| 步骤 | Body | 说明 |
|------|------|------|
| 1 | `{"model":"gpt-4"}` | 初始模型 |
| 2 | `{"model":"gpt-4"}` | 同一模型，应被限流 |
| 3（重启 BFE 后，规则模型改为 `gpt-3.5`） | `{"model":"gpt-3.5"}` | 模型变更，但 `redis_key` 不变 |

## 预期结果

- 三次请求分别返回 200、429、429。
- `cluster_ratelimit` mock backend 被命中 1 次。

## 清理

停止 `bfe` 进程、mock 后端与嵌入式 Redis，删除临时目录。
