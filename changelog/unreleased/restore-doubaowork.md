### English

- Restore the Doubao Work (豆包工作) provider as an in-process adapter: OpenAI-compatible chat, a live per-account model catalog (DeepSeek, GLM, Kimi, Qwen, MiMo and Doubao models), and remaining-credit read. Chat stays a direct in-process HTTP call to doubao.com; no child process is spawned.
- Add a browser login for Doubao Work driven by a short-lived Playwright helper with a persistent Chrome profile. Sign in once in the popped-up Chrome window; later logins reuse the saved profile. A pasted doubao.com session Cookie still works as before. The browser is only used during login, never per request.
- Add Doubao Work session keepalive: the nightly maintenance sweep performs a health check and persists any refreshed cookies back to the account, so a gateway restart no longer falls back to the exact cookies captured at import. Doubao does not slide `sessionid` on ordinary requests, so an expired session still needs a re-login.
- Daily check-in is intentionally out of scope in this restore: no check-in capability is advertised and no claim endpoint is called. Remaining credit stays a read-only quota view.

### 中文

- 恢复豆包工作（Doubao Work）供应商，改为进程内适配器：OpenAI 兼容聊天、实时按账号模型目录（DeepSeek、GLM、Kimi、Qwen、MiMo 及豆包模型）和剩余积分读取。聊天仍是进程内直连 doubao.com 的 HTTP 请求，不拉起子进程。
- 新增豆包工作浏览器登录，由一次性 Playwright 辅助脚本配合持久化 Chrome 配置驱动：在弹出的 Chrome 窗口登录一次，之后复用已保存的登录态；原有粘贴 doubao.com 会话 Cookie 的方式仍然可用。浏览器只在登录时使用，聊天请求不经过它。
- 新增豆包工作会话保活：夜间维护任务做健康检查，并把刷新过的 Cookie 写回账号，网关重启不再回退到导入时那份 Cookie。豆包不会在普通请求上续期 `sessionid`，因此会话过期后仍需重新登录。
- 本轮不接入每日签到：不声明签到能力，也不调用任何领取接口。剩余积分只做只读配额展示。
