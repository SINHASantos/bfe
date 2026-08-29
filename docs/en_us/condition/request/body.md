# Condition Primitives Related to Request Body

## req_body_json_in(json_path, value_list, case_insensitive)

* Meaning: Searches for the field specified by `json_path` in the JSON-formatted request body and checks if its value exactly matches any in `value_list`.
* Parameters  

| Parameter        | Description                                    |
| ---------------- | ---------------------------------------------- |
| json_path        | String<br>The path to the JSON field in the request body |
| value_list       | String<br>List of values, separated by ‘&#124;’ |
| case_insensitive | Boolean<br>Whether to ignore case sensitivity   |

* Example

```go
req_body_json_in("model", "deepseek-r1|qwen-plus", true)
```

## req_body_json_prefix_in(json_path, value_prefix_list, case_insensitive)

* Meaning: Searches for the field specified by `json_path` in the JSON-formatted request body and checks if its string value starts with any prefix in `value_prefix_list`.
* Parameters

| Parameter        | Description                                    |
| ---------------- | ---------------------------------------------- |
| json_path        | String<br>The path to the JSON field in the request body |
| value_prefix_list | String<br>List of prefixes, separated by ‘&#124;’ |
| case_insensitive | Boolean<br>Whether to ignore case sensitivity   |

* Example

```go
// Match all OpenRouter models
req_body_json_prefix_in("model", "openrouter/", false)

// Match all models under OpenRouter/anthropic namespace
req_body_json_prefix_in("model", "openrouter/anthropic/", false)

// Match all models starting with gpt- or claude- (case-insensitive)
req_body_json_prefix_in("model", "gpt-|claude-", true)
```

## req_body_larger_than(size)

* Meaning: Checks whether the value of the `Content-Length` request header is strictly greater than `size` (unit: bytes).
* Parameters

| Parameter | Description       |
| --------- | ----------------- |
| size      | Integer<br>Threshold in bytes |

* Notes
  * The data source is the HTTP `Content-Length` header, which reflects the total size of the HTTP body in bytes, not the pure prompt text length.
  * If the request has no `Content-Length` header (e.g., chunked requests), this primitive returns `false`.
  * Calibrate the threshold against real traffic and reserve margin for the fixed overhead of the JSON structure.

* Example

```go
// Match when the request body is larger than 8KB
req_body_larger_than(8192)
```

## req_body_less_than(size)

* Meaning: Checks whether the value of the `Content-Length` request header is strictly less than `size` (unit: bytes).
* Parameters

| Parameter | Description       |
| --------- | ----------------- |
| size      | Integer<br>Threshold in bytes |

* Notes
  * The data source is the HTTP `Content-Length` header, which reflects the total size of the HTTP body in bytes, not the pure prompt text length.
  * If the request has no `Content-Length` header (e.g., chunked requests), this primitive returns `false`.
  * Calibrate the threshold against real traffic and reserve margin for the fixed overhead of the JSON structure.

* Example

```go
// Match when the request body is smaller than 2KB
req_body_less_than(2048)
```
