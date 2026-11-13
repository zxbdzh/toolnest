# 本轮验证记录

环境：2026-11-12，Windows amd64，Go 1.27.4，`CGO_ENABLED=0`。未更改真实 PATH、系统代理、自启；未发布；所有生产代码和文档改动局限 `internal/network/**`、`internal/update/**`。

使用命令：

```powershell
& gofmt.exe -w internal\network internal\update
& go test -timeout 90s -count=1 -cover ./internal/network ./internal/update
& go test -count=5 -timeout 120s ./internal/network ./internal/update
& F:\tmp\toolnest-build\go\bin\go.exe vet ./internal/network ./internal/update
& F:\tmp\toolnest-build\go\bin\go.exe test -timeout 90s ./internal/app
```

已实际通过：两个包的 `go test`、连续五遍重复测试、`go vet`，及只读调用的 `internal/app` 接口接入测试。实际 Credential Manager 独立测试条目往返、真实 Windows 文件共享占用、真实旧父进程等待、签名归档中的真实 Go 测试 exe 启动/握手及启动失败回退均在本机执行，而不只是 mock。

最终增补回归后再次执行单遍覆盖率测试、vet 和 app 测试均通过：`internal/network` 82.5%，`internal/update` 70.8%。五遍重复测试在增补最终崩溃断点与受阻版本回归前执行，未把它误记为最终代码五遍复跑。新增回归也验证：失败版本不会残留为下次启动自动应用，明确手动检查并重新准备后才解除该版本阻止。

`go test -race` 已尝试，但工具链报告 `-race requires cgo; enable cgo by setting CGO_ENABLED=1`，且当前未发现 gcc/clang；因此 **未通过/未验证 race detector**，未为了测试改变项目无 cgo 约束。

未宣称完成的整机验收：NSIS 安装/卸载、真实 UI/托盘/计划与快照续接、完整 AT-18 已安装版本升级、企业系统 WinHTTP/PAC/WPAD、生产源/公钥、Authenticode、真实磁盘满/ACL 权限不足、掉电。占用已真实测试，权限和换入/回退失败通过定点文件系统故障注入验证；中断 rename 阶段通过真实临时文件重建崩溃状态验证。

MyGo 的内置差分/归档 verifier 在固定源码的 `internal/update`，不可从 ToolNest 导入；默认安全回退完整签名包，尚未提供官方差分成功 adapter。详见 `README.md` 的包格式、限制及 main 接入顺序，不可把这条已知限制描述成完整差分引擎已实现。
