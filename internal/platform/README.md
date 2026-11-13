# Windows 平台边界与接入

本目录实现 `core.Registry`，不修改 main/core/UI，也不自动写入真实 PATH、Run 或计划任务。生命周期见 `lifecycle/`，安装互斥与构建协议见 `../../../packaging/README.md`。

## main 最早分流

必须先处理只读 build-info 与受限 maintenance 命令（见 packaging），然后在单实例、设置、网络、任务、托盘初始化前调用：

```go
if handled, code, err := platform.HandleHelper(context.Background(), os.Args[1:]); handled {
    // 仅记录错误到当前 stderr；助手不初始化主 UI/调度器。
    if err != nil { fmt.Fprintln(os.Stderr, err) }
    os.Exit(code)
}
```

`HandleHelper` 非助手启动返回 `(false, 0, nil)`。助手失败返回非零 code；调用者必须立即退出，不可忽略错误后继续普通启动。助手参数协议是私有入口，不得从用户文本拼接 shell 命令。正常主程序保持 asInvoker。

## 恢复 API

```go
registry, err := platform.NewRegistry(platform.Options{})
if err != nil { return err }
service := &core.Service{Registry: registry, Store: store, Now: time.Now}
plan, err := service.Preview(ctx, snapshotID, []core.Scope{core.User, core.System})
if err != nil { return err }
// UI 展示 plan.Before、plan.Target、Changes、Scopes 并等待明确确认。
// 此后不可再改变这个 plan；用户拒绝则直接返回。
if err := registry.ConfirmRestore(plan); err != nil { return err }
result := service.Restore(ctx, plan)
// 分别展示 result.Outcome、Error、NotificationError、ProtectionID。
```

导出 API：`Options{TestRoot string}`、`NewRegistry(Options) (*Registry,error)`、`Registry.ConfirmRestore(core.RestorePlan) error`、`Read/Authorize/Write/Notify`、`HandleHelper(context.Context, []string) (bool,int,error)`；错误有 `ErrNotConfirmed/ErrUnauthorized/ErrCancelled/ErrUnsupported`。

`ConfirmRestore` 是必需桥接：core.Registry.Authorize 只携带范围而没有目标值。它冻结确认令牌、原始 SID、范围和前后精确值，一次消费。只允许每个已确认范围写目标一次，然后必要时回滚到 before 一次；不能任意 PATH 值或重复使用旧确认。core.Service 负责授权后重读、保护快照落盘、逐范围写入/读回、逆序回滚和通知；不能绕过它调用 Write 做恢复。

## 安全与保真

- 只读打开 `HKU/<调用者 SID>/Environment`（与其 HKCU 对应）和固定 HKLM Environment；不创建键、不初始化缺值、不展开变量。缺值、类型、异常 UTF-16、零长度及原始 bytes 都保留。非 core 支持的 PATH 类型报错，不悄悄转型。
- 固定原始用户 SID，不因 UAC 使用其他管理员凭据而切换用户目标。用户 PATH 由普通进程写；只有请求系统范围时才 runas 受限助手。取消 UAC/助手确认/最终授权阶段会使整个事务零写入。
- 助手双方核对 named-pipe 对端 PID；父核对 ShellExecute 返回进程、管理员 token；助手核对活动调用者、同程序路径/文件 SHA-256、caller token SID、启动摘要与已冻结请求。管道仅本机、随机名、first-instance 和限定 DACL。高权限助手作为客户端显式指定 `SECURITY_SQOS_PRESENT | SECURITY_IDENTIFICATION`，不把可模拟的管理员 token 交给父进程；确认后与每次写前复查调用者存活，退出即撤销会话。
- 助手再确认范围、SID、令牌、摘要与类型/长度；只接受固定系统 Path 的目标与必要回滚，拒绝任意命令、任意键、未知字段、超大或尾随消息。会话最长五分钟，父退出/关闭通道撤销能力；该期限外不保证继续回滚。
- 用 `NtSetValueKey` 的明确 DataSize 保真写入。隔离测试曾复现 `RegSetValueExW` 为缺终止符的 REG_EXPAND_SZ 补零，不能用字符串 setter 替代。写前重读发现外改立即停止，但注册表 compare/read/write **不是跨进程原子 CAS**；不能承诺消除最后一个系统调用窗口的竞争。
- 通知用 WM_SETTINGCHANGE/Environment，失败独立报告，不把已写成功伪装成未发生。

## 隔离测试

```powershell
& go test -count=1 -v ./internal/platform
& go vet ./internal/platform
```

`Options{TestRoot: core.NewID()}` 将两个范围都限制到 `HKCU/Software/ToolNest/Tests/<32位小写hex>/{user,system}`，不会广播桌面环境变化，不请求 UAC。NewRegistry/Read 不创建此测试路径；测试夹具自己创建并清理自己的随机键。参数不允许绝对/相对任意注册表路径。

已包含原始bytes/类型 roundtrip、仅 Path 不动邻值、读缺键不创建、未确认/错范围/取消零写入、不可变确认、重放拒绝、外改停止、协议越权与异常帧、原生 overlapped 管道、core 保护快照/失败回滚集成测试。所有 Windows 自动测试仅使用专用测试键与临时文件。

## 必须由集成方完成的真实验收

main 必须按上述入口和 packaging/lifecycle 文档绑定；本任务按目录限制没有擅改 main/UI。自动测试不替代隔离 Win11 VM 的实际 UAC（包括另一管理员与两阶段取消）、标准用户 HKLM 失败路径、桌面广播/Explorer、托盘重建、登录自启和安装/升级/卸载验收。没有运行安装器、卸载器、真实自启、真实 PATH 修改或生产发布。未完成真实验收前不得标记生产就绪。
