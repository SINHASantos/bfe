# TC-02 Anthropic 风格转发

## 用例编号与名称

TC-02 Anthropic 风格转发

## 所属场景

SC06 Claude 协议支持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证 Anthropic 风格请求被正确路由到仅支持 `anthropic` 协议的集群，并使用 `x-api-key` 注入集群 key，同时自动补 `anthropic-version`。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. mock 后端 `cluster_anthropic_only` 已启动，返回 200。
3. 临时 BFE 配置已生成并加载，`cluster_anthropic_only` 配置 `ModelProtocols = ["anthropic"]`，Key 为 `anthropic-key`。
4. `ai_route.data` 中 `apikey_ak_user_a` 命中 `user_a-anthropic`，target 为 `cluster_anthropic_only`。

## 配置构造

- `cluster_anthropic_only.AIConf`：
  - `ModelProtocols`: `["anthropic"]`
  - `Keys`: `[{"Name":"anthropic-key","Key":"sk-anthropic-key","Weight":100}]`

## BFE 请求

发送 1 次 POST 请求：

| 字段 | 值 |
|------|-----|
| Host | `anthropic.example.org` |
| Path | `/v1/messages` |
| x-api-key | `ak_user_a` |
| Body | `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}` |

## 预期结果

- 响应状态码：200。
- `cluster_anthropic_only` mock backend 命中 1 次。
- upstream 收到的 `x-api-key` 头为 `sk-anthropic-key`。
- upstream 收到的 `anthropic-version` 头为 `2023-06-01`。
- upstream 未收到 `Authorization` 头。

## 清理

停止 `bfe` 进程、mock 后端，删除临时目录。
