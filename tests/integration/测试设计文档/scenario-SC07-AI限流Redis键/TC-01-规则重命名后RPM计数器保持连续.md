# TC-01 规则重命名后 RPM 计数器保持连续

## 用例编号与名称

TC-01 规则重命名后 RPM 计数器保持连续

## 所属场景

SC07 AI 限流 Redis 键

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证 `redis_key` 优先级高于规则名，规则重命名不会导致计数器重置。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程与嵌入式 Redis。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. 嵌入式 Redis 已启动。
3. mock 后端 `cluster_ratelimit` 已启动，返回 200。
4. 临时 BFE 配置已生成并加载，包含 `mod_ai_rate_limit` 规则：
   - 规则名：`old-rule`
   - 模型：`gpt-4`
   - `redis_key`: `RL_RPM_rlp-0001_0`
   - `MaxRequests`: 1
   - `WindowMinutes`: 1
   - `Burst`: 1
5. `ai_route.data` 中 `apikey_ak_ratelimit` 命中 `ratelimit-default`，target 为 `cluster_ratelimit`。
6. `ak_ratelimit` 已绑定限流策略 `rlp-0001`。

## 配置构造

- `cluster_ratelimit.AIConf`：
  - `Keys`: `[{"Name":"ratelimit-key","Key":"sk-ratelimit-key","Weight":100}]`
- 限流规则 RPM：
  - `Name`: `old-rule`
  - `Models`: `["gpt-4"]`
  - `RedisKey`: `RL_RPM_rlp-0001_0`
  - `MaxRequests`: 1
  - `WindowMinutes`: 1
  - `Burst`: 1

## BFE 请求

发送 3 次 POST 请求：

| 步骤 | Host | Path | Authorization | Body |
|------|------|------|---------------|------|
| 1 | `ratelimit.example.org` | `/v1/chat/completions` | `Bearer ak_ratelimit` | `{"model":"gpt-4"}` |
| 2 | `ratelimit.example.org` | `/v1/chat/completions` | `Bearer ak_ratelimit` | `{"model":"gpt-4"}` |
| 3（重启 BFE 后，规则名改为 `new-rule`） | `ratelimit.example.org` | `/v1/chat/completions` | `Bearer ak_ratelimit` | `{"model":"gpt-4"}` |

## 预期结果

- 三次请求分别返回 200、429、429。
- `cluster_ratelimit` mock backend 被命中 1 次。

## 清理

停止 `bfe` 进程、mock 后端与嵌入式 Redis，删除临时目录。
