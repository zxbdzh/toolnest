# ToolNest

Windows 桌面工具箱，提供 PATH 查看、快照备份、差异比较与安全恢复。使用 Go 和 [MyGo](https://github.com/egoist/mygo) 原生 UI，采用悬浮侧栏、雾蓝银白配色与应用内磨砂。

## 下载与安装

从 [GitHub Releases](https://github.com/zxbdzh/toolnest/releases/latest) 下载 `ToolNest-Setup-0.0.5-windows-amd64.exe`，支持 Windows 11 x64。

安装前正常退出旧版 ToolNest，包括托盘；覆盖安装保留原配置、快照、自动备份计划和执行记录，无需先卸载。安装完成后，从桌面或开始菜单打开 ToolNest。安装器不修改 PATH，不自动启用自启或备份计划，也不会自动打开软件；卸载保留快照、设置和执行记录。

这是公开体验版，安装器尚未做 Windows 代码签名，系统可能显示“未知发布者”。应用更新包另有独立签名验证。公开更新入口与源码使用本仓库，后续新版本发布后会按设置检查更新。

此前的本地 0.1.x 版本用于开发验收，不属于公开版本序列。请先退出再手动覆盖安装当前公开版本；软件不会自动将更高版本号降级。

## 功能

- 查看、搜索和完整复制用户与系统 PATH，保留顺序、重复项、空项和变量。
- 手动保存快照、比较历史变化，备份后直接查看本次快照。
- 恢复前预览并确认范围，先创建保护快照，再写入与读回校验。
- 查看各范围的恢复结果，通过保护快照重新预览并确认撤回。
- 设置应用内间隔或每日自动备份，无变化只记录检查结果。
- 隐藏到托盘后继续计划，退出后停止；开机自启默认关闭。
- 公共代理支持跟随系统、直连、自定义 HTTP／SOCKS5，代理凭据由 Windows 保存。
- 签名更新包下载后由用户决定重启或下次打开生效，失败保留恢复入口。
- 明暗主题、中文安装向导、统一产品图标，以及桌面和开始菜单入口。
- 概览直达最近快照；通用设置保持精简，代理和存储按需进入高级设置。
- 隐藏后保持后台状态，托盘短暂不可用不会主动弹窗；手动再次启动仍可唤回。
- 软件自绘顶栏，保留拖动、最大化、边缘缩放与关闭确认。
- 内置 MiSans，提供三档字号；应用内磨砂不依赖 Windows 透明效果开关。

查看、搜索、复制和备份不会修改 PATH。**恢复会在预览确认后修改所选范围**；系统范围按需请求管理员授权。

## 开发与验证

需要 Go 1.27.4、Windows 11 x64；依赖锁在 `go.mod/go.sum`。主程序是纯 Go 原生 UI，不需要 Node 或 WebView 前端。

```powershell
go test ./...
go vet ./...
go run .
```

`go run .` 会显示应用窗口。普通测试不显示窗口；真窗口验收需要明确授权后独立执行，不在用户使用电脑时抢焦点。发布、安装或恢复真实 PATH 与自动测试分开。

```powershell
pwsh -File .\scripts\build.ps1 -Task test -Go '<Go 1.27.4 的 go.exe>'
pwsh -File .\scripts\build.ps1 -Task format -Go '<Go 1.27.4 的 go.exe>'
```

默认数据根 `%LOCALAPPDATA%\ToolNest`，程序安装根 `%LOCALAPPDATA%\Programs\ToolNest`。备份只保存注册表原始类型／字节，不修改 PATH；恢复必须经过差异预览、范围确认与保护快照。自启和自动备份默认关闭。

发布构建会生成多尺寸图标、验证 GUI 子系统、构建 NSIS 安装器并校验签名更新资产。签名私钥始终保存在仓库外。

- [正式规格](docs/specs/toolnest-v1.md)：产品范围、实现决定、工程默认值、测试与发布待配置项。
- [领域词表](GLOSSARY.md)：统一产品内概念。
- [框架与产品方向决定](docs/adr/0001-mygo-toolbox.md)。
- [应用内计划与运行状态决定](docs/adr/0002-in-app-scheduling.md)。
- [Magpie 与 MyGo 的安装更新调研](docs/research/installation-and-updates.md)。
- [安装与发布](packaging/README.md)：测试签名、Authenticode、发布门禁与数据保留。
- [发布说明](docs/releases/0.0.5.md)。
- [问题反馈](https://github.com/zxbdzh/toolnest/issues)。
- [第三方许可](THIRD_PARTY_NOTICES.md)。

当前版本面向 Windows 11 x64，采用应用内磨砂、清晰的内容层次和跟随系统的明暗主题；具备软件内部备份计划、托盘与关闭记忆、安装包和自动更新，网络使用工具箱公共代理配置。安装、代理下载与跨版本更新均属于验收范围。

已有回归覆盖原始 PATH 保真、保护快照、失败回滚、计划、代理和更新验证；独立测试安装链已验证首次使用、备份恢复、跨版本更新、卸载和重装保留数据。真实 Explorer 托盘、登录自启、UAC 和物理多显示器 DPI 仍需不同设备继续验证。

旧 PathGuard 快照通过应用内确认复制导入；旧程序、自启与计划任务不自动迁移或停用，归属不明的入口保持原状。

## 许可

[MIT](LICENSE)。
