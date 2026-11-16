# ToolNest MyGo 原生 UI

此包只依赖 `internal/app` 的只读 `View` 与 `Actions`，不创建 WebView，不写注册表、文件或系统配置。

## 主程序集成

已有 MyGo 生命周期和托盘时，使用以下入口，避免再启动一个应用循环：

```go
controller := ui.New(actions)
options := controller.WindowOptions("ToolNest")
options.Hidden = true
window := mygo.NewWindow(options)
controller.AttachWindow(window)
```

窗口使用 `TitleBarHidden + HideWindowButtons`，由现有内容顶栏绘制最小化、最大化／还原与关闭按钮，不额外增加一行系统标题栏；保留 Windows 边缘缩放、圆角、阴影与最大化按钮的贴靠布局。顶栏空白和侧栏品牌区可拖动，双击顶栏沿用系统最大化行为。关闭按钮与系统关闭消息共用 `RequestClose`，不得绕过托盘配置或关闭确认。窗口控件的可访问名称、最小尺寸布局和真实私有桌面行为均需回归。

托盘显示窗口：`window.Show()`；托盘打开设置：在主线程调用 `controller.ShowSettings()` 后 `window.Show()`、`window.Invalidate()`。主代理负责托盘、单实例和 `RequestClose` 的隐藏／安全退出动作；真正退出不能再次调用会触发相同关闭确认的 `window.Close()`，应由业务协调完成后销毁窗口或退出 MyGo。

没有现成 MyGo 循环时可直接调用 `ui.Run(actions)`。两种集成方式只能选一种。

## 数据与线程

- `View()` 在绘制时调用，必须快速且线程安全，返回切片和嵌套范围的不可变副本。UI 每 250ms 拉取实际状态，不调用 `Refresh()` 模拟后台刷新。
- 耗时 Actions（包括预览、目录选择和保存）在工作 goroutine 提交。服务负责串行化、真实 Busy/Error/Status、结果持久化及 MyGo 原生对话框所需的主线程派发。
- `Copy`、`OpenDataDirectory`、`RequestClose` 从 UI 主线程调用，须快速返回；退出必须遵守业务层关键写入保护。
- `PreviewRestore` 须返回非空 `Token`，以及与请求一致的 `SnapshotID/User/System`。恢复按钮只提交最后一次、范围匹配的预览；取消或变更范围使旧结果失效。
- 字符串枚举：主题 `system/light/dark`；字号 `compact/standard/comfortable`（空值兼容旧配置，按标准档）；关闭 `ask/tray/exit`（兼容 `quit`）；计划 `interval/daily`；代理 `system/direct/custom`。版本状态支持当前业务的 `unconfigured/unchecked/checking/latest/available/downloading/verifying/ready/waiting-safe-switch/applying/pending-startup-confirmation/confirmed/blocked/failed`，未知值原样显示。
- `Update.Progress` 为 0–1；`Entry.Index` 是业务层提供的原始序号，不由 UI 重新编号。`Scope.Text` 是完整复制源，不使用筛选结果拼接。
- UI 不提供强制中断关键写入。检查和下载通过可选 `CancelDownload()` 能力取消；保存代理与正在执行的更新请求互斥，需取消请求后重试。`View.ProxyTesting` 表达连接测试全过程并禁止重复提交。目录迁移只统计 `Snapshot.Error == ""` 的可识别条目，并提示执行前重新核实。

## 交互

2026-11-14 按用户要求参考 [MyGo Music](https://github.com/egoist/mygo-music) 的导航和设置组织；11-16 按最新反馈继续参考 [BewlyCat](https://github.com/keleus/BewlyCat) 的悬浮导航、磨砂和紧凑控件层次，独立实现悬浮侧栏与连续磨砂工作面，不复制其源码、品牌或媒体资产。概览直接展示环境、最近快照、最近活动和当前 PATH；侧栏进入 PATH 保护、快照、自动备份和活动记录，底部保留应用设置及关于与更新。快照数量只统计可读快照，不将损坏文件或演示数据作为保护证据。

PATH 顶栏刷新／备份；宽屏 MyGo SplitPercent 可拖动且键盘可操作，窄屏保留各自搜索／选择；Table 独立滚动、原始序号、横向长路径和完整复制。快照采用原生虚拟 List 的主从浏览，列表行统一实底、细选中标记与明确的查看入口；删除复选框与行查看各自保留可访问名称，方向键和 Enter 仍可操作。默认不勾选删除项，恢复默认仅用户，差异方向明确。

快照删除复选框与查看按钮必须是并列控件，不能把复选框放进 `ButtonBase.Children`：MyGo 的辅助功能树把按钮视为叶节点，会隐藏其内部交互控件。回归须检查 `Tester.AccessByRole(RoleCheckBox, name)` 并验证真实私有桌面 UIA；仅 `Tester.Click(name)` 不能证明辅助技术可用。

11-16 最新材质：根据用户明确要求，应用内磨砂独立于 Windows 透明效果开关。`frost.go` 使用 MyGo shader 的背景采样与 20 DIP 模糊，自行完成饱和度、透色和边缘高光；不能调用会按 `ReduceTransparency` 回退实底的 `glass.Blur`。不截取桌面、不修改系统设置；高对比模式仍优先实底。导航分为实际内容高度的两组短悬浮 Dock，不再将空白撑成全高菜单。环境色场必须在根背景或显式负层级，工作面裁剪子内容到圆角内。

动效分工：导航选中底 240ms 连续滑移，显隐 150ms；图标悬停放大至 109%、220ms；导航按压 97.5%，实体按钮按压 96.5%、180ms；按钮悬停／释放颜色与柔影 150ms；分段指示 200ms；弹层 200ms 原地轻微聚焦、150ms 收回。全部响应用户操作，可中断且不等待动画结束才执行业务；减少动态效果时直接反馈。没有背景循环、整页上飘和列表逐行入场。快照列表只在局部移除原生 Accent、SurfaceHover 与 Background，行内 Checkbox 和按钮恢复完整主题，保留虚拟化、键盘与独立辅助功能。

设置统一使用通用／高级／更新三类。通用只有主题、字号、开机自启与关闭行为；代理、存储迁移、自启隐藏放在高级设置，未检测到旧版数据时不展示导入入口。自定义代理才显示凭据字段，测试使用草稿而不自动保存。所有分类共享同一草稿，各自保留滚动位置；有修改才显示固定底部的保存与取消。后台刷新与页面导航不会冲掉未保存修改。保存由服务确认，不显示乐观成功；目录迁移、删除、导入、旧入口修改和更新重启分别确认。

分段控件的轨道、点击区和选中底必须使用同一完整段宽，起点包含边框与内边距；不能把缩减后的按钮宽度与未缩减的动画步长混用。轨道显式按内容收口，避免在 Column 中拉满。侧栏选中状态以磨砂底色和文字强调表达，不再画右侧小圆点。`alignment_test.go` 用实际像素定位选中底中心，并检查各页面、三档字号和缩放后的控件边界；尺寸矩阵使用实底以避免重复进行大面积 CPU 磨砂，材质另由真实磨砂像素测试与截图验证。

概览展示最多三份最近可用快照与四条真实活动，可直接进入对应快照。首次使用只提供创建首份快照或先看看，不要求先配置计划与自启。自动备份关闭时隐藏频率字段，草稿与已保存的计划状态分别呈现。更新界面按状态展示下载或重启操作，重启仍需单独确认。Ctrl+1–5 切换主要页面、Ctrl+, 打开设置、Ctrl+R 刷新；模态确认期间不执行这些快捷键。

侧栏、页标题、表格和设置行共享边距与控件高度。`internal/ui/controls.go` 用 MyGo `ButtonBase / SegmentedBase / Table / List` 收口按钮、输入框、分段控件、列表行与表格的薄边、统一控件字阶和即时反馈；字号提供 13/14/16 DIP 三档，旧配置空值按标准档。主工作面只做一次整面模糊，内部表格与输入使用薄透色，不嵌套带阴影的玻璃卡片；普通分组依靠对齐与分隔线。详情查看保留明确入口与完整辅助名称。长列表自行滚动，最小窗口的设置图标与标签保持同一行，不独占一整行。首页主列展示最近快照，窄侧列承接活动；快照统计合并进工具栏，详情保留两个范围的真实状态。全局反馈同一时刻只展开待退出、可操作错误或更新中的一项，其他回执保留底栏入口；旧备份结果不会覆盖后来连接测试或设置保存的回执。

保留 MyGo 原生 `ButtonBase / SearchField / TextInput / NumberInput / SegmentedBase / Table / SplitPercent / Switch / DialogBase` 的交互与可访问语义，由统一控件层接管外观；不引入 WebView 或新的前端框架。导航使用统一的描边 SVG，所有入口都有可访问名称。时间按本地可读格式展示，记录详情保留原始时间；快照和记录可按显示的日期、中文来源和结果搜索。

强调色浅色 `#52697F`、深色 `#718DA8`，辅助字浅色 `#626976`；MiSans 正文默认 14 DIP，可选 13/14/16 DIP。控件高 32 DIP，按钮和分段为胶囊，输入框圆角 10 DIP、分组 12 DIP、导航 Dock 22 DIP、工作面 20 DIP，避免所有层级共用一个半径。应用完整绘制窗口背景，不依赖 Acrylic。MiSans Regular/Medium/Semibold 压缩内嵌，首次绘制注册一次；路径与快捷键有意使用等宽字体，其余界面沿用 MiSans。三个字重均有实际注册回归，许可原文随安装器分发，不单独分发字体软件。

更新流程提供可关闭的全局就绪／结果提示、直达更新设置、可读版本说明、下载字节量与速率、独立校验状态、按错误类型显示的处理入口，以及单独确认的失败版本重试。选择下次打开生效只关闭同版本提示并保留准备包；普通退出不会重新拉起主程序。托盘的版本入口也打开同一更新页面，不自动弹窗。

关键操作期间可请求退出或重启，`View.PendingExit` 表达真实等待意图，`CancelExit` 可撤销；进入提交阶段后禁用取消。重启前发现本地草稿未保存会要求先保存或取消修改。保存回执由递增的 `View.SettingsRevision` 确认，不依赖全局错误是否为空，因此托盘错误不会阻止代理保存回执，凭据保存失败也不能被误判为成功。

## 验证

```powershell
go test ./internal/ui
go vet ./internal/ui
```

无窗口测试使用 MyGo NewTester 覆盖首页入口、真实数据摘要、布局、标签导航、搜索保序、完整复制、取消恢复、过期预览、草稿、反馈优先级和关闭确认。首页导航按 620/780/1180/1440 DIP、浅深主题、100/125/150/200% 渲染缩放检查边界。它们不能代替 Windows 实际显示器 DPI、系统磨砂、托盘和 UAC 验收。

分类切换回归通过辅助技术角色定位实际输入框，避免同名误定位；更新操作按按钮角色精确定位，不能把「自动下载更新」开关误判成下载按钮。字号回归覆盖三档字阶和旧配置空值；深色预览从第一帧按深色启动。材质回归检查真实模糊边界混色、Windows 透明开关前后像素一致、高对比实底和背景层不挡点击；快速导航反向切换不等待或回退。设定 `TOOLNEST_UI_REVIEW_DIR` 可导出 16 张无窗口原生预览，明确在 `ReduceTransparency=true` 下渲染，使用隔离合成数据。边界见 [验证范围](../../docs/validation.md)。

`tests/e2e/Invoke-Background.ps1 -Scenario inspect` 在不切换输入桌面的私有桌面启动测试程序，通过 UIA 操作及 PrintWindow 截图；数据与注册表使用单独临时目录和 `HKCU\Software\ToolNest\Tests\<32hex>`。不得用前台键鼠、真实 PATH 或真实安装目录补齐缺失验收。

0.1.5 的首次引导可选择创建首份快照；跳过后仍有空状态入口。备份／恢复操作提供持久结果及关联快照，UI 的撤回只打开保护快照的新预览，不直接复用旧确认。数据错误由 `View.Problems` 提供处理入口和独立详情；更新回退可显示当前版本与实际启动检查原因，有数据错误时先处理再允许重试。
