# BFE 支持 Claude 协议转发设计变更

## 1. 背景

当前 BFE AI 网关的数据面实现默认按 **OpenAI 兼容协议** 处理请求：

- 客户端 API Key 只从 `Authorization: Bearer <key>` 提取；
- 转发给上游时无条件注入 `Authorization: Bearer <cluster-key>`；
- `usage` 只解析 `prompt_tokens` / `completion_tokens` / `total_tokens`；
- 没有“协议/认证风格”概念。

Anthropic Claude Messages API 与 OpenAI 存在几个转发视角的关键差异：

| 项目 | OpenAI / 兼容协议 | Claude Messages API |
|------|-------------------|---------------------|
| 对话端点 | `POST /v1/chat/completions` | `POST /v1/messages` |
| 客户端认证头 | `Authorization: Bearer <key>` | `x-api-key: <key>` |
| 版本头 | 无 | 必须 `anthropic-version: 2023-06-01` |
| 响应 usage | `usage.prompt_tokens` / `completion_tokens` / `total_tokens` | `usage.input_tokens` / `output_tokens`（无 total） |

BFE 对 Claude 协议**不需要做协议翻译**（`content[]` 与 `choices[]` 的结构差异对网关透明），但必须解决三个核心差异：

1. 认证头差异；
2. `anthropic-version` 版本头要求；
3. Usage 字段差异。

本设计变更仅涉及 **BFE 数据面**；控制面（`ai-gateway-api`）的配套修改在本仓库外完成。

---

## 2. 目标

1. 让 BFE 在只做转发的前提下，支持把客户端 Claude Messages API 请求原样转发到 Anthropic 后端。
2. 自动识别 Claude 协议风格（路径 + 请求头）。
3. 按协议风格向上游注入正确的 API Key（`x-api-key`）和 `anthropic-version`。
4. 扩展 usage 解析，支持 `input_tokens` / `output_tokens`，保证配额扣减、成本计算与访问日志正确。
5. 校验请求协议与集群 provider 支持的协议是否匹配，不匹配直接拒绝。
6. 保持 OpenAI / DeepSeek 等现有集群的向后兼容。

---

## 3. 变更总览

| 层级 | 变更点 | 影响文件 |
|---|---|---|
| 请求基础信息 | `AiBasicInfo` 新增 `AuthStyle`，`GetApiKey` 支持 `x-api-key` fallback | `bfe/bfe_basic/request_ai_basic.go` |
| 认证头注入 | `SetApiKey` 按 `AuthStyle` 注入 `Authorization` 或 `x-api-key` | `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go` |
| 版本头注入 | `doSingleAIForward` 在 Anthropic 风格下自动补 `anthropic-version` | `bfe/bfe_server/reverseproxy.go` |
| Usage 解析 | `UpdateCtxByUsage`、`SSEEvent.GetQuotaUsage`、`RawEvent.GetQuotaUsage` 支持 Claude usage 字段 | `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go`、`bfe/bfe_modules/mod_body_process/llm_util.go`、`bfe/bfe_modules/mod_body_process/body_process.go` |
| 协议风格匹配 | `doSingleAIForward` 比较请求 `AuthStyle` 与 `AIConf.ModelProtocols`，不匹配返回 400 | `bfe/bfe_server/reverseproxy.go` |
| 配置扩展（阶段一） | `AIConf` 新增 `ModelProtocols`，接收控制面下发的 provider 协议列表 | `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go` |
| 访问日志 | 新增 `ai_protocol` 字段，记录 `AuthStyle` | `bfe-access-pb/bfe_access_pb/bfe_access.proto`、`bfe/bfe_modules/mod_access_pb3/request_log.go` |


---

## 4. 详细设计

### 4.1 协议风格定义

```go
const (
    AuthStyleOpenAI    = "openai"
    AuthStyleAnthropic = "anthropic"
    AuthStyleUnknown   = "unknown"
)
```

识别规则（ purely based on 请求特征，由 BFE 底层代码完成，不依赖用户路由表条件）：

1. 请求路径：`strings.HasPrefix(req.HttpRequest.URL.Path, "/v1/messages")` → `anthropic`；
2. 请求头：存在 `x-api-key` 且不存在 `Authorization` → `anthropic`；
3. 否则默认 `openai`。

识别结果写入 `AiBasicInfo.AuthStyle`。

> **命名约定**：`anthropic` 是协议/风格标识符（与 `model_protocols` 枚举一致），`Claude` 是 Anthropic 的模型/助手品牌。BFE 代码中统一用 `anthropic` 表示该协议风格。
>
> 注意：不能靠 `model` 名字判断，也不能靠 `AIConf.Provider` 名称判断（provider 名称是用户自定义的，如 `my-anthropic`）。协议匹配校验使用 `AIConf.ModelProtocols`。

### 4.2 扩展 `AiBasicInfo`

文件：`bfe/bfe_basic/request_ai_basic.go`

```go
type AiBasicInfo struct {
    // ... 已有字段 ...
    AuthStyle string // openai / anthropic / unknown
    // ...
}
```

`GetApiKey` 改造为：

```go
func GetApiKey(req *Request) string {
    // 1. 优先读取 Authorization: Bearer <key>
    authHeader := req.HttpRequest.Header.Get("Authorization")
    if authHeader != "" {
        authHeader = strings.TrimPrefix(authHeader, "Bearer ")
        authHeader = strings.TrimPrefix(authHeader, "sk-")
        if ai := req.GetAiBasicInfo(); ai != nil {
            ai.AuthStyle = AuthStyleOpenAI
        }
        return authHeader
    }

    // 2. fallback 读取 x-api-key
    if xApiKey := req.HttpRequest.Header.Get("x-api-key"); xApiKey != "" {
        if ai := req.GetAiBasicInfo(); ai != nil {
            ai.AuthStyle = AuthStyleAnthropic
        }
        return xApiKey
    }

    return ""
}
```

优先级说明：当客户端同时传 `Authorization` 和 `x-api-key` 时，优先 `Authorization`，保证现有 OpenAI 客户端行为不变。

### 4.3 按风格注入 API Key

文件：`bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go`

当前 `SetApiKey`：

```go
func SetApiKey(req *bfe_http.Request, apiKey string) {
    if apiKey == "" {
        return
    }
    req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
}
```

改造后：

```go
func SetApiKey(req *bfe_http.Request, apiKey string, authStyle string) {
    if apiKey == "" {
        return
    }

    switch authStyle {
    case AuthStyleAnthropic:
        req.Header.Set("x-api-key", apiKey)
    default:
        req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", apiKey))
    }
}
```

调用点 `bfe/bfe_server/reverseproxy.go:1525` 同步更新为：

```go
mod_ai_token_auth.SetApiKey(outreq, selectedKey.Key, aiMeta.AuthStyle)
```

### 4.4 注入 `anthropic-version`

文件：`bfe/bfe_server/reverseproxy.go`（`doSingleAIForward` 中设置 API Key 之后、`clusterInvoke` 之前）

```go
if aiMeta.AuthStyle == AuthStyleAnthropic {
    if outreq.Header.Get("anthropic-version") == "" {
        outreq.Header.Set("anthropic-version", "2023-06-01")
    }
}
```

阶段一选择 Anthropic 当前长期支持的版本 `2023-06-01` 作为默认值，由 BFE 自动注入。阶段一不引入额外配置字段。

### 4.5 Usage 解析扩展

Claude 响应 usage 示例：

```json
{
  "usage": {
    "input_tokens": 1000,
    "output_tokens": 500,
    "cache_creation_input_tokens": 200,
    "cache_read_input_tokens": 300
  }
}
```

#### 4.5.1 非流式：`mod_ai_token_auth.go`

`UpdateCtxByUsage` 在读取 OpenAI 字段后增加 fallback：

```go
used       = gjson.GetBytes(data, "usage.total_tokens").Int()
prompt     = gjson.GetBytes(data, "usage.prompt_tokens").Int()
completion = gjson.GetBytes(data, "usage.completion_tokens").Int()
cacheRead  = gjson.GetBytes(data, "usage.cache_read_tokens").Int()
cacheWrite = gjson.GetBytes(data, "usage.cache_write_tokens").Int()

// Claude fallback
if prompt == 0 && completion == 0 {
    prompt     = gjson.GetBytes(data, "usage.input_tokens").Int()
    completion = gjson.GetBytes(data, "usage.output_tokens").Int()
    if cacheRead == 0 {
        cacheRead = gjson.GetBytes(data, "usage.cache_read_input_tokens").Int()
    }
    if cacheWrite == 0 {
        cacheWrite = gjson.GetBytes(data, "usage.cache_creation_input_tokens").Int()
    }
    if used == 0 {
        used = prompt + completion
    }
}
```

#### 4.5.2 流式：`llm_util.go` / `body_process.go`

`SSEEvent.GetQuotaUsage` 与 `RawEvent.GetQuotaUsage` 做同样扩展：

```go
used       := gjson.GetBytes(data, "usage.total_tokens").Int()
prompt     := gjson.GetBytes(data, "usage.prompt_tokens").Int()
completion := gjson.GetBytes(data, "usage.completion_tokens").Int()
cacheRead  := gjson.GetBytes(data, "usage.cache_read_tokens").Int()
cacheWrite := gjson.GetBytes(data, "usage.cache_write_tokens").Int()

// Claude fallback
if prompt == 0 && completion == 0 {
    prompt     = gjson.GetBytes(data, "usage.input_tokens").Int()
    completion = gjson.GetBytes(data, "usage.output_tokens").Int()
    if cacheRead == 0 {
        cacheRead = gjson.GetBytes(data, "usage.cache_read_input_tokens").Int()
    }
    if cacheWrite == 0 {
        cacheWrite = gjson.GetBytes(data, "usage.cache_creation_input_tokens").Int()
    }
    if used == 0 {
        used = prompt + completion
    }
}
```

> 说明：
> - Anthropic 的 `input_tokens` 已经包含 cache read / cache creation 部分，因此 `UsedQuota` 仍由 `input_tokens + output_tokens` 推导；
> - `cache_read_input_tokens` / `cache_creation_input_tokens` 单独写入 `CacheReadTokens` / `CacheWriteTokens`，供后续按 cache 价格项计费；
> - Anthropic 目前没有 `audio_*` / `image_count` 字段，本期保持现有解析逻辑即可。

最终 `TokenUsage.PromptTokens` / `CompletionTokens` / `UsedQuota` 与 OpenAI 场景保持一致，配额、成本、日志字段无需额外改造。

### 4.6 协议风格匹配与拒绝

文件：`bfe/bfe_server/reverseproxy.go`（`doSingleAIForward` 开头，在构建 `outreq` 之前）

```go
// 若此前未识别风格，则兜底识别
if aiMeta.AuthStyle == AuthStyleUnknown || aiMeta.AuthStyle == "" {
    aiMeta.AuthStyle = DetectAuthStyle(basicReq)
}

// 判断当前集群是否支持该请求协议
if !clusterSupportsAuthStyle(cluster.AIConf.ModelProtocols, aiMeta.AuthStyle) {
    err := bfe_basic.NewAiError(
        bfe_basic.CodeProviderProtocolMismatch,
        bfe_basic.TypeInvalidRequestError,
        fmt.Sprintf("request protocol %s not supported by cluster provider (model_protocols=%v)",
            aiMeta.AuthStyle, cluster.AIConf.ModelProtocols),
    )
    return err.CreateErrorResponse(basicReq), closeAfterReply, nil
}
```

其中 `clusterSupportsAuthStyle` 直接判断 `authStyle` 是否在 `model_protocols` 中（因为 `AuthStyle` 取值与 `model_protocols` 枚举保持一致）：

```go
func clusterSupportsAuthStyle(modelProtocols []string, authStyle string) bool {
    if len(modelProtocols) == 0 {
        return authStyle == AuthStyleOpenAI // 兼容旧配置
    }
    for _, mp := range modelProtocols {
        if mp == authStyle {
            return true
        }
    }
    return false
}
```

> 当前 `AIConf` 需要新增 `ModelProtocols []string`；若为空则默认只支持 OpenAI，保证旧配置向后兼容。

### 4.7 `AIConf` 新增 `ModelProtocols []string` 字段

文件：`bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go`

```go
type AIConf struct {
    Type         int
    ModelMapping *map[string]string
    Provider     string
    Keys         []AIKey
    KeyPolicy    *AIKeyPolicy
    ModelTable   *ModelTable
    MatchPrefix  string
    StripPrefix  bool

    // ModelProtocols 表示该集群所属 provider 支持的模型访问协议。
    // 来源：ai-gateway-api 的 provider.model_protocols，如 ["openai"]、["anthropic"]、["openai", "anthropic"]。
    // 用于 doSingleAIForward 中判断请求协议风格是否被当前集群支持。
    ModelProtocols []string
}
```

控制面（`ai-gateway-api`）的 `model/icluster_conf/cluster.go` 在 `newAIConf` 时需要把 provider 的 `model_protocols` 透传进来：

```go
func newAIConf(llmConfig *LLMConfig, modelTable *cluster_conf.ModelTable,
               providerKeys []iprovider.ProviderKey,
               providerModelProtocols []string) *cluster_conf.AIConf {
    aiConf := &cluster_conf.AIConf{
        Type:           0,
        ModelMapping:   convertToBFEModelMapping(llmConfig.ModelMappings),
        Keys:           []cluster_conf.AIKey{},
        ModelProtocols: providerModelProtocols,
    }
    // ... 其余字段不变 ...
    return aiConf
}
```

> 为什么不能通过 `AIConf.Provider` 名称推断协议？cluster/provider 分离后，`provider` 名称是用户自定义的（如 `my-anthropic`），不再限定为系统内置枚举，只有显式的 `ModelProtocols` 才能准确表达该 provider 支持哪些协议。

### 4.8 访问日志增加 `ai_protocol` 字段

文件：`bfe-access-pb/bfe_access_pb/bfe_access.proto`

在 `ai_mode`（`716`）之后新增字段：

```proto
// AI protocol style, e.g. openai, anthropic
optional string ai_protocol = 717;
```

修改后需要重新生成 `bfe_access.pb.go`，并同步更新 `bfe-access-pb/docs/protobuf.md`。

文件：`bfe/bfe_modules/mod_access_pb3/request_log.go`

在填充访问日志时，把 `AiBasicInfo.AuthStyle` 写入 `ai_protocol`：

```go
if aiInfo := req.GetAiBasicInfo(); aiInfo != nil {
    // ... 已有字段 ...
    if aiInfo.Provider != "" {
        reqLog.AiProvider = proto.String(aiInfo.Provider)
    }
    if aiInfo.AuthStyle != "" {
        reqLog.AiProtocol = proto.String(aiInfo.AuthStyle)
    }
    // ...
}
```

> 说明：`ai_provider` 保存用户自定义的 provider 名称，无法准确反映协议类型；新增 `ai_protocol` 后，一个标识“哪个 provider”，一个标识“什么协议”。

---

## 5. 关键代码索引

| 文件 | 行号范围 | 说明 |
|---|---|---|
| `bfe/bfe_basic/request_ai_basic.go` | 82-98 | `AiBasicInfo` 结构 |
| `bfe/bfe_basic/request_ai_basic.go` | 118-130 | `GetApiKey` |
| `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go` | 114-153 | `UpdateCtxByUsage` |
| `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go` | 233-239 | `SetApiKey` |
| `bfe/bfe_modules/mod_ai_token_auth/mod_ai_token_auth.go` | 241-253 | `GetApiKey`（模块内辅助函数） |
| `bfe/bfe_server/reverseproxy.go` | 1515-1527 | `doSingleAIForward` 中应用 `AIConf`、注入 API Key |
| `bfe/bfe_modules/mod_body_process/llm_util.go` | 123-157 | `SSEEvent.GetQuotaUsage` |
| `bfe/bfe_modules/mod_body_process/body_process.go` | 422-455 | `RawEvent.GetQuotaUsage` |
| `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go` | 167-181 | `AIConf` 结构 |
| `bfe/bfe_config/bfe_cluster_conf/cluster_conf/cluster_conf_load.go` | 167-181 | `AIConf.ModelProtocols` 新增字段 |
| `bfe-access-pb/bfe_access_pb/bfe_access.proto` | 357-365 | `ai_protocol = 717` 新增字段 |
| `bfe/bfe_modules/mod_access_pb3/request_log.go` | 400-410 | 将 `AiBasicInfo.AuthStyle` 写入 `ai_protocol` |

---

## 6. 测试计划

### 6.1 单元测试

| 被测函数 | 场景 |
|---|---|
| `bfe_basic.GetApiKey` | `Authorization: Bearer xxx`；`x-api-key: xxx`；两者同时存在时的优先级；无 key |
| `mod_ai_token_auth.SetApiKey` | `openai` 风格生成 `Authorization: Bearer`；`anthropic` 风格生成 `x-api-key` |
| `mod_ai_token_auth.UpdateCtxByUsage` | OpenAI usage JSON；Claude usage JSON；混合字段时的优先级 |
| `mod_body_process.SSEEvent.GetQuotaUsage` | Claude 流式 usage 字段解析 |
| `mod_body_process.RawEvent.GetQuotaUsage` | Claude 非流式 usage 字段解析 |
| `bfe_basic.DetectAuthStyle` | `/v1/messages`、`x-api-key`、OpenAI 默认路径识别 |
| `bfe_server`（协议匹配） | Anthropic 风格请求命中 `model_protocols=["openai"]` 的集群返回 400；OpenAI 风格请求命中 `model_protocols=["anthropic"]` 的集群返回 400 |
| `mod_access_pb3` | `ai_protocol` 字段被正确填充为 `openai` 或 `anthropic` |

### 6.2 集成测试

| 用例编号 | 场景 | 预期 |
|---|---|---|
| SC02-TC-XX | 客户端 `POST /v1/messages`，带 `x-api-key` | upstream 收到 `x-api-key` 为选中的 cluster key，`anthropic-version` 被自动注入 |
| SC02-TC-XX | Claude 非流式响应返回 `input_tokens`/`output_tokens` | 访问日志 `ai_input_tokens`/`ai_output_tokens`/`ai_total_tokens` 正确；访问日志 `ai_protocol` 为 `anthropic` |
| SC02-TC-XX | Claude 流式响应返回 usage | RMB 配额扣减与访问日志正确；访问日志 `ai_protocol` 为 `anthropic` |
| SC02-TC-XX | OpenAI 风格请求 | 访问日志 `ai_protocol` 为 `openai` |
| SC02-TC-XX | Anthropic 风格请求命中只支持 OpenAI 的集群 | BFE 返回 400 `PROVIDER_PROTOCOL_MISMATCH`，upstream 不被调用 |
| SC02-TC-XX | OpenAI 风格请求命中只支持 Claude 的集群 | BFE 返回 400 `PROVIDER_PROTOCOL_MISMATCH`，upstream 不被调用 |
| SC02-TC-XX | 同集群同时命中 OpenAI 与 Claude 路径 | 不同认证头不互相污染 |

### 6.3 回归测试

- `go test ./bfe_basic/...`
- `go test ./bfe_modules/mod_ai_token_auth/...`
- `go test ./bfe_modules/mod_body_process/...`
- `go test ./bfe_server/...`
- `go test ./bfe_modules/mod_access_pb3/...`

---

## 7. 风险与回滚

| 风险 | 缓解措施 |
|---|---|
| 认证头冲突 | 明确优先级：`Authorization` > `x-api-key`，兼容现有 OpenAI 客户端 |
| `anthropic-version` 默认值过期 | 选择长期支持版本 `2023-06-01`；阶段一不引入额外配置字段 |
| 聚合平台模型名含 `anthropic/` 或 `claude` 但走 OpenAI 路径 | 不依赖 model 名判断，依赖路径/头/集群配置 |
| Claude 无 `total_tokens` | `UsedQuota` 由 `input + output` 推导 |
| 影响现有 OpenAI/DeepSeek 集群 | 默认 `AuthStyle == openai`，仅在明确识别为 Claude 时切换；`AIConf.ModelProtocols` 为空时默认只支持 OpenAI |
| 用 provider 名称硬编码协议风格 | provider 名称由用户自定义，不能再用 `"anthropic"` 判断；必须通过 `AIConf.ModelProtocols` 校验 |

**回滚**：该设计修改的是 BFE 二进制行为。若线上需要恢复旧行为，需回滚代码并重新发版；影响面仅涉及 AI 网关路径。

---

## 8. 与控制面的边界

控制面（`ai-gateway-api`）需要完成的配套工作（不在本仓库实现）：

1. `/providers` 创建/更新时设置 `model_protocols` 包含 `anthropic`（首期枚举已支持）；
2. `/clusters` 的 `llm_config.provider` 引用该 provider（`provider_type` 字段已移除）；
3. `model/icluster_conf/cluster.go` 的 `newAIConf` 把 provider 的 `model_protocols` 写入 BFE `AIConf.ModelProtocols`；
BFE 数据面按“阶段一把 `model_protocols` 透传到 `AIConf` 做校验”推进，先以最小改动完成 Claude 转发支持，同时为后续多协议扩展预留空间。
