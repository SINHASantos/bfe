# TC-01 OpenAI 风格转发

## 用例编号与名称

TC-01 OpenAI 风格转发

## 所属场景

SC06 Claude 协议支持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证 OpenAI 风格请求被正确路由到仅支持 `openai` 协议的集群，并使用 `Authorization: Bearer` 注入集群 key。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. mock 后端 `cluster_openai_only` 已启动，返回 200。
3. 临时 BFE 配置已生成并加载，`cluster_openai_only` 配置 `ModelProtocols = ["openai"]`，Key 为 `openai-key`。
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
| Path | `/v1/chat/completions` |
| Authorization | `Bearer ak_user_a` |
| Body | `{"model":"gpt-4"}` |

## 预期结果

- 响应状态码：200。
- `cluster_openai_only` mock backend 命中 1 次。
- upstream 收到的 `Authorization` 头为 `Bearer sk-openai-key`。
- upstream 未收到 `x-api-key` 头。

## 清理

停止 `bfe` 进程、mock 后端，删除临时目录。
