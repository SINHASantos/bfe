# TC-01 开启亲和性时同一 ClientKeyId 命中同一 Key

## 用例编号与名称

TC-01 开启亲和性时同一 ClientKeyId 命中同一 Key

## 所属场景

SC10 同 cluster 多 API-Key 会话保持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证 `AIConf.KeyPolicy.SessionAffinity = true` 时，同一 `ClientKeyId` 的连续请求会被绑定到同一个 Provider Key，从而提升 Provider 侧缓存命中率。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程与嵌入式 Redis。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. 嵌入式 Redis 已启动。
3. mock 后端 `cluster_session_affinity` 已启动，返回 200。
4. 临时 BFE 配置已生成并加载，包含：
   - `AIConf.Keys`：`key-a`、`key-b`、`key-c`
   - `KeyPolicy.SessionAffinity = true`
   - `KeyPolicy.SessionAffinityRedisPrefix = "bfe:ai:key_affinity"`
5. `ai_route.data` 中 `apikey_ak_session_affinity` 命中 `cluster_session_affinity`。
6. `ak_session_affinity` 已绑定到该路由表，并在 `token_rule.data` 中对应 `key_id = session_affinity_key_id`。

## 配置构造

- `cluster_session_affinity.AIConf`：
  - `Keys`: `[{"Name":"key-a","Key":"sk-key-a","Weight":50},{"Name":"key-b","Key":"sk-key-b","Weight":30},{"Name":"key-c","Key":"sk-key-c","Weight":20}]`
  - `KeyPolicy.SessionAffinity`: `true`
  - `KeyPolicy.SessionAffinityTTL`: `300`
  - `KeyPolicy.SessionAffinityRedisPrefix`: `"bfe:ai:key_affinity"`

## BFE 请求

连续发送 50 次 POST 请求：

| Host | Path | Authorization | Body |
|------|------|---------------|------|
| `session-affinity.example.org` | `/v1/chat/completions` | `Bearer ak_session_affinity` | `{"model":"gpt-4"}` |

## 预期结果

- 50 次请求全部返回 200。
- mock 后端收到的 `Authorization` 头只有一种 Provider Key。
- Redis 中存在绑定 `bfe:ai:key_affinity:cluster_session_affinity:session_affinity_key_id`。

## 清理

停止 `bfe` 进程、mock 后端与嵌入式 Redis，删除临时目录。
