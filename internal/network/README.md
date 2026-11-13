# 公共网络接入

本包只改 ToolNest 请求，不写 Windows 系统代理、PATH 或自启。最低外部依赖：项目已固定的 `github.com/egoist/mygo/netproxy`、`golang.org/x/net/http/httpproxy`、`golang.org/x/sys/windows`；不修改 `go.mod`。

```go
vault := network.WindowsVault{Namespace: "ToolNest"}
err := vault.Write(ctx, "proxy-account-v1", network.Credentials{
    Username: username, Password: password,
})
shared, err := network.New(ctx, network.Config{
    Mode: network.Custom,
    Address: "http://127.0.0.1:7890",
    CredentialID: "proxy-account-v1",
}, vault)
```

- `Validate(Config)`：拒绝无效协议、主机、端口、内嵌账号密码、路径和 query。自定义仅 `http://host:port` / `socks5://host:port`，账号密码分别写 Vault；配置只能保存 `CredentialID`。
- `Manager.Configure(ctx, config, vault)`：完整验证成功后原子替换客户端；失败保持原客户端。已在途请求保持旧快照。下载取消完成后再重试，不能边下载边换策略。
- `Manager.Snapshot()`：一项检查/下载操作固定持有一个 `*Client`，对检查、说明、资源及每次重定向使用同一策略。`Client.Do` 支持 context 取消，并去除调用方传入的代理认证头。
- `TestConnection(ctx, actualReleaseEndpoint)`：访问实际更新入口，返回去 query 的地址、状态码及耗时；不能用只连接代理端口冒充连接成功。2xx 才通过更新入口检查；404／403／429／5xx 返回只含状态码的 `HTTPStatusError`（可用 `errors.Is(err, ErrHTTPStatus)` 区分），不混成 `ErrNetwork`，也不导出响应正文。收到 HTTP 响应不代表更新源可用，但不能据此直接判定代理失效；407 仍归类代理认证错误。
- `Client.WithURLPolicy(allow)`：同一 Transport 上限制每一跳的地址，在请求发出前拒绝非法重定向。更新包自动使用它，不需要 app 额外调用。

## 代理规则

`direct` 永远直连，`custom` 永远使用指定代理，二者忽略所有环境代理与 `NO_PROXY`，包括回环目标。代理连接/认证失败不改用直连。

`system`：回环先直连；只要 `HTTP_PROXY` / `HTTPS_PROXY` / `ALL_PROXY` / `NO_PROXY` 之一非空，环境接管，不再回退系统代理。非 Windows 系统上大写优先小写，Windows 环境变量名不区分大小写；HTTP/HTTPS 各自变量优先，缺少时使用 `ALL_PROXY`，`NO_PROXY` 负责绕过。环境代理内容无效明确失败，不由 `httpproxy` 静默忽略。无环境配置时调用固定版本 MyGo `netproxy.Proxy`，复用 WinHTTP 当前用户静态代理、PAC/WPAD 和绕过列表。MyGo 的系统查询有 5 秒缓存，其 PAC 查询失败/无系统答案时返回直连是框架既有行为；ToolNest 明确自定义代理则无此回退。

Windows 凭据调用系统目录的 `advapi32!CredWriteW/CredReadW/CredDeleteW`，当前用户保存。秘密没有 JSON 字段，字符串格式化输出脱敏；Vault 错误不透出系统秘密。底层网络错误归类为固定错误，避免 `url.Error` 带出认证代理地址。HTTP 认证支持 Basic；SOCKS5 支持用户名密码，不承诺 NTLM/Kerberos。

HTTP Basic 和 SOCKS5 口令本身不是传输加密；连接不可信远程代理需由用户保证链路安全。正式更新内容仍由 HTTPS 及独立签名保护。网络失败不能阻塞离线 PATH 功能。

## 测试

真实本地 HTTP 服务器与代理覆盖认证失败、源站不泄密、跨域重定向、SOCKS5 用户密码握手、取消、环境优先级、显式策略、配置原子替换及连接诊断。Credential Manager 测试只使用唯一的 `ToolNest-Test-*` 名字并清理自己的条目，不读取用户已有凭据。系统 WinHTTP/PAC 的具体企业网络配置仍需实机验收。

`TestOptionalLiveProxyConnectionDiagnostics` 仅在显式提供 `TOOLNEST_TEST_LIVE_PROXY` 数值回环代理时才联网，普通测试跳过；不写设置或系统代理。2026-11-13 通过用户界面所填 `http://127.0.0.1:7890`，实际 ToolNest 网络代码对 GitHub API 根返回 200、占位 `toolnest-test/toolnest/releases/latest` 返回 404，两者均非传输失败。该测试是一次现场诊断证据，不把占位仓库的永久 404 当作公开 API 保证，也不代表正式更新源已发布。
