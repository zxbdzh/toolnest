//go:build windows

package toolnest

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"

	"toolnest/internal/core"
	"toolnest/internal/network"
	"toolnest/internal/platform"
	"toolnest/internal/platform/lifecycle"
	"toolnest/internal/update"
	"toolnest/packaging"
	installation "toolnest/packaging/runtime"
)

type environment struct {
	options
	root, executable, installDir string
	publicKey, endpoint          string
	source                       update.Source
	installed                    bool
}

// Run 是主程序唯一入口；提权助手和更新助手不会争抢普通 UI 的单实例锁。
func Run(arguments []string, stdout, stderr io.Writer) int {
	if handled, code, err := platform.HandleHelper(context.Background(), arguments); handled {
		if err != nil {
			fmt.Fprintln(stderr, err)
		}
		return code
	}
	flags, err := parseOptions(arguments, stderr)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		fmt.Fprintln(stderr, err)
		return 2
	}
	if flags.buildInfo {
		if err := packaging.WriteBuildInfo(stdout); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if flags.version {
		fmt.Fprintln(stdout, packaging.Version())
		return 0
	}
	// 手测组合即使被直接双击，也不能退回真实用户目录和真实 PATH。
	if packaging.BuildChannel == "manual-test" && !flags.testing() {
		fmt.Fprintln(stderr, "本地手测版只接受隔离启动，请使用「打开隔离测试」专用入口")
		return 2
	}
	env, err := resolveEnvironment(flags)
	if err == nil && !flags.testing() {
		err = lifecycle.RequireUnelevated()
	}
	if err != nil {
		return startupFailure(flags, stderr, err)
	}
	if flags.diagnostics {
		return diagnostics(env, stdout, stderr)
	}
	if flags.helperFile != "" {
		if err := runUpdateHelper(env); err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		return 0
	}
	if err := runDesktop(env, stdout, stderr); err != nil {
		return startupFailure(flags, stderr, err)
	}
	return 0
}

func resolveEnvironment(flags options) (environment, error) {
	env := environment{options: flags, publicKey: packaging.UpdatePublicKey}
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return env, err
	}
	env.root = filepath.Join(local, "ToolNest")
	env.executable, err = os.Executable()
	if err != nil {
		return env, err
	}
	env.executable = filepath.Clean(env.executable)
	if flags.helperFile != "" {
		root := env.root
		if flags.testing() {
			root = flags.dataDir
		}
		expected := filepath.Join(root, "updates", "helpers", flags.helperToken, filepath.Base(flags.helperTarget))
		if !strings.EqualFold(env.executable, expected) {
			return env, errors.New("更新助手必须从本次私有副本运行")
		}
		env.executable = filepath.Clean(flags.helperTarget)
	}
	env.installDir = filepath.Dir(env.executable)
	installedDir, err := installation.DefaultInstallDir()
	if err != nil {
		return env, err
	}
	env.installed = strings.EqualFold(env.executable, filepath.Join(installedDir, "ToolNest.exe"))
	if flags.testing() {
		for _, protected := range []string{env.root, filepath.Join(local, "PathGuard"), installedDir, env.installDir} {
			if within(flags.dataDir, protected) || within(protected, flags.dataDir) {
				return env, errors.New("隔离数据目录不能与生产数据、旧版数据或程序目录重叠")
			}
		}
		if env.installed && !allowInstalledIsolation(packaging.BuildChannel) {
			return env, errors.New("生产安装路径不接受测试模式，请在隔离目录运行测试 exe")
		}
		env.root = flags.dataDir
	}
	if err := core.CheckDirectory(env.root); err != nil {
		return env, err
	}
	if flags.testing() {
		if err := validateIsolatedSettings(env.root); err != nil {
			return env, err
		}
	}
	if flags.testSource != "" {
		env.source, err = update.HTTPTestSource(flags.testSource)
		if err != nil {
			return env, err
		}
		file, err := os.Open(flags.testKeyFile)
		if err != nil {
			return env, err
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 4097))
		file.Close()
		if readErr != nil || len(data) > 4096 {
			return env, errors.New("测试公钥读取失败或超过大小上限")
		}
		env.publicKey = strings.TrimSpace(string(data))
		key, err := base64.StdEncoding.DecodeString(env.publicKey)
		if err != nil || len(key) != ed25519.PublicKeySize {
			return env, errors.New("测试文件只能包含 base64 Ed25519 公钥，不能包含私钥")
		}
		env.endpoint = flags.testSource
	} else if packaging.Repository != "" && env.publicKey != "" {
		env.source, err = update.GitHub(packaging.Repository)
		if err != nil {
			return env, err
		}
		env.endpoint = "https://api.github.com/repos/" + packaging.Repository + "/releases/latest"
	}
	// 开发 exe 可以离线工作，但未安装路径不参与生产自替换。
	if !env.installed && !flags.testing() {
		env.source = nil
		env.endpoint = ""
	}
	return env, nil
}

func allowInstalledIsolation(channel string) bool {
	if channel != "test" {
		return false
	}
	account, err := lifecycle.CurrentAccount()
	if err != nil {
		return false
	}
	parts := strings.Split(account.Name, `\`)
	// 仅测试构建且真实令牌对应 Sandbox 客体账户才开放安装路径验收，不能只信环境变量。
	return len(parts) > 0 && strings.EqualFold(parts[len(parts)-1], "WDAGUtilityAccount")
}

func validateIsolatedSettings(root string) error {
	for _, filename := range []string{"settings.json", "settings.json.previous"} {
		file, err := os.Open(filepath.Join(root, filename))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		data, err := io.ReadAll(io.LimitReader(file, (1<<20)+1))
		file.Close()
		if err != nil || len(data) > 1<<20 {
			return errors.New("隔离设置读取失败或超过大小上限")
		}
		var config struct{ Settings struct{ SnapshotDir string } }
		if json.Unmarshal(data, &config) != nil {
			return errors.New("隔离设置损坏，拒绝尝试从未知目录恢复")
		}
		if config.Settings.SnapshotDir != "" && !within(root, config.Settings.SnapshotDir) {
			return errors.New("隔离设置的快照目录必须位于本次数据根内")
		}
	}
	return nil
}

func (env environment) updater() (*update.Manager, error) {
	manager, err := network.New(context.Background(), network.Config{Mode: network.Direct}, nil)
	if err != nil {
		return nil, err
	}
	var gate update.Gate = &update.LocalGate{}
	if env.helperFile != "" && env.installed {
		gate = &maintenanceGate{directory: env.installDir}
	}
	return update.New(update.Config{CurrentVersion: packaging.Version(), Source: env.source, PublicKey: env.publicKey,
		InstallDir: env.installDir, StateDir: filepath.Join(env.root, "updates"), Executable: filepath.Base(env.executable), Network: manager, Gate: gate})
}

type maintenanceGate struct {
	gate      update.LocalGate
	directory string
}

func (gate *maintenanceGate) TryAcquire(operation string) (func(), error) {
	release, err := gate.gate.TryAcquire(operation)
	if err != nil {
		return nil, err
	}
	if operation != "update-apply" {
		return release, nil
	}
	maintenance, err := installation.AcquireInstallation(gate.directory)
	if err != nil {
		release()
		return nil, err
	}
	// 替换期间挡住安装/卸载，监督启动前释放，让新版能取得自己的终身维护锁。
	return func() { maintenance(); release() }, nil
}

func runUpdateHelper(env environment) error {
	manager, err := env.updater()
	if err != nil {
		return err
	}
	if manager.Status().Phase == update.Unconfigured {
		return update.ErrNotConfigured
	}
	// RunHelper 自己验证请求路径、令牌并等待父进程；助手不能占用主实例锁。
	err = manager.RunHelper(context.Background(), env.helperFile, env.helperToken, func(executable string, arguments []string) (update.Process, error) {
		confirmed := false
		for _, argument := range arguments {
			confirmed = confirmed || argument == "--toolnest-update-confirm"
		}
		if !confirmed {
			// 回退旧版没有新版令牌，不得把仅限监督启动的参数原样交回旧入口。
			filtered := make([]string, 0, len(arguments))
			for _, argument := range arguments {
				if argument != "--resume-tray" {
					filtered = append(filtered, argument)
				}
			}
			arguments = filtered
		}
		return launchProcess(executable, arguments)
	})
	result := struct {
		Error string `json:"error"`
	}{}
	if err != nil {
		result.Error = err.Error()
	}
	encoded, _ := json.Marshal(result)
	persistErr := core.AtomicWrite(filepath.Join(env.root, "updates", "helper-result.json"), encoded, true)
	return errors.Join(err, persistErr)
}

func startupFailure(flags options, output io.Writer, err error) int {
	fmt.Fprintln(output, "ToolNest:", err)
	if !flags.diagnostics && !flags.smoke && flags.helperFile == "" && !flags.testing() {
		message, _ := windows.UTF16PtrFromString("ToolNest 无法启动：\n" + err.Error())
		title, _ := windows.UTF16PtrFromString("ToolNest")
		_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
	}
	return 1
}

func diagnostics(env environment, stdout, stderr io.Writer) int {
	registry, err := platform.NewRegistry(platform.Options{TestRoot: env.testRoot})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	state, err := registry.Read(context.Background())
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	manager, err := env.updater()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer token.Close()
	result := map[string]any{"version": packaging.Version(), "channel": packaging.BuildChannel, "dataDir": env.root,
		"testRoot": env.testRoot, "installed": env.installed, "elevated": token.IsElevated(), "update": manager.Status(),
		"user":             map[string]any{"exists": state.User.Exists, "type": state.User.TypeName(), "bytes": len(state.User.Bytes)},
		"system":           map[string]any{"exists": state.System.Exists, "type": state.System.TypeName(), "bytes": len(state.System.Bytes)},
		"defaultAutostart": false, "defaultSchedule": false, "runtimeProtocol": 1}
	if err := json.NewEncoder(stdout).Encode(result); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func updateBlocked(root, version string) (bool, error) {
	data, err := os.ReadFile(filepath.Join(root, "updates", "blocked.json"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || len(data) > 4096 {
		return true, errors.New("更新阻止记录不可读，保留当前版本")
	}
	var blocked struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal(data, &blocked); err != nil {
		return true, err
	}
	return blocked.Version == version, nil
}

func helperArguments(env environment, hidden bool) []string {
	arguments := env.runtimeArguments()
	if env.autostart {
		arguments = append(arguments, "--autostart")
	}
	if hidden {
		arguments = append(arguments, "--resume-tray")
	}
	if env.smoke {
		arguments = append(arguments, "--smoke")
	}
	return arguments
}

func startUpdateHelper(env environment, manager *update.Manager, hidden bool) error {
	if prepared := manager.Prepared(); prepared == nil {
		return update.ErrNotReady
	} else if blocked, err := updateBlocked(env.root, prepared.Version); blocked || err != nil {
		return errors.Join(update.ErrBlocked, err)
	}
	command, err := manager.HelperCommand(context.Background(), helperArguments(env, hidden))
	if err != nil {
		return err
	}
	token := strings.TrimSuffix(strings.TrimPrefix(filepath.Base(command.RequestFile), "helper-"), ".json")
	if !tokenID(token) {
		return update.ErrArchive
	}
	staging := filepath.Join(env.root, "updates", "helpers", token)
	if err := core.CheckDirectory(staging); err != nil {
		return err
	}
	if err := os.MkdirAll(staging, 0700); err != nil {
		return err
	}
	helper := filepath.Join(staging, filepath.Base(env.executable))
	if err := copyHelper(env.executable, helper); err != nil {
		return err
	}
	arguments := append(env.runtimeArguments(), "--toolnest-update-target", env.executable)
	arguments = append(arguments, command.Arguments...)
	process, err := launchProcess(helper, arguments)
	if err != nil {
		return err
	}
	go process.Wait()
	return nil
}

func copyHelper(source, destination string) error {
	input, err := os.Open(source)
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0700)
	if err != nil {
		return err
	}
	_, err = io.Copy(output, input)
	if err == nil {
		err = output.Sync()
	}
	return errors.Join(err, output.Close())
}

type childProcess struct{ command *exec.Cmd }

func (child childProcess) Wait() error { return child.command.Wait() }
func (child childProcess) Kill() error { return child.command.Process.Kill() }
func launchProcess(executable string, arguments []string) (update.Process, error) {
	command := exec.Command(executable, arguments...)
	command.Dir = filepath.Dir(executable)
	// 只抑制控制台；HideWindow 会把 SW_HIDE 传给新版首个窗口，导致更新已确认但主界面不可见。
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	if err := command.Start(); err != nil {
		return nil, err
	}
	return childProcess{command: command}, nil
}
