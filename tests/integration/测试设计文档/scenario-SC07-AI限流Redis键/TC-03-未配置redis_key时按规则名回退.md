# TC-03 未配置 redis_key 时按规则名回退

## 用例编号与名称

TC-03 未配置 redis_key 时按规则名回退

## 所属场景

SC07 AI 限流 Redis 键

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证未配置 `redis_key` 时，BFE 回退到基于规则名生成 Redis key，保持向后兼容。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程与嵌入式 Redis。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. 嵌入式 Redis 已启动。
3. mock 后端 `cluster_ratelimit` 已启动，返回 200。
4. 临时 BFE 配置已生成并加载，包含 `mod_ai_rate_limit` 规则：
   - 规则名：`name-based-rule`
   - 模型：`gpt-4`
   - `redis_key`: 空
   - `MaxRequests`: 1
   - `WindowMinutes`: 1
   - `Burst`: 1

## 配置构造

- `cluster_ratelimit.AIConf`：
  - `Keys`: `[{"Name":"ratelimit-key","Key":"sk-ratelimit-key","Weight":100}]`
- 限流规则 RPM：
  - `Name`: `name-based-rule`
  - `Models`: `["gpt-4"]`
  - `RedisKey`: `""`
  - `MaxRequests`: 1
  - `WindowMinutes`: 1
  - `Burst`: 1

## BFE 请求

发送 2 次 POST 请求：

| 步骤 | Host | Path | Authorization | Body |
|------|------|------|---------------|------|
| 1 | `ratelimit.example.org` | `/v1/chat/completions` | `Bearer ak_ratelimit` | `{"model":"gpt-4"}` |
| 2 | `ratelimit.example.org` | `/v1/chat/completions` | `Bearer ak_ratelimit` | `{"model":"gpt-4"}` |

## 预期结果

- 两次请求分别返回 200、429。
- `cluster_ratelimit` mock backend 被命中 1 次。

## 清理

停止 `bfe` 进程、mock 后端与嵌入式 Redis，删除临时目录。
