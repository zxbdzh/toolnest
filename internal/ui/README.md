# ToolNest MyGo 原生 UI

此包只依赖 `internal/app` 的只读 `View` 与 `Actions`，不创建 WebView，不写注册表、文件或系统配置。

## 主程序集成

已有 MyGo 生命周期和托盘时，使用以下入口，避免再启动一个应用循环：

```go
controller := ui.New(actions)
window := mygo.NewWindow(mygo.WindowOptions{
    Title: "ToolNest", Width: 1180, Height: 780,
    MinWidth: 620, MinHeight: 520,
    Vibrancy: mygo.VibrancySidebar,
    Content: controller.Content(),
})
controller.AttachWindow(window)
```

托盘显示窗口：`window.Show()`；托盘打开设置：在主线程调用 `controller.ShowSettings()` 后 `window.Show()`、`window.Invalidate()`。主代理负责托盘、单实例和 `RequestClose` 的隐藏／安全退出动作；真正退出不能再次调用会触发相同关闭确认的 `window.Close()`，应由业务协调完成后销毁窗口或退出 MyGo。

没有现成 MyGo 循环时可直接调用 `ui.Run(actions)`。两种集成方式只能选一种。

## 数据与线程

- `View()` 在绘制时调用，必须快速且线程安全，返回切片和嵌套范围的不可变副本。UI 每 250ms 拉取实际状态，不调用 `Refresh()` 模拟后台刷新。
- 耗时 Actions（包括预览、目录选择和保存）在工作 goroutine 提交。服务负责串行化、真实 Busy/Error/Status、结果持久化及 MyGo 原生对话框所需的主线程派发。
- `Copy`、`OpenDataDirectory`、`RequestClose` 从 UI 主线程调用，须快速返回；退出必须遵守业务层关键写入保护。
- `PreviewRestore` 须返回非空 `Token`，以及与请求一致的 `SnapshotID/User/System`。恢复按钮只提交最后一次、范围匹配的预览；取消或变更范围使旧结果失效。
- 字符串枚举：主题 `system/light/dark`；关闭 `ask/tray/exit`（兼容 `quit`）；计划 `interval/daily`；代理 `system/direct/custom`。版本状态支持当前业务的 `unconfigured/unchecked/checking/latest/available/downloading/verifying/ready/waiting-safe-switch/applying/pending-startup-confirmation/confirmed/blocked/failed`，未知值原样显示。
- `Update.Progress` 为 0–1；`Entry.Index` 是业务层提供的原始序号，不由 UI 重新编号。`Scope.Text` 是完整复制源，不使用筛选结果拼接。
- UI 不提供强制中断关键写入。检查和下载通过可选 `CancelDownload()` 能力取消；保存代理与正在执行的更新请求互斥，需取消请求后重试。`View.ProxyTesting` 表达连接测试全过程并禁止重复提交。目录迁移只统计 `Snapshot.Error == ""` 的可识别条目，并提示执行前重新核实。

## 交互

2026-11-13 按用户确认采用「浮光布局＋雾蓝银白」：默认进入工具空间，顶部胶囊导航直接打开 PATH 保护、快照和自动备份，右上角保留应用设置。首页包含真实状态的 PATH 备份主卡、计划摘要和三个工具入口；工具搜索不改变业务状态。快照数量只统计可读快照，不将损坏文件或演示数据作为保护证据。

PATH 顶栏刷新／备份；宽屏 MyGo SplitPercent 可拖动且键盘可操作，窄屏保留各自搜索／选择；Table 独立滚动、原始序号、横向长路径和完整复制。快照列表将时间、来源和摘要放在同一单元格，详情操作保持在左侧；默认不勾选删除项，恢复默认仅用户，差异方向明确。

设置在宽屏使用分类导航与限宽内容区，在窄屏使用 MyGo Segmented，默认进入外观与启动，不再把所有设置堆成长表单。公共代理卡片直接进入代理设置，自定义模式才展示地址与凭据字段；测试使用草稿，不能自动保存。所有分类共享同一草稿，各自保留滚动位置，保存与取消始终固定在底部。后台刷新与页面导航不会冲掉未保存修改。保存由服务确认，不显示乐观成功；目录迁移、删除、导入、旧入口修改和更新重启分别确认。

首页保留浮光布局，但压缩装饰封面，工具入口采用统一雾蓝图标与实际状态，最近活动展示最多三条真实记录。自动备份在宽屏分开呈现配置草稿与实际计划状态，切换开关不能冒充已生效。更新界面按状态展示下载或重启操作，重启仍需单独确认。

保留 MyGo 原生 Button、SearchField、Segmented、Table、SplitPercent、Switch、DialogBase，不引入 WebView 或新的前端框架。装饰插画由内嵌 SVG 与 MyGo Image 绘制；不承担数据或交互语义。

强调色 `#416B8C`；浅色背景 `#F3F6F9`，正文 `#253441`、次级文字 `#5C6C78`，深色正文 `#E2EAF0`、次级文字 `#B0C0CC`。顶栏露出 Windows 系统磨砂，正文使用实色；材质关闭时回退实底。正文与操作以 13–14 DIP 为主，PATH 表格内容高 28 DIP 加 MyGo 内边距约 36–40 DIP。较矮窗口压缩页标题，优先保留操作空间。没有统一入场动效。

更新流程提供可关闭的全局就绪／结果提示、直达更新设置、可读版本说明、下载字节量与速率、独立校验状态、按错误类型显示的处理入口，以及单独确认的失败版本重试。选择下次打开生效只关闭同版本提示并保留准备包；普通退出不会重新拉起主程序。托盘的版本入口也打开同一更新页面，不自动弹窗。

关键操作期间可请求退出或重启，`View.PendingExit` 表达真实等待意图，`CancelExit` 可撤销；进入提交阶段后禁用取消。重启前发现本地草稿未保存会要求先保存或取消修改。保存回执由递增的 `View.SettingsRevision` 确认，不依赖全局错误是否为空，因此托盘错误不会阻止代理保存回执，凭据保存失败也不能被误判为成功。

## 验证

```powershell
go test ./internal/ui
go vet ./internal/ui
```

无窗口测试使用 MyGo NewTester 覆盖首页入口、真实数据摘要、工具搜索、布局、标签导航、搜索保序、完整复制、取消恢复、过期预览、草稿和关闭确认。首页导航按 620/780/1180/1440 DIP、浅深主题、100/125/150/200% 渲染缩放检查边界。它们不能代替 Windows 实际显示器 DPI、系统磨砂、托盘和 UAC 验收。

分类切换回归通过辅助技术角色定位实际输入框，避免文字标签与编辑控件同名造成误定位；更新操作回归按按钮角色精确定位，不能把「自动下载更新」开关误判成下载按钮。设定 `TOOLNEST_UI_REVIEW_DIR` 可导出 14 张原生无窗口预览，图中使用隔离测试夹具，不读取个人 PATH 或配置。边界见 [验证范围](../../docs/validation.md)。

`tests/e2e/Invoke-Background.ps1 -Scenario inspect` 在不切换输入桌面的私有桌面启动测试程序，通过 UIA 操作及 PrintWindow 截图；数据与注册表使用单独临时目录和 `HKCU\Software\ToolNest\Tests\<32hex>`。不得用前台键鼠、真实 PATH 或真实安装目录补齐缺失验收。

0.1.5 的首次引导可选择创建首份快照；跳过后仍有空状态入口。备份／恢复操作提供持久结果及关联快照，UI 的撤回只打开保护快照的新预览，不直接复用旧确认。数据错误由 `View.Problems` 提供处理入口和独立详情；更新回退可显示当前版本与实际启动检查原因，有数据错误时先处理再允许重试。
