# TC-05 不同 ClientKeyId 独立绑定

## 用例编号与名称

TC-05 不同 ClientKeyId 独立绑定

## 所属场景

SC10 同 cluster 多 API-Key 会话保持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证不同 `ClientKeyId` 在 Redis 中有独立的 Key 绑定，各自命中一个 Provider Key，互不干扰。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程与嵌入式 Redis。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. 嵌入式 Redis 已启动。
3. mock 后端 `cluster_session_affinity` 已启动，返回 200。
4. 临时 BFE 配置已生成并加载，`SessionAffinity = true`。
5. `token_rule.data` 中包含两个 token：
   - `ak_session_affinity` -> `key_id = session_affinity_key_id`
   - `ak_session_affinity_2` -> `key_id = session_affinity_key_id_2`
6. `ai_route.data` 中两个 apiKey 均绑定到同一路由表 `apikey_ak_session_affinity`。

## 配置构造

- `cluster_session_affinity.AIConf`：
  - `Keys`: `[{"Name":"key-a","Key":"sk-key-a","Weight":50},{"Name":"key-b","Key":"sk-key-b","Weight":30},{"Name":"key-c","Key":"sk-key-c","Weight":20}]`
  - `KeyPolicy.SessionAffinity`: `true`

## BFE 请求

| 分组 | Host | Path | Authorization | Body | 次数 |
|------|------|------|---------------|------|------|
| 1 | `session-affinity.example.org` | `/v1/chat/completions` | `Bearer ak_session_affinity` | `{"model":"gpt-4"}` | 20 |
| 2 | `session-affinity.example.org` | `/v1/chat/completions` | `Bearer ak_session_affinity_2` | `{"model":"gpt-4"}` | 20 |

## 预期结果

- 40 次请求全部返回 200。
- 分组 1 的 20 次请求只使用一个 Provider Key。
- 分组 2 的 20 次请求只使用一个 Provider Key。
- Redis 中存在两个绑定：
  - `bfe:ai:key_affinity:cluster_session_affinity:session_affinity_key_id`
  - `bfe:ai:key_affinity:cluster_session_affinity:session_affinity_key_id_2`

## 清理

停止 `bfe` 进程、mock 后端与嵌入式 Redis，删除临时目录。
