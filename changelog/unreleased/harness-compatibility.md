### English

- `POST /v1/messages/count_tokens` is now served for Anthropic clients: Claude Code can size the context before a turn and auto-compact on time. The estimate counts system prompts, tool schemas and image blocks (1500 tokens per image) instead of forwarding base64 as text.
- `/v1/responses` answers Codex CLI's empty-input warmup with a synthetic completed response, so startup probes no longer fail with `input required` or spend tokens on an empty prompt.
- Tool-result screenshots are materialized into a trailing user image turn for the Codex, Meta, Qoder, Devin and Command adapters; previously those adapters built the tool message from text only and the picture vanished.
- MiniMax Code forwards the client's `max_tokens` verbatim instead of overwriting it with 8192, and its Anthropic-shaped payload now accepts user images and images inside `tool_result`.
- CodeArts downloads remote images with a browser `User-Agent`/`Accept` (the cause of CDN 403s against hosts such as Wikimedia) and rejects an inline `data:` URL above 32 MiB before it reaches the upstream.
- Codex and Meta now reject an image part that has no URL with an explicit 400 instead of sending an empty `input_image`.

### 中文

- 新增 `POST /v1/messages/count_tokens`：Claude Code 可以在每轮之前估算上下文并按时自动压缩。估算覆盖 system、工具 schema 和图片块（每张图按 1500 token），不再把 base64 当普通文本计数。
- `/v1/responses` 现在本地应答 Codex CLI 的空输入预热请求（返回合成的完成响应），启动探测不再报 `input required`，也不会为空提示词消耗 token。
- Codex、Meta、Qoder、Devin、Command 适配器会把工具结果里的截图补成一条用户图片消息；此前这些适配器只按文本构造工具消息，图片会静默消失。
- MiniMax Code 改为原样转发客户端的 `max_tokens`，不再固定覆盖为 8192；其 Anthropic 形状的请求体也开始接受用户图片和 `tool_result` 内的图片。
- CodeArts 下载远程图片时补齐浏览器 `User-Agent`/`Accept`（修复 Wikimedia 一类 CDN 的 403），并在进入上游前拒绝超过 32 MiB 的内联 `data:` URL。
- Codex 与 Meta 遇到没有 URL 的图片部件时返回明确的 400，不再发送空的 `input_image`。
