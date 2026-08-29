# TC-04 单 Key cluster 跳过 Redis 绑定

## 用例编号与名称

TC-04 单 Key cluster 跳过 Redis 绑定

## 所属场景

SC10 同 cluster 多 API-Key 会话保持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证当 cluster 下只配置一个 API-Key 时，BFE 完全不读写 Redis 绑定，避免冗余的 Redis 访问。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程与嵌入式 Redis。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. 嵌入式 Redis 已启动。
3. mock 后端 `cluster_session_affinity` 已启动，返回 200。
4. 临时 BFE 配置已生成并加载，`AIConf.Keys` 仅包含一个 Key。

## 配置构造

- `cluster_session_affinity.AIConf`：
  - `Keys`: `[{"Name":"key-a","Key":"sk-key-a","Weight":100}]`
  - `KeyPolicy.SessionAffinity`: `true`

## BFE 请求

连续发送 10 次 POST 请求：

| Host | Path | Authorization | Body |
|------|------|---------------|------|
| `session-affinity.example.org` | `/v1/chat/completions` | `Bearer ak_session_affinity` | `{"model":"gpt-4"}` |

## 预期结果

- 10 次请求全部返回 200。
- mock 后端收到的 `Authorization` 头全部为 `Bearer sk-key-a`。
- Redis 中不存在 `bfe:ai:key_affinity:cluster_session_affinity:session_affinity_key_id`。

## 清理

停止 `bfe` 进程、mock 后端与嵌入式 Redis，删除临时目录。
