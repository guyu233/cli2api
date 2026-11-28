### English

- Aggregation members now normalize the provider family and strip a provider/region prefix from the model id. A member picked from a prefixed list entry such as `deepseek/deepseek-v4-flash` is stored as the real model and can route instead of silently matching nothing.
- Logical aggregation ids may contain `/`. They are resolved by exact name before provider-prefix parsing, so an id such as `custom/v5-probe` or `deepseek/v4-flash` is not mistaken for a real provider model.
- OpenAI chat streams, Anthropic Messages and Responses now echo the model id the client sent (including streams) instead of the concrete member model, so aggregation and pooled names stay provider-neutral end to end.
- A model-scoped cooldown no longer hides a healthy sibling member on the same account: routing falls through to that member instead of reporting no route until the whole cooldown expires.
- `/v1/models` no longer marks the whole catalog stale when a provider with zero served models (for example qoder before any account is added) has a failed sync.

### 中文

- 聚合成员会归一化供应商标识，并去掉模型名里的供应商/区域前缀。从带前缀的模型列表选中 `deepseek/deepseek-v4-flash` 时，会按真实模型保存并正常路由，不再静默匹配不到任何账号。
- 逻辑聚合 ID 允许包含 `/`。路由会先按完整名称解析逻辑模型，再做供应商前缀解析，因此 `custom/v5-probe`、`deepseek/v4-flash` 这类 ID 不会被当成真实的供应商模型。
- OpenAI 聊天流、Anthropic Messages 和 Responses 现在都会回显客户端请求的模型名（流式同样生效），不再泄露实际成员模型名，聚合与同名模型池的名称从入口到出口保持供应商中立。
- 模型级冷却不再连带隐藏同一账号上的健康兄弟成员：路由会落到该成员，而不是在整个冷却期内一直报无可用路由。
- 某个供应商零模型（例如尚未添加账号的 qoder）同步失败时，`/v1/models` 不再把整个目录标记为 stale。
