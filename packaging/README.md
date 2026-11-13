# ToolNest Windows 包装与发布

## 安全边界

- `ToolNest.exe` 的测试包和正式包都必须带 `-H=windowsgui`，构建后由 `scripts/Assert-WindowsGui.ps1` 检查实际 PE 子系统为 Windows GUI（2）；控制台子系统（3）直接拒绝。后台启动参数不能替代这一检查，否则用户双击仍会附带终端。维护助手和 CLI 不套用 GUI 主程序的要求。
- 读取 GUI 主程序的只读身份使用显式等待及重定向管道，测试构建和后台入口共用 `scripts/Read-BuildInfo.ps1`；不通过 PowerShell 直接管道启动 GUI exe，以免管道提前关闭导致身份缺失。
- 程序固定安装到当前用户 `%LOCALAPPDATA%\Programs\ToolNest`，不提权、不改 PATH。开始菜单和卸载登记仅使用当前用户。没有默认 Run、RunOnce、计划任务或安装后自动启动。
- 卸载只逐个删除程序白名单文件、ToolNest 开始菜单快捷方式和本产品卸载登记。绝不递归删除安装目录，未知文件、`%LOCALAPPDATA%\ToolNest`、旧 PathGuard 数据和所有自定义快照目录均保留；也不读取配置来推测可删除的数据路径。
- 覆盖安装/卸载先运行安装器内嵌的可信维护助手，再获取同一 share=0 文件锁，再次检查进程。发现任意 ToolNest.exe、锁占用、目录重定向或登记归属不明都停止，要求正常退出（含托盘与关键任务）。无 taskkill、强制结束或重启后删除。
- Run 清理由 `RemoveOwnedRun` 完成：名称 ToolNest、REG_SZ、完整绝对 ToolNest.exe 路径、引号与精确 `--autostart` 参数全部匹配才删。Windows 路径允许大小写变化，参数不允许；其他命令、类型、附加参数均保留。删除前重读；Windows 注册表无 compare-and-delete，外部程序并发修改同一值仍存在极窄竞态，不承诺跨进程原子删除。

## 主程序必须绑定的 API

`toolnest/packaging`：

- `Version() string`：唯一版本来源为嵌入的 `packaging/VERSION`，首个公开版本为 0.0.1。
- 链接变量 `Repository`、`UpdatePublicKey`、`BuildChannel` 由 build.ps1 注入。
- `WriteBuildInfo(io.Writer) error`：输出 JSON version/repository/publicKey/channel/runtimeProtocol=1。`--toolnest-build-info` 必须在创建目录、读写配置、联网、UI、注册自启和获取安装锁之前执行并立即退出；不能仅添加身份输出而未实现生命周期合同。

`toolnest/packaging/runtime`（Windows）：

- `DefaultInstallDir() (string, error)` / `ValidateInstallDir(dir string) error`：固定本用户路径，拒绝现有重解析点祖先。
- `AcquireInstallation(dir string) (release func(), err error)`：普通已安装应用应在启动关键工作前获取；**托盘、后台任务、写入、关闭排空期间一直持有**，所有任务结束后最后释放。share=0 与 NSIS 使用同一 `.toolnest-maintenance.lock`；不能只在显示窗口时持有。
- `CheckIdle(dir string) error`：只读进程快照，保守阻止任何同名 ToolNest.exe，兼容尚未使用锁的旧版本。
- `ValidateExisting(dir string) error`：验证路径及现有 HKCU 安装归属，不接管未知非空目录。
- `RunCommand(executable string) (string, error)` / `OwnedRun(RunValue, executable string) bool`：为用户显式启用自启提供统一命令和纯归属判断；本包不自动创建 Run。
- `RemoveOwnedRun(dir string) (bool, error)`：只删除精确归属的 Run；不清理偏好或数据。
- `SyncInstalledVersion(dir, version string) error`：仅在新版本启动确认后更新已验证卸载登记的 DisplayVersion；不得从保留偏好恢复自启。

独立 `packaging/maintenance` 提供 `--mode check-install|check-uninstall|remove-owned-run --install-dir <固定路径>`；只供安装器调度。`check-*` 只读；`remove-owned-run` 会更改真实 HKCU，开发验收不要直接运行。主入口及更新协调须确保更新助手只在应用正常退出、关键写入排空后替换；身份协议本身不是运行时锁接入的形式化证明。当前主程序接入由其他工作流负责，本目录没有修改 main/internal。

## 构建与验证

PowerShell 7.2+；默认 Go 为 `F:/tmp/toolnest-build/go/bin/go.exe`（严格 go1.27.4 windows/amd64），MyGo 为 `F:/tmp/toolnest-build/mygo`，固定 commit `0d644f4aca24be8208d1b7d364d7c928fc2cb890`。不写 go.mod/go.sum/mygo.json，不修改全局环境；GOFLAGS=-mod=readonly，GOWORK=off。默认命令是 test。

```powershell
./scripts/build.ps1 format       # 只检查；平台目录也仅检查
./scripts/build.ps1 format -Fix  # 只格式化 packaging/scripts 自有 Go 文件
./scripts/build.ps1 vet
./scripts/build.ps1 test         # 包含 internal/platform/...
./scripts/acceptance.ps1 -CompileInstaller -MakeNSIS <NSIS3.13/makensis.exe>
```

acceptance 只运行临时目录单元测试和编译安装器；主程序是不可运行 fixture，产物明确名为 TEST-ONLY-DO-NOT-INSTALL.exe，绝不执行。NSIS 编译 UTF8 且警告视为错误。编译器与固定 MyGo 相同：NSIS 3.13，zip SHA256 `ba63dffc4410ee89193e1cb5a41989991bd77c61068da17e3156d136b7b0b3d8`。

测试 key 只能显式生成到仓库外新目录，不覆盖已有目录，不生成生产 key：

```powershell
& F:/tmp/toolnest-build/go/bin/go.exe run -mod=readonly ./scripts/release keygen --test --root F:/github/toolnest --dir F:/tmp/toolnest-test-keys-NEW
$pub = (Get-Content F:/tmp/toolnest-test-keys-NEW/mygo-update.pub -Raw).Trim()
./scripts/build.ps1 release -Test -Repository test-owner/toolnest-test -PublicKey $pub -PrivateKeyFile F:/tmp/toolnest-test-keys-NEW/mygo-update.key -MakeNSIS <makensis.exe> -Output F:/tmp/toolnest-test-release-NEW
./scripts/build.ps1 verify -Test -Repository test-owner/toolnest-test -PublicKey $pub -Output F:/tmp/toolnest-test-release-NEW
```

release 先静态检查已知 main/cmd 接入，再构建真实主程序并以 10 秒期限捕获只读 build-info。不满足入口/身份/版本一致性则失败，不用假主程序冒充正式安装包；超时不强杀，要求正常关闭探针。输出必须仓库外全新目录。测试 channel 不能通过未带 -Test 的 verify。

正式 release 必须有真实匹配 GitHub origin、独立可信公钥、外部 MyGo 格式更新私钥，以及 Authenticode SignTool/证书 thumbprint。私钥仅由仓库外 `-PrivateKeyFile` 或进程环境 `MYGO_UPDATER_PRIVATE_KEY` 提供。禁止将生成的 TEST-ONLY key 用于生产；环境变量的凭据性质由 secret 管理方保证。正式应用、维护助手、内嵌卸载器和安装器签名失败均中止。脚本本身永不上传。

## 产物与签名合同

八个必需文件：ToolNest-<version>-windows-amd64.tar.gz、ToolNest-Setup-<version>-windows-amd64.exe、release-identity.json、update-windows-amd64.json、update-public-key.txt、release-index.json、release-index.json.sig、SHA256SUMS。verify 拒绝缺项、多项、符号链接、篡改、版本/仓库/工具链/channel 不一致和不可信公钥。

更新 tar.gz **恰好两个普通文件**：ToolNest.exe 与 .toolnest-release.json。描述符 version/target/windows-amd64/executable 必须符合 internal/update 现有协议；维护助手不混入更新归档。Ed25519 标准 base64 私钥64字节/公钥32字节，签名是压缩归档原始 SHA256 摘要的 Ed25519 签名再标准 base64，完全匹配固定 MyGo 的 writeSigned/Sign。MyGo CLI 无独立通用签名命令，因此本地工具复用其格式而不导入禁止访问的 internal 包。索引及每个资产也独立签名；verify 公钥必须由调用方提供，绝不信任旁边的公钥文件。

## GitHub 发布门禁

生产打包要求存在 `docs/releases/<版本>.md`，可用 release CLI 的 `--notes-file` 指定等价文件；必须是 UTF-8、匹配版本标题和用户可感知的变化，缺失或占位说明直接拒绝。说明进入已签名资产清单覆盖的更新 manifest；发布 workflow 从复验后的 manifest 写出正文文件，再使用 `--notes-file`，应用与 Releases 显示同一份中文说明。测试 overlay 版本没有对应说明时只生成明确的测试用途说明，不能走生产发布。该流程只修改构建能力，不自动上传任何版本。

仅 workflow_dispatch，已有精确 vX.Y.Z tag；build 输出 commit，publish checkout 同一 commit 并检查 tag 未移动。release-signing environment 保存更新私钥与 PFX；production-release environment 必须 required reviewers、prevent_self_review=true、can_admins_bypass=false。发布 job 在 environment 人工授权后用只读 Administration 权限的外部 `TOOLNEST_ENVIRONMENT_AUDIT_TOKEN` 再查保护规则；无法查询或配置不足均失败。

Variables：TOOLNEST_UPDATE_PUBLIC_KEY。Secrets：TOOLNEST_UPDATE_PRIVATE_KEY、TOOLNEST_AUTHENTICODE_PFX（base64）、TOOLNEST_AUTHENTICODE_PASSWORD、TOOLNEST_ENVIRONMENT_AUDIT_TOKEN。构建只读 contents，只有经授权 publish job 有 contents:write。先完整验证/上传构建资产；人工授权后再次验证签名、hash、size、版本及安装器 Authenticode，创建 draft，重新下载 draft 全资产并再次验证，最后才公开发布；任何步骤失败保留 draft、禁止覆盖已有 release。管理员必须在 GitHub 设置中建立保护规则，本地脚本无法替管理员配置审核。

## 验收边界

本任务允许的验收仅静态检查、Go 临时目录测试、真实编译和测试签名产物生成。**未做真实安装、覆盖、卸载、自启、PATH/Run 变更或生产发布**。真实文件锁/权限/开始菜单/卸载登记/自定义数据保留应在一次性 Windows VM 中由人工按规范验收；正式证书、GitHub environment 审核和远端 draft 发布流程需要外部凭据，未在本机执行。

### 单独授权的公开测试源

2026-11-13 用户另行确认公开 `zxbdzh/toolnest-updates-test` 的两版 TEST-ONLY 更新验收资产，已先草稿上传、全部下载复核哈希与签名，再公开 `v0.1.0`／`v0.1.1`。这不走也不放宽生产 workflow：安装器保持 NotSigned、channel=test、独立测试公钥；源仓库只初始化说明，不推送本地应用源码。详情、当前基线包与未完成的用户重启验收见 `docs/acceptance/2026-11-13.md` 的「真实公开测试更新源」。

公开测试信任身份在 `tests/e2e/TestUpdateSource.json`；`Build-TestVersions.ps1 -PublishBundles` 仅生成八项已验证发布目录，不自动上传。默认真实测试源必须显式复用外部 `-SigningKeyDirectory`，公钥不匹配立即拒绝；不得拿这个密钥做生产签名或在每次重建时悄悄替换。旧占位构建无法自动学会新源／新公钥，需要用户先手动覆盖安装新的 0.1.0 基线。

### 图标与流程

`scripts/Build-BrandAssets.ps1` 从 `internal/brand` 的同一图形生成 16–256 像素多分辨率 ICO、PNG 与 Windows COFF 资源。外部构建工具固定 `github.com/akavel/rsrc@v0.10.2`，只用于构建，不进入应用依赖；正式构建与双版本测试构建都自动执行该步骤。生成的 `internal/brand/brand_windows_amd64.syso` 不提交，普通 `go build` 之前需先执行资源生成，发布脚本会自动处理。

中文 NSIS 向导使用同一 ICO，开始菜单与桌面快捷方式明确指定产品图标。卸载只删除对应快捷方式与白名单程序文件，不递归清理数据。

`Build-ManualUpdateKit.ps1` 生成 0.1.5→0.1.6 独立安装组合。`Installed-Kit-Worker.ps1` 在非输入私有桌面执行真实 NSIS 安装、开始菜单入口、首次启动、升级、恢复合成 PATH、卸载和重装，并对比真实 PATH 未变；该结论只覆盖独立测试安装目录。固定正式安装目录、自启、UAC、Explorer 托盘与物理 DPI 仍单独验收。

## 公开体验版 0.0.1

公开源码和更新使用 `zxbdzh/toolnest`，版本号从 `0.0.1` 开始。此前 0.1.x 是本地开发验收版本，历史记录不作为公开发布历史。

`-PublicPreview` 是显式的公开体验构建模式，`channel=public-preview`。它要求匹配的真实远端和仓库外独立签名私钥，拒绝 TEST-ONLY 密钥；构建及验证均需显式指定该模式。公开体验版不会放宽 production 模式的 Authenticode 要求，安装器在发布说明中标明未做 Windows 代码签名。

```powershell
# 只在首次配置时生成到全新的仓库外私有目录。不要在后续构建中重新生成。
go run ./scripts/release keygen --public-preview --root $PWD --dir '<外部私有目录>'
$public = (Get-Content '<外部私有目录>/mygo-update.pub' -Raw).Trim()
./scripts/build.ps1 -Task release -PublicPreview -Repository zxbdzh/toolnest -PublicKey $public -PrivateKeyFile '<外部私有目录>/mygo-update.key' -Go '<go.exe>' -MyGo '<固定 MyGo 源码>' -MakeNSIS '<makensis.exe>' -Output '<新产物目录>'
./scripts/build.ps1 -Task verify -PublicPreview -Repository zxbdzh/toolnest -PublicKey $public -Go '<go.exe>' -Output '<产物目录>'
```

仅上传经过验证的八项资产。先创建草稿 Release，重新下载并以独立公钥验签，确认标签、版本和清单一致后公开。不要上传隔离测试安装器、私钥、截图、个人快照或本地日志。
