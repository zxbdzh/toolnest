# 更新包接入契约

只操作配置的安装目录和独立更新状态目录；不读取/写入 PATH、快照、计划、自启或系统代理。生产仓库/公钥未配置时 `New` 返回可供 UI 展示的 `unconfigured`，`Check` 返回 `ErrNotConfigured`，绝不显示“已是最新”。不生成或发布生产密钥。

## 创建及日常调用

```go
source, err := update.GitHub("实际归属账号/实际仓库")
updates, err := update.New(update.Config{
    CurrentVersion: version,
    Source: source,
    PublicKey: embeddedBase64PublicKey,
    InstallDir: installDirectory,
    StateDir: filepath.Join(userDataRoot, "updates"),
    Executable: "ToolNest.exe",
    Network: sharedNetwork,
    Gate: applicationWriteGate,
})
release, err := updates.Check(ctx, true) // 手动检查，不解除失败版本的阻止
prepared, err := updates.Prepare(ctx)   // 只下载、验签和暂存，不替换
status := updates.Status()             // 轮询副本，不暴露可变内部状态
```

`Gate.TryAcquire(operation string) (release func(), error)` 必须与备份、恢复、导入、目录切换共用真正的排它锁；不要用 `if !Busy()` 检查替代。`LocalGate` 可供 app 直接统一使用。锁失败返回 `ErrBusy`，状态是 `waiting-safe-switch`。`core.Service` 自有内部锁不自动等同于 app 的更新锁，主代理须在 app 调度所有关键操作时持有同一协调器。

`Run(ctx, preferences)` 默认 10 秒后检查、每 24 小时检查一次；每次读取自动检查/下载偏好。只调用 `Check` / `Prepare`，绝不自动替换或重启。也可由 app 自行调度。这两个方法同一个 manager 不允许并发；网络错误不会写“最新”。UI 从 `Status` 读取阶段、当前/可用版本、说明、最近检查、字节进度、速度及差分回退原因；错误不含代理密码。`Check` 的说明和每次下载全部固定使用公共客户端快照，HTTP 重定向也经过同一代理。

已有准备包时 `Phase` 继续保持 `ready`，`NetworkPhase` 单独表示当前检查；再次 `Prepare` 重新核验已准备文件后返回，不重新下载。检查失败仍保留可用准备包，若发现更新的版本则用 `NewerVersion` 提示，先完成当前准备项后再检查。文件缺失或篡改会撤销内存就绪状态。

`presentation.json` 单独保存版本说明、最近检查、准备完成时间和提示关闭标记，避免改动旧版本仍需读取的 `prepared.json` 格式。`DismissNotice(version)` 只记住当前阶段的提示，不丢弃更新。真实更新的非 200 响应保留 `network.HTTPStatusError`；`FailureCode` 用于用户解释与处理入口，`Failure` 保留诊断详情。说明链接失败可回退内嵌说明或明确空态。

## main 必须接入的受限入口

1. 在单实例检查、UI 与常规业务启动 **之前** 分发 `--toolnest-update-helper <request-file> <token>`：用同一个安装位置/状态根/生产源/公钥构造 manager，再调用 `RunHelper(ctx, requestFile, token, nil)`，处理后退出助手模式。助手等原进程退出再替换，且不启动第二个普通实例。
2. 用户明确选择“重启安装”时，等待 app 关键写入安全结束；调用 `HelperCommand(ctx, restartArgs)`，用返回的 `Executable` / `Arguments` 启动受限助手，确认启动成功后关闭 app。该方法本身不启动进程。`restartArgs` 保留窗口/托盘状态，禁止嵌入更新控制参数。
3. 选择“下次启动生效”只保留 `prepared.json`，不启动助手；在下一次 app 开始业务前发现 `Prepared()!=nil` 时走同一助手模式并退出当前启动。隐藏托盘不是退出，不能触发切换。
4. 新版进程会带 `--toolnest-update-confirm <token>`。正常初始化 UI、设置、快照和计划完成后调用 `ConfirmStartup(ctx, token)`；必须从普通业务参数中去掉该受限参数。此进程不要先调用 `Recover` 或再次应用暂存包。
5. 普通非确认启动在业务开始前调用 `Recover(ctx)` 检查中断事务，处理错误和人工恢复路径。若没有待处理事务再按普通方式启动。若出现 `ErrStartup` 且旧版本已恢复，可显示受阻原因；`ErrRecovery` 必须展示保留副本的完整路径，不说成功。

`Apply(ctx)` / `RestartInstalled(ctx, result, args, timeout, launcher)` 是显式底层入口，供助手和隔离测试使用。普通在运行 UI 不应直接调用 `Apply`；Windows 的安全路径是父进程退出后的 `RunHelper`。`Launcher` 可注入测试进程，`StartProcess` 是默认真实启动器。新进程退出、无法启动、握手超时或取消时停新进程并恢复旧版本；不能停止或无法恢复时返回 `ErrRecovery`，旧副本留存。`Recover` 识别写前日志与旧/新文件摘要，支持在 rename 断点后恢复。

状态目录持有内核文件锁（Windows 进程终止后自动释放），防止多个助手同时改程序；app 内仍须使用同一个 `Gate` 防业务并发。受阻版本写 `blocked.json`，自动和手动检查都不解除阻止；只有单独确认后调用 `RetryBlocked(ctx)`，核对仍是被确认的版本，再下载验签。此启动确认只证明应用到达确认点，不代表所有 UI/业务都健康。

失败回退时同步撤销 `prepared.json` 与内存就绪项，避免下次启动跳过检查再次换入受阻版本；只有用户单独确认重试并重新准备成功后，才清除该版本的自动应用阻止。不要在 app 层另存一份绕过这些规则的“已准备更新”标记。

普通退出绝不启动更新助手，准备包等下次真实启动；显式更新重启等待关键写入完成再启动助手。等待阶段可取消，提交退出后不提供强杀。应用层后台检查结束后重读自动下载偏好，检查开始时的旧开关不能覆盖后来保存的设置。

## 签名包与兼容边界

MyGo 来源固定为 `github.com/egoist/mygo` 提交 `0d644f4aca24be8208d1b7d364d7c928fc2cb890`（`v0.9.1-0.20261111182651-0d644f4aca24`）。该版本的 verifier、归档和差分实现均在 `mygo/internal/update`，Go internal 规则禁止本项目直接导入。公开 `mygo.Update.Install` 将下载与替换绑定，不能复用而绕开 busy/用户重启；没有使用它。

因此采用标准库 `crypto/ed25519`，**完全沿用 MyGo 的签名约定：Ed25519(SHA-256(完整 tar.gz 字节))，公钥 32 字节 base64，签名 64 字节 base64**，没有自造签名算法。读取 MyGo 同结构的 `update-windows-amd64.json`（version / notes / url / size / signature / deltas），生产由 GitHub `/releases/latest` 仅取稳定非草稿版，并核对 release tag 与 manifest 版本。每个请求与重定向只允许 HTTPS；`HTTPTestSource` 则只允许数字回环 HTTP，包括每跳重定向，不能用任意公网 HTTP。

首版选择 **单 exe 原子切换策略**，归档只接受下面两个普通文件，任何 DLL/目录/链接/路径穿越/ADS/重复文件均拒绝，不半替换多文件程序：

```text
ToolNest.exe
.toolnest-release.json
```

签名归档内 `.toolnest-release.json` 内容：

```json
{"version":"1.1.0","target":"windows-amd64","executable":"ToolNest.exe"}
```

这份已签名描述绑定目标版本与平台，防止未签名 manifest 把旧归档改称新版本。**MyGo 默认产物若没有描述文件须在发布打包时补入后再按其原签名机制签名；不能直接接受缺描述的旧包。** 私钥只在发布环境或隔离测试内存里；本包无生产签名/发布入口。

默认压缩下载上限 256 MiB、单 exe 512 MiB（配置最大 1 GiB），metadata/notes 1 MiB，描述文件 4 KiB；限制实际流量与最终长度，签名失败/大小不符即拒绝。准备后只留自己的暂存目录；替换前再次验签并从已签名归档重新解包，不信任可编辑暂存 JSON 摘要。换入文件在安装目录同卷准备；旧 exe 改名 `.toolnest-old-<token>.exe` 永久保留供恢复，失败新版也保留 `.toolnest-failed-*`，不清用户数据。没有自动删除旧版本的策略。

差分通过可选 `Config.ApplyDelta` 扩展。当前 MyGo 官方差分同样是 internal 且其输出是目录，不虚构可调用公开接口；未配置兼容 adapter 时明确显示完整包回退。配置的 adapter 接收已验证签名差分，必须输出 **与完整签名归档字节一致** 的 tar.gz；完整包签名再验证失败就回退完整下载，不能接受未签名差分结果。默认可靠性不依赖差分。

## 故障与验收

测试包含真实本地 HTTP 更新源、带认证的转发代理、说明与下载重定向、断网/取消/超限/错签名、篡改暂存、受保护 busy、单 exe 档案校验、差分回退、写文件故障注入、真实 Windows 文件共享冲突、真实测试 exe 启动/确认/退出及真实父进程等待、启动超时、不能结束新进程、回退失败、受阻版本及手动重试。

这些测试不等同于完整 AT-18：NSIS 已安装旧版到已安装新版、完整 UI/计划/快照续接和生产 GitHub 在线发布仍由主代理整机验收；公钥/发布源未配置时不能宣称线上可升级。没有改真实 PATH、系统代理、自启，也没有创建生产密钥或发布版本。
