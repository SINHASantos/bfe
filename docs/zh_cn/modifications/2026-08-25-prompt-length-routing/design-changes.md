# BFE 支持按 Prompt 长度路由（基于 Content-Length）

## 1. 背景

AI 网关客户提出希望根据输入 prompt 长度执行路由策略：

- 长文本请求路由到擅长长上下文的集群/模型；
- 短文本请求路由到成本更低、响应更快的集群/模型。

由于 BFE 对 AI 请求采用流式转发，在转发开始时通常无法确定完整 body 大小，直接解析 body 计算 prompt 长度会受 `AccessibleBodySize` 限制且开销较大。本变更采用新的思路：**直接利用 HTTP `Content-Length` 头判断请求体字节数**，作为 prompt 长度的代理指标，新增两个条件原语 `req_body_larger_than` 和 `req_body_less_than`，在不影响流式转发的前提下支持按 body 大小路由。

---

## 2. 需求示例

某 apikey 希望将长文本请求路由到长上下文集群，短文本请求路由到默认集群：

```json
{
    "Version": "1.0",
    "route_rules": {
        "apikey_ak_user_a": {
            "type": "apikey",
            "owner": "ak_user_a",
            "rules": [
                {
                    "name": "long-body-route",
                    "Cond": "req_body_larger_than(8192)",
                    "targets": [
                        {"cluster": "cluster_long_context", "model": "", "weight": 100}
                    ],
                    "fallbacks": []
                },
                {
                    "name": "short-body-route",
                    "Cond": "req_body_less_than(2048)",
                    "targets": [
                        {"cluster": "cluster_fast_cheap", "model": "", "weight": 100}
                    ],
                    "fallbacks": []
                },
                {
                    "name": "default",
                    "Cond": "default_t()",
                    "targets": [
                        {"cluster": "cluster_default", "model": "", "weight": 100}
                    ],
                    "fallbacks": []
                }
            ]
        }
    },
    "ApikeyRouteTableBindings": {
        "ak_user_a": ["apikey_ak_user_a"]
    }
}
```

> 说明：`req_body_larger_than(8192)` 表示整个 HTTP body 大于 8192 字节，实际 prompt 文本会略小于此值（扣除 JSON 字段开销）。配置阈值时建议通过实际请求采样校准。

---

## 3. 当前现状

| 层级 | 当前能力 | 不足 |
|---|---|---|
| 条件原语 | 支持 `req_host_in`、`req_header_value_in`、`req_body_json_in`、`default_t` 等 | 没有基于请求体大小的条件原语 |
| 路由规则 | `mod_ai_route` 在 `HandleFoundProduct` 阶段执行，可基于条件表达式选择集群 | 无法按 prompt/body 长度做路由决策 |
| 请求处理 | HTTP 头（含 `Content-Length`）在路由阶段已解析完成 | 未暴露 body 大小给条件系统 |
| 流式转发 | BFE 只缓存部分 body（`AccessibleBodySize`），支持流式转发 | 无法依赖完整 body 解析获取 prompt 长度 |

---

## 4. 变更目标

1. 在 BFE 条件系统中新增两个原语：`req_body_larger_than(<bytes>)` 和 `req_body_less_than(<bytes>)`。
2. 原语基于 HTTP `Content-Length` 头判断请求体字节数，单位：字节。
3. 无 `Content-Length` 头时两个原语均返回 `false`（不匹配），行为明确且安全。
4. 支持与其他条件原语组合使用（如 `req_host_in(...) && req_body_larger_than(8192)`）。
5. 新增单元测试与集成测试，覆盖有/无 `Content-Length`、边界值、组合条件等场景。

---

## 5. 变更总览

| 模块 | 主要改动 |
|---|---|
| `bfe_basic/condition` | 新增 `ContentLengthFetcher`、`GtInt64Matcher`、`LtInt64Matcher`；在 `build.go` 注册两个新原语 |
| `bfe_basic/condition/primitive.go` | 实现 Fetcher 与 Matcher |
| `bfe_basic/condition/build.go` | 增加 `req_body_larger_than` / `req_body_less_than` 的 case |
| `bfe/docs/zh_cn/condition/request/body.md` | 补充新原语文档 |
| 测试 | 补充条件原语单元测试、按 body 大小路由集成测试 |

---

## 6. 详细设计

### 6.1 新增条件原语

**文件：** `bfe/bfe_basic/condition/build.go`

在 `Build` 函数的条件原语分发中新增两个 case：

```go
case "req_body_larger_than":
    size, err := strconv.ParseInt(node.Args[0].Value, 10, 64)
    if err != nil {
        return nil, fmt.Errorf("req_body_larger_than: invalid size %s", node.Args[0].Value)
    }
    return &PrimitiveCond{
        name:    node.Fun.Name,
        node:    node,
        fetcher: &ContentLengthFetcher{},
        matcher: &GtInt64Matcher{threshold: size},
    }, nil

case "req_body_less_than":
    size, err := strconv.ParseInt(node.Args[0].Value, 10, 64)
    if err != nil {
        return nil, fmt.Errorf("req_body_less_than: invalid size %s", node.Args[0].Value)
    }
    return &PrimitiveCond{
        name:    node.Fun.Name,
        node:    node,
        fetcher: &ContentLengthFetcher{},
        matcher: &LtInt64Matcher{threshold: size},
    }, nil
```

### 6.2 实现 Fetcher 与 Matcher

**文件：** `bfe/bfe_basic/condition/primitive.go`

```go
// ContentLengthFetcher 从请求头读取 Content-Length
type ContentLengthFetcher struct{}

func (f *ContentLengthFetcher) Fetch(req *bfe_basic.Request) (interface{}, error) {
    if req == nil || req.HttpRequest == nil {
        return nil, fmt.Errorf("fetcher: nil pointer")
    }

    cl := req.HttpRequest.Header.Get("Content-Length")
    if cl == "" {
        return nil, fmt.Errorf("fetcher: Content-Length absent")
    }

    n, err := strconv.ParseInt(cl, 10, 64)
    if err != nil {
        return nil, fmt.Errorf("fetcher: invalid Content-Length %s", cl)
    }
    return n, nil
}

// GtInt64Matcher: value > threshold
type GtInt64Matcher struct {
    threshold int64
}

func (m *GtInt64Matcher) Match(v interface{}) bool {
    n, ok := v.(int64)
    if !ok {
        return false
    }
    return n > m.threshold
}

// LtInt64Matcher: value < threshold
type LtInt64Matcher struct {
    threshold int64
}

func (m *LtInt64Matcher) Match(v interface{}) bool {
    n, ok := v.(int64)
    if !ok {
        return false
    }
    return n < m.threshold
}
```

Fetcher 返回 `error` 时，`PrimitiveCond.Match` 会按 `false` 处理，因此无 `Content-Length` 的请求自动不匹配。

### 6.3 条件原语语义

| 原语 | 参数 | 命中条件 | 无 `Content-Length` 时 |
|---|---|---|---|
| `req_body_larger_than(N)` | 字节数（整数） | `Content-Length > N` | 返回 `false` |
| `req_body_less_than(N)` | 字节数（整数） | `Content-Length < N` | 返回 `false` |

### 6.4 与现有路由链路的集成

`mod_ai_route` 注册在 `HandleFoundProduct` 阶段。在此阶段，HTTP 头（包括 `Content-Length`）已经解析完成，新增原语可直接参与 `ai_route.data` 条件表达式的匹配，不改变后续加权选择、fallback、计费逻辑。

---

## 7. 边界情况与兼容性

| 场景 | 处理建议 |
|---|---|
| 请求无 `Content-Length` 头 | 两个原语均返回 `false`，走默认路由或后续规则 |
| `Content-Length` 为非法值 | Fetcher 返回 error，`PrimitiveCond.Match` 按 `false` 处理 |
| 阈值为负数或非法整数 | `condition.Build` 阶段报错，配置加载失败 |
| `Content-Length` 等于阈值 | `req_body_larger_than` 不命中（严格大于），`req_body_less_than` 不命中（严格小于） |
| 与 `default_t()` 组合 | 按 BFE 条件系统常规优先级执行 |
| chunked 请求 | 通常无 `Content-Length`，两个原语不匹配 |

---

## 8. 测试计划

### 8.1 单元测试

在 `bfe/bfe_basic/condition/` 中新增或扩展测试：

1. `TestReqBodyLargerThan`：
   - `Content-Length` 大于阈值时命中；
   - `Content-Length` 小于/等于阈值时不命中；
   - 无 `Content-Length` 时不命中；
   - 非法 `Content-Length` 时不命中。

2. `TestReqBodyLessThan`：
   - `Content-Length` 小于阈值时命中；
   - `Content-Length` 大于/等于阈值时不命中；
   - 无 `Content-Length` 时不命中；
   - 非法 `Content-Length` 时不命中。

3. `TestContentLengthFetcher`：
   - 验证正常读取、缺失、非法值三种情况。

### 8.2 集成测试

新增独立 scenario `bfe/tests/integration/implementation/scenario-SC10-prompt-length-routing/`，主要用例：

1. `TestTC01_LongBodyRoute`：
   - 后端/模拟请求带 `Content-Length: 10000`；
   - `ai_route.data` 配置 `req_body_larger_than(8192)` 指向 `cluster_long_context`；
   - 验证请求被路由到 `cluster_long_context`。

2. `TestTC02_ShortBodyRoute`：
   - 模拟请求带 `Content-Length: 1024`；
   - `ai_route.data` 配置 `req_body_less_than(2048)` 指向 `cluster_fast_cheap`；
   - 验证请求被路由到 `cluster_fast_cheap`。

3. `TestTC03_NoContentLengthFallback`：
   - 模拟请求不带 `Content-Length`（如 chunked）；
   - 验证走 `default_t()` 默认路由。

运行方式：

```bash
go test ./tests/integration/implementation/scenario-SC10-prompt-length-routing/...
```

---

## 9. 实施步骤建议

1. **条件系统扩展**：在 `bfe_basic/condition/primitive.go` 实现 `ContentLengthFetcher`、`GtInt64Matcher`、`LtInt64Matcher`；在 `build.go` 注册两个新原语。
2. **单元测试**：补充 Fetcher、Matcher、Build 阶段校验测试。
3. **文档更新**：更新 `bfe/docs/zh_cn/condition/request/body.md`，说明新原语语义与使用限制。
4. **集成测试**：新增 SC10 集成测试场景，验证按 body 大小路由到不同 cluster。
5. **回归验证**：确认现有路由、计费、fallback 场景不受影响。

---

## 10. 影响范围

| 模块/文件 | 影响 |
|---|---|
| `bfe/bfe_basic/condition/build.go` | 新增两个条件原语的分支 |
| `bfe/bfe_basic/condition/primitive.go` | 新增 `ContentLengthFetcher`、`GtInt64Matcher`、`LtInt64Matcher` |
| `bfe/bfe_basic/condition/build_test.go` 或相关测试文件 | 新增原语单元测试 |
| `bfe/docs/zh_cn/condition/request/body.md` | 补充新原语文档 |
| `bfe/tests/integration/implementation/scenario-SC10-prompt-length-routing/` | 新增集成测试场景 |
| `bfe/docs/zh_cn/modifications/2026-08-25-prompt-length-routing/design-changes.md` | 本设计变更文档 |

---

## 11. 兼容性与风险

### 11.1 兼容性

- 不修改现有条件原语语义，已有 `ai_route.data` 配置完全兼容。
- 不修改路由、加权选择、fallback、计费逻辑。
- 新增原语为可选能力，未使用时行为不变。

### 11.2 风险与缓解

| 风险 | 缓解措施 |
|---|---|
| `Content-Length` 不等于纯 prompt 长度（包含 JSON 结构开销） | 文档明确说明这是 body 字节数代理指标，建议通过采样校准阈值 |
| 无 `Content-Length` 时原语不匹配 | 文档说明依赖条件；chunked 请求走默认路由 |
| 多模态 base64 导致 body 偏大 | 文档说明阈值需留出余量；未来可叠加二期精确解析方案 |
| 负数/非法阈值导致配置加载失败 | `Build` 阶段做参数校验，非法配置明确报错 |

---

## 12. 参考资料

- `bfe/bfe_basic/condition/build.go`
- `bfe/bfe_basic/condition/primitive.go`
- `bfe/bfe_modules/mod_ai_route/mod_ai_route.go`
- `bfe/bfe_modules/mod_ai_route/route_rule.go`
- `bfe/bfe_basic/request_ai_basic.go`
- `bfe/bfe_basic/request_ai_route.go`
