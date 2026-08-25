# TC-02 Fallback 应用自身 strip 配置

## 用例编号与名称

TC-02 Fallback 应用自身 strip 配置

## 所属场景

SC08 AI 模型 Fallback 重算

## 版本声明

- `bfe`：当前源码版本

## 测试目的

验证 fallback cluster 不仅从 `ClientModel` 重算，还会应用自身的 `StripPrefix` 配置。

## 运行模式

单组件模式：仅启动真实 `bfe` 进程。

## 前置条件

1. 已编译 `bfe` 可执行文件。
2. mock 后端已启动：
   - `cluster_openrouter` 返回 503，触发 fallback；
   - `cluster_fallback` 返回 200。
3. 临时 BFE 配置已生成并加载。

## 配置构造

### cluster_openrouter.AIConf

```json
{
    "Type": 0,
    "MatchPrefix": "openrouter/",
    "StripPrefix": true,
    "ModelMapping": {
        "modelA": "mapped-primary-model"
    },
    "Keys": [
        {"Name": "primary-key", "Key": "sk-primary-key", "Weight": 100}
    ]
}
```

### cluster_fallback.AIConf

```json
{
    "Type": 0,
    "MatchPrefix": "clientprefix/",
    "StripPrefix": true
}
```

### ai_route.data 路由规则

| 规则名 | 条件 | targets | fallbacks |
|--------|------|---------|-----------|
| user_a-openrouter | `req_body_json_prefix_in("model", "clientprefix/", false)` | `cluster_openrouter`，Model 覆盖为 `openrouter/modelA` | `cluster_fallback`，Model 为空 |
| user_a-default | `default_t()` | `cluster_default` | 无 |

## BFE 请求

发送 1 次 POST 请求：

| 字段 | 值 |
|------|-----|
| Host | `api.example.org` |
| Path | `/v1/chat/completions` |
| Authorization | `Bearer ak_user_a` |
| Body | `{"model":"clientprefix/mymodel","messages":[{"role":"user","content":"hello"}]}` |

## 主 cluster 模型计算

1. target Model 覆盖：`openrouter/modelA`
2. strip `openrouter/`：`modelA`
3. ModelMapping：`mapped-primary-model`

## fallback cluster 模型计算

1. target Model 为空，保持 `ClientModel`：`clientprefix/mymodel`
2. strip `clientprefix/`：`mymodel`

## 预期结果

- 响应状态码：200。
- `cluster_openrouter` mock backend 命中 1 次，收到的 model 为 `mapped-primary-model`。
- `cluster_fallback` mock backend 命中 1 次，收到的 model 为 `mymodel`。

## 清理

停止 `bfe` 进程、mock 后端，删除临时目录。
