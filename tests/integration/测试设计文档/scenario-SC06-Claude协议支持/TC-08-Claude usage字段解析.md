# TC-08 Claude usage 字段解析

## 用例编号与名称

TC-08 Claude usage 字段解析

## 所属场景

SC06 Claude 协议支持

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证 Claude 响应中的 `input_tokens`、`output_tokens` 及 cache 相关字段可被 BFE 正常解析，不影响响应返回。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. mock 后端 `cluster_anthropic_only` 已启动，返回如下 body：
   ```json
   {
     "content": [{"text":"ok"}],
     "usage": {
       "input_tokens": 100,
       "output_tokens": 50,
       "cache_read_input_tokens": 30,
       "cache_creation_input_tokens": 20
     }
   }
   ```
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
| Body | `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"hi"}]}` |

## 预期结果

- 响应状态码：200。
- `cluster_anthropic_only` mock backend 命中 1 次。
- BFE 不报错、不中断响应。

## 清理

停止 `bfe` 进程、mock 后端，删除临时目录。
