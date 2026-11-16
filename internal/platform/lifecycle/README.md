# lifecycle：Windows 生命周期与旧入口适配

本子包独立于 `internal/platform` 根包，不写 PATH、不创建计划、不接管 UI 关闭偏好，不自动开启 Run。本次只变更 `internal/platform/lifecycle/**`，没有修改 main/UI/go.mod。

## 依据

- `docs/specs/toolnest-v1.md` §6/迁移条款。
- 固定 MyGo 源码 `F:/tmp/toolnest-build/mygo`，提交 `0d644f4aca24be8208d1b7d364d7c928fc2cb890`。
- 复用 `mygo.NewTray`、`NewMenu`、`MenuItem`、`Tray.Bounds/SetIcon/SetToolTip/Destroy`、`RunOnMain`。固定版本托盘创建不直接报告 Shell_NotifyIcon 注册失败，因此以实际 Bounds 查询作正向验证；返回空矩形即保守显窗。
- 不复用 MyGo 自带单实例锁：该实现使用临时 Unix socket、失败时可能放行，不能给出本包要求的 Windows SID 隔离和失败拒绝语义。
- 旧版仅参考 `F:/github/path-guard/src/desktop.rs`（Run `PathGuard`、`--autostart`）和 `backend.rs`（`PathGuard-PATH-Backup-<SID>`、`--backup`、完整版本标记）。没有扫描安装目录或任务列表。

## main/UI 接线顺序（本次未改 main）

1. **最早处理根包受限 helper 入口**：调用主线程提供的 `HandleHelper`（具体签名以根包为准）；已处理则返回。helper 不进入 UI、单实例和调度器。未知/不合法 helper 参数应由 main 拒绝，不能当普通 UI 启动。
2. 正常入口调用 `RequireUnelevated()`。如果被管理员身份启动，返回清楚错误，不创建管理员 UI、不代替用户提权或任意降权重启。
3. 建立 `UIQueue` 与 `Controller`，Hooks 闭包可以捕获尚未创建的窗口，但必须等 UI ready 才执行。获取 `AcquireInstance` 后只有主实例才能创建调度器、窗口；`secondary == true` 直接返回；任何 error 也必须返回，**不能失败后继续启动第二实例**。
4. `App.OnReady` 中创建窗口、绑定 Hooks，运行 `Controller.Start`，再 `queue.Ready()`。UI 决定初次可见性，避免启动先闪现后隐藏。手动启动始终显窗；自启只在用户勾选 startInTray 且验证托盘存在时隐藏。
5. 主窗口关闭询问/记住/取消由 UI 实现；选择隐藏时调用 `Controller.Hide()`，不要直接 `window.Hide()`。退出调用应用忙碌协调器，安全完成关键写入后退出。不从托盘直接强杀进程。
6. 真正退出时，在 UI 事件循环仍可调度期间 `Controller.Close()`，再 `UIQueue.Close()`，主进程退出最后释放 `Instance.Close()`。不要窗口隐藏时释放实例锁。

下面是接线片段，窗口创建、用户偏好与业务回调由现有 main/UI 提供（不是新增 main）：

```go
// 受限 helper 必须在此之前分流；这里已经是正常桌面入口。
if err := lifecycle.RequireUnelevated(); err != nil {
    return err
}
queue := lifecycle.NewUIQueue(lifecycle.MyGoPost)
var window *mygo.Window // 在 App.OnReady 中赋值
controller, err := lifecycle.NewController(lifecycle.Hooks{
    Post: queue.Post,
    ShowMain: func() {
        window.Show()
        window.Restore() // 包括最小化的已有窗口
        window.Focus()
    },
    HideMain: func() { window.Hide() },
    OpenSettings: uiOpenSettings,  // 轻量 UI 操作
    BackupNow: enqueueManualBackup, // 入队，不能阻塞 UI
    RequestExit: requestSafeExit,   // 等待/取消退出；不强杀正在写入的操作
    ReportError: showInAppError,    // 应用内错误与日志
})
if err != nil { queue.Close(); return err }
launch := lifecycle.ParseLaunch(os.Args[1:])
instance, secondary, err := lifecycle.AcquireInstance(
    ctx, "ToolNest", launch, controller.SecondLaunch,
)
if err != nil || secondary { queue.Close(); return err }
defer instance.Close()
mygo.App.OnReady(func() {
    window = createExistingMainWindow()
    controller.Start(launch, savedStartInTray, lifecycle.NewMyGoTrayFactory(
        lifecycle.MyGoTrayOptions{NormalPNG: normalIcon, ErrorPNG: errorIcon},
    ))
    queue.Ready()
})
// 完成关键工作、准备真正退出时，在 UI 线程执行：
// controller.Close() // 此时 queue 仍可用
// queue.Close()
```

`ShowMain` 必须真正唤回/恢复/聚焦，不能只把菜单设为可见。`AcquireInstance` 通过命名管道服务器/客户端 PID token 验证相同 SID，并把本次前台权限授予该服务器 PID；手动唤回需确认握手。第二次自启静默返回，不发送显窗事件。全局 SID 命名 mutex 使同一账号跨会话也不能重复建调度器；Windows 不允许把其他交互会话的窗口搬到当前会话，跨会话只能保持单实例，不能保证在当前桌面显窗。

## Hooks 与主要 API

- `Hooks{Post, ShowMain, HideMain, OpenSettings, BackupNow, RequestExit, ReportError}`：全部必填；Post 必须排队、不阻塞。`UIQueue.Post/Ready/Close` 处理首个窗口创建前的唤回。
- `ParseLaunch([]string) Launch`：仅严格的单参数 `--autostart` 为自启；其他 helper/更新模式必须由 main 事先分流。
- `AcquireInstance(ctx, namespace, launch, onLaunch) (*Instance, secondary bool, error)`；固定 namespace 不随版本/安装路径变化；回调必须迅速返回，推荐 `controller.SecondLaunch`。
- `NewController(Hooks)`；`Start` 仅 UI ready 后 UI 线程调用。`Hide`、`SetStatus`、`SecondLaunch`、`HandleAction` 可排队；`ProbeTray` 是 UI 线程方法。
- 托盘提供显示主窗口/立即备份/设置/退出、运行/错误文字和两套图标。控制器每 2 秒探测图标，运行中暂时失效只记录一次错误，不显示或聚焦窗口；手动二次启动仍可唤回。初次启动失败或用户请求隐藏时不可用，才保留可见入口。桌面适配在恢复后重发状态，并仅清除托盘自己的错误。自动回归覆盖失联、更新状态失败和恢复；物理息屏与 Explorer 托盘仍需真实设备验收。
- `CurrentAccount()` 返回 token SID 和可解析的域账号；`RequireUnelevated()` 是拒绝管理员 UI 的 guard，不是提权/降权执行器。

## Run：默认关闭、精确归属与撤销

```go
account, err := lifecycle.CurrentAccount()
if err != nil { return err }
store, err := lifecycle.NewWindowsRunStore()
if err != nil { return err }
journal, err := lifecycle.NewFileJournal(undoDirectory) // 用户数据目录中的独立目录
if err != nil { return err }
exe, err := os.Executable()
if err != nil { return err }
startup, err := lifecycle.NewRunManager(store, journal, account, exe)
if err != nil { return err }
enabled, err := startup.Enabled(ctx) // 只读，不默认写任何自启项
_ = enabled
if err != nil { return err }
// 仅在用户明确保存“启用/关闭开机自启”后调用：
change, err := startup.SetEnabled(ctx, requestedEnabled)
if err != nil { /* 保留 change.BackupID；刷新状态并报告错误 */ return err }
// 需要撤销时读取持久备份；记录不存在/状态变化就中止，不覆盖后来修改。
record, err := journal.Load(ctx, change.BackupID)
if err != nil { return err }
_, err = startup.Undo(ctx, record)
return err
```

- 正常 store 固定为 HKCU `Software\Microsoft\Windows\CurrentVersion\Run`，只允许 `ToolNest`/已知 `PathGuard` 值名，不碰 HKLM。构造和 Enabled 不写键。
- `RunManager` 只管理 ToolNest。归属同时要求 **REG_SZ、绝对 exe 路径、完整参数向量 `--autostart`**；Windows 路径大小写不敏感，不扩展环境变量、不把 REG_EXPAND_SZ 当成自有值。未知同名命令返回 `ErrNotOwned`，不能删除/覆盖。
- `RunValue` 保留 Exists/Type/原始 UTF-16 字节；备份先持久落盘，写前重读、条件替换、写后核对。撤销只接受账号一致且当前值仍等于预期结果。
- Windows 注册表没有此处可用的单值跨进程原子 CAS：同进程持锁、紧邻写前重读和写后核对能发现常规并发修改，**不承诺抵抗其他进程恰好插入检查与写入之间的微小竞态**。

## 旧 PathGuard：明确已知项、确认后禁用

```go
// knownOldExecutables 来自已确认的旧安装配置/用户选择，不遍历磁盘推断。
policy := lifecycle.KnownLegacyPolicy(account, knownOldExecutables)
tasks, err := lifecycle.NewWindowsTaskStore(knownOldExecutables)
if err != nil { return err }
legacy, err := lifecycle.NewLegacyManager(store, tasks, journal, policy)
if err != nil { return err }
entries, err := legacy.Detect(ctx)
if err != nil { return err }
// UI 显示 entries 的归属/完整路径/参数/账号，用户明确确认目标后：
change, err := legacy.Disable(ctx, confirmedEntry)
if err != nil { /* 即使返回错误，也保留 change.BackupID 供人工检查 */ return err }
record, err := journal.Load(ctx, change.BackupID)
if err != nil { return err }
_, err = legacy.Undo(ctx, record) // 稍后显式撤销；从不自动覆盖后改
return err
```

- Detect 只读取 policy 中明确给定且允许的名称：Run `PathGuard`、根任务 `PathGuard-PATH-Backup-<当前 SID>`。账号一致还必须有用户提供的已知旧 exe 绝对路径，Run 精确 `--autostart`；任务精确单 Exec、`--backup`、SID/账号、Author、Source、Description、LogonType=3（InteractiveToken）、RunLevel=0。版本标记为 `PathGuard/RegistryPathBackup/v1/49b84af9-b152-49f9-9de1-6ee0d93adfd6`。不按名字单独认领。
- 未知/缺失项不会改动；没有安装路径依据则不能构建迁移管理器。迁移不启用新 Run/计划，不结束旧进程，也不删除任务。
- `WindowsTaskStore` 只运行内嵌固定 `tasks.ps1`，仅 `read`/`toggle` 操作。系统绝对 PowerShell 路径、固定 EncodedCommand、JSON stdin 数据、输出上限、12 秒超时；不暴露任意脚本/命令 API，不调用 Invoke-Expression、不执行备份 exe，不接受 XML 导入/注册任务。
- 真正切换前固定脚本再次独立验证当前 SID、完整归属及预览 XML/Enabled；只改 Enabled。完整 XML 和状态变更前经 Journal 保存。重启后可从原备份撤销；只忽略 Settings/Enabled 文本差异，其他定义变化均拒绝。
- Task Scheduler COM 也没有此接口可用的跨进程 CAS；外部写入发生在最后重读与 Enabled 赋值之间时无法保证原子性，写后验证发现不一致即报错、保留备份，**不盲目回滚覆盖外部更新**。
- FileJournal 随机 ID、O_EXCL、Sync、读回校验，拒绝路径穿越/可见目录链接。Windows 的 0600 是权限意图，不替代应用数据目录的实际用户 ACL；调用方必须选择当前用户私有数据目录。同账号恶意进程不是安全边界。无操作/已处于目标状态时 `Changed=false`，没有 BackupID，UI 不应提供空记录撤销按钮。

## 隔离验收与未验收项

执行环境：`F:/tmp/toolnest-build/go/bin/go.exe`，`go version go1.27.4 windows/amd64`。

已实际运行：

```text
go test -count=1 -timeout=45s -cover ./internal/platform/lifecycle
PASS，coverage: 70.4%（真实托盘/任务修改分支有意不执行）
go vet ./internal/platform/lifecycle
PASS，无输出
go test -count=8 -run TestWindowsInstanceManualWakeAndSilentAutostart -timeout=60s ./internal/platform/lifecycle
PASS
go test -race -count=1 -timeout=60s ./internal/platform/lifecycle
未执行测试：go: -race requires cgo; enable cgo by setting CGO_ENABLED=1
```

19 个顶层测试覆盖：启动可见性/静默二次自启/手动唤回、托盘工厂失败及丢失回退、UI ready 前排队、Run 默认关闭/类型与路径参数归属/备份失败与竞态/后改保护、旧任务多维归属/禁用与持久记录撤销/定义后改保护、日志落盘与 ID 限制，以及 Windows 随机单实例命名空间。

Windows 注册表测试只创建随机 `HKCU Software\ToolNest\Tests\Lifecycle-<random>` 并清理**该随机子键**；从不调用真实 Run store。任务测试只用 fake storage，并用 PowerShell Parser.ParseInput 解析脚本（显式 UTF-8），不执行脚本、不连接任务 COM。**没有修改真实 PATH、Run、TaskScheduler，没有运行真实托盘/自启/任务禁用。**

剩余集成验收：main/helper 分流接线、实际 MyGo 窗口与关闭偏好、普通权限 guard、两套图标资源、用户数据 ACL、Windows Explorer 重启/溢出托盘/前台焦点、已授权的真实 Run/旧任务读改撤销与并发边界。未实施部分不能宣称端到端已完成。
