# ToolNest 原生入口

`main.go` 仅调用 `toolnest.Run(args, stdout, stderr)`。Windows 入口按以下顺序执行：

1. `platform.HandleHelper`：受限 PATH 恢复助手，在单实例、窗口与普通权限检查之前。
2. 参数校验、`--version` / `--toolnest-build-info` 只读输出。
3. 固定 LocalAppData 数据根，正常主进程拒绝提升权限；显式隔离模式允许提升状态验收。
4. 私有副本更新助手，不创建普通 UI 或调度器，不取得主实例锁。
5. `lifecycle.AcquireInstance`、实际安装 exe 的终身 `AcquireInstallation`、app 与 MyGo 桌面。

## 导出入口

- `Run(arguments []string, stdout, stderr io.Writer) int`。
- 窗口 1180×780，最小 620×520；`ui.Content` 原生绘制，Sidebar 材质，正文实底由 UI 包保证。
- `--diagnostics-json` 不创建窗口或计划，不输出 PATH 原文或代理凭据。
- `--data-dir <绝对目录> --test-root <32位小写hex>` 必须成对，禁止与真实数据、旧数据和程序目录重叠。
- 测试 Run/Vault/单实例均按 test root 隔离；不连接真实任务计划程序，不能导入真实旧快照，快照迁移限制在测试数据根内。
- `--smoke` 仅隔离模式可用；启动真实窗口与托盘，1.5 秒后输出状态并经过业务安全退出。
- `--test-update-source http://127.0.0.1:<port>/manifest --test-update-key-file <绝对公钥文件>` 仅显式隔离模式可用；只接受数值 loopback 与外部 base64 Ed25519 公钥。
- `--legacy-executable <绝对旧exe>` 可重复，提供归属依据而非自动授权停用。确认后保存路径依据和生命周期 journal，重启后可撤销。

## 更新接线

后台仅检查和 Prepare；不使用框架自动替换插件。RestartUpdate、正常退出或下次启动有准备包时，先复制当前 exe 到 `updates/helpers/<token>/` 再启动 HelperCommand；RunHelper 等待父进程退出后 Apply、RestartInstalled。助手不能直接运行被替换的目标 exe，否则 Windows 会锁住目标程序。

新版在原生首帧、设置/快照/调度初始化后 ConfirmStartup；正常安装路径才同步安装版本登记。监督失败回退由 update.Manager 完成。主入口检查 blocked 记录，避免下次启动循环重试同一失败版本。隐藏到托盘只隐藏，不运行 helper。更新助手错误保存在 `updates/helper-result.json`，下次启动显示。

助手与新版子进程用 `CREATE_NO_WINDOW` 抑制控制台，不设置 `HideWindow`／`SW_HIDE`；后者会连正式主窗口一并隐藏，造成版本已确认但用户看不到界面。主窗口的显示／托盘状态只由生命周期控制器与更新恢复参数决定。无窗口子进程回归检查真实 `GetStartupInfo`，后台 UIA 另行验证新版窗口可交互。

生产 Repository/UpdatePublicKey 未配置时明确 unconfigured，离线 PATH 仍可用；普通开发 exe 不参与生产自替换。私钥从不进入入口参数、源树或构建身份。

## 验证

```powershell
$go = (Get-Command go.exe).Source
& $go test ./cmd/toolnest
& $go vet ./cmd/toolnest .
& $go test ./...
```

可选真实升级测试先用版本 overlay 在仓库外构建两个 TEST-ONLY 版本，不修改 `packaging/VERSION`。`Version()` 从嵌入文件读取版本，不能用 `-X toolnest/packaging.version` 伪造两个版本：

```powershell
pwsh -NoProfile -File .\tests\e2e\Build-TestVersions.ps1 -MakeNSIS '<NSIS 3.13 makensis.exe>' -SigningKeyDirectory '<仓库外 TEST-ONLY 密钥目录>'
# 以构建脚本实际返回的外部目录替换此值；后台控制器负责真实桌面核验与子进程清理。
$versions = '<仓库外版本验收目录>'
pwsh -NoProfile -File .\tests\e2e\Invoke-Background.ps1 -Scenario entry -PreviousApplication "$versions\0.1.0\ToolNest.exe" -UpdatedApplication "$versions\0.1.1\ToolNest.exe" -TimeoutSeconds 180
pwsh -NoProfile -File .\tests\e2e\Invoke-Background.ps1 -Scenario update-ui -PreviousApplication "$versions\0.1.0\ToolNest.exe" -UpdatedApplication "$versions\0.1.1\ToolNest.exe" -SignedUpdate "$versions\signed-update" -TimeoutSeconds 180
```

`entry` 使用临时目录、内存临时签名密钥和 loopback，覆盖真实主程序 0.1.0→0.1.1、托盘启动请求下的确认、无法执行的新版回退并阻止循环。仅设置环境变量不足以放开窗口测试，必须核验当前真实线程桌面与输入桌面不同。`update-ui` 使用预签名真实应用包，经 HTTP／SOCKS5 认证代理驱动检查、下载、稍后更新与重启更新，再核对新版的数据、计划和凭据续接。普通 go test 跳过这些窗口测试。私有桌面没有 Explorer 通知区域，不能由此宣称真实托盘可用；这些测试也不是已安装路径、安装器、UAC 或 DPI 全验收的替代。

## 交给主代理的跨包核对

`lifecycle.Command.Value` 写出带引号的 `--autostart` 参数；`packaging/runtime.OwnedRun` 已严格兼容本应用带引号及安装兼容格式，其他参数不匹配。单元测试通过，卸载清理的实际系统验收仍需 Sandbox。

`mygo.json` 是原生开发/框架构建配置，不包含前端与默认自动更新插件。正式安装产物应继续走仓库 `scripts/build.ps1` 的自有安全安装器，不用框架默认 NSIS 替代已实现的维护锁与卸载边界。

图标来自 `internal/brand`，窗口通过 MyGo `SetIcon` 显式设置，托盘正常／失败状态使用同一识别图形；构建资源嵌入主程序和测试启动器。显式隔离窗口标题为「ToolNest 更新测试版」，主壳显示「使用合成 PATH」，避免与正常安装混淆。新版数据初始化失败会记录绑定本次确认令牌的 `startup-failure.json`，只有相同事务的回退才能引用该原因。
