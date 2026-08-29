# TC-06 BFE 重启后绑定保持

## 用例编号与名称

TC-06 BFE 重启后绑定保持

## 所属场景

SC10 同 cluster 多 API-Key 会话保持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证 Redis 中的 `ClientKeyId -> Provider Key` 绑定在 BFE 进程重启后仍然有效，保证会话亲和性跨进程保持。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程与嵌入式 Redis。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. 嵌入式 Redis 已启动。
3. mock 后端 `cluster_session_affinity` 已启动，返回 200。
4. 临时 BFE 配置已生成并加载，`SessionAffinity = true`。

## 配置构造

- `cluster_session_affinity.AIConf`：
  - `Keys`: `[{"Name":"key-a","Key":"sk-key-a","Weight":50},{"Name":"key-b","Key":"sk-key-b","Weight":30},{"Name":"key-c","Key":"sk-key-c","Weight":20}]`
  - `KeyPolicy.SessionAffinity`: `true`
  - `KeyPolicy.SessionAffinityTTL`: `300`

## BFE 请求

| 阶段 | Host | Path | Authorization | Body | 次数 |
|------|------|------|---------------|------|------|
| 重启前 | `session-affinity.example.org` | `/v1/chat/completions` | `Bearer ak_session_affinity` | `{"model":"gpt-4"}` | 10 |
| 重启后 | `session-affinity.example.org` | `/v1/chat/completions` | `Bearer ak_session_affinity` | `{"model":"gpt-4"}` | 10 |

执行步骤：

1. 启动 BFE，发送 10 次请求，记录最终绑定的 Provider Key `K1`；
2. 停止 BFE 进程，但保持 mock 后端与 Redis 运行；
3. 使用同一配置重新启动 BFE；
4. 再发送 10 次请求。

## 预期结果

- 重启前 10 次请求全部返回 200，且均使用同一 Provider Key `K1`。
- 重启后 10 次请求全部返回 200，且均继续使用 `K1`。
- Redis 中绑定 `bfe:ai:key_affinity:cluster_session_affinity:session_affinity_key_id` 的值为 `K1` 对应的名字（如 `key-a`）。

## 清理

停止 `bfe` 进程、mock 后端与嵌入式 Redis，删除临时目录。
