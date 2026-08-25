# TC-07 显式 anthropic-version 被保留

## 用例编号与名称

TC-07 显式 anthropic-version 被保留

## 所属场景

SC06 Claude 协议支持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证客户端显式传递的 `anthropic-version` 不会被 BFE 的默认值覆盖。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. mock 后端 `cluster_anthropic_only` 已启动，返回 200。
3. 临时 BFE 配置已生成并加载，`cluster_anthropic_only` 配置 `ModelProtocols = ["anthropic"]`。

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
| anthropic-version | `2023-06-02` |
| Body | `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}` |

## 预期结果

- 响应状态码：200。
- `cluster_anthropic_only` mock backend 命中 1 次。
- upstream 收到的 `anthropic-version` 头仍为 `2023-06-02`。

## 清理

停止 `bfe` 进程、mock 后端，删除临时目录。
