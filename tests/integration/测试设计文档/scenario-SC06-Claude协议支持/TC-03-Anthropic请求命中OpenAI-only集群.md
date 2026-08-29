# TC-03 Anthropic 请求命中 OpenAI-only 集群

## 用例编号与名称

TC-03 Anthropic 请求命中 OpenAI-only 集群

## 所属场景

SC06 Claude 协议支持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证请求协议与集群 `ModelProtocols` 不匹配时，BFE 直接拒绝且不访问 upstream。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. mock 后端 `cluster_openai_only` 已启动。
3. 临时 BFE 配置已生成并加载，`cluster_openai_only` 配置 `ModelProtocols = ["openai"]`。
4. `ai_route.data` 中 `apikey_ak_user_a` 命中 `user_a-openai`，target 为 `cluster_openai_only`。

## 配置构造

- `cluster_openai_only.AIConf`：
  - `ModelProtocols`: `["openai"]`
  - `Keys`: `[{"Name":"openai-key","Key":"sk-openai-key","Weight":100}]`

## BFE 请求

发送 1 次 POST 请求：

| 字段 | 值 |
|------|-----|
| Host | `openai.example.org` |
| Path | `/v1/messages` |
| x-api-key | `ak_user_a` |
| Body | `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}` |

## 预期结果

- 响应状态码：400。
- 响应体包含 `PROVIDER_PROTOCOL_MISMATCH`。
- `cluster_openai_only` mock backend 命中 0 次。

## 清理

停止 `bfe` 进程、mock 后端，删除临时目录。
