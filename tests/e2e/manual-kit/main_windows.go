//go:build windows

// 本入口只随手测组合分发，不是正式 ToolNest 的启动器或更新服务。
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"

	"toolnest/internal/app"
	_ "toolnest/internal/brand"
	"toolnest/internal/core"
	"toolnest/internal/platform/lifecycle"
	"toolnest/tests/e2e/lab"
)

var kitID, trustedPublicKey string

type kitConfig struct {
	ID, Baseline, Target, PublicKey, Notes string
	BaselineSHA256, TargetSHA256           string
}

type locations struct {
	program, data, source string
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		// 只在用户主动点击启动器后显示错误；只读检查、安装预检与后台源不会弹窗。
		if len(os.Args) == 1 || len(os.Args) == 2 && os.Args[1] == "--open" {
			message, _ := windows.UTF16PtrFromString(err.Error())
			title, _ := windows.UTF16PtrFromString("ToolNest 更新流程测试")
			windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0, uintptr(unsafe.Pointer(message)), uintptr(unsafe.Pointer(title)), 0x10)
		}
		os.Exit(1)
	}
}

func run(arguments []string) error {
	flags := flag.NewFlagSet("ToolNest-TestLauncher", flag.ContinueOnError)
	open := flags.Bool("open", false, "用户主动打开隔离测试应用")
	serve := flags.Bool("serve", false, "内部本地更新源")
	stop := flags.Bool("stop", false, "停止本次本地源，不关闭应用")
	check := flags.Bool("check-maintenance", false, "安装与卸载前检查，只处理本组合")
	verify := flags.Bool("verify-assets", false, "只读核验，不启动应用或服务")
	mode := flags.String("set-mode", "", "good, source-not-found, http-error, bad-signature")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("不接受额外参数")
	}
	actions := 0
	for _, selected := range []bool{*open, *serve, *stop, *check, *verify, *mode != ""} {
		if selected {
			actions++
		}
	}
	if actions > 1 {
		return errors.New("操作必须互斥")
	}
	paths, err := resolveLocations()
	if err != nil {
		return err
	}
	if *check {
		if err := checkIdle(paths); err != nil {
			return err
		}
		return stopSource(paths)
	}
	if *stop {
		return stopSource(paths)
	}
	if *mode != "" {
		if !validMode(*mode) {
			return errors.New("不支持的故障模式")
		}
		if err := os.MkdirAll(paths.source, 0700); err != nil {
			return err
		}
		return core.AtomicWrite(filepath.Join(paths.source, "mode.txt"), []byte(*mode), true)
	}
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	config, archive, signature, err := readAssets(filepath.Dir(executable))
	if err != nil {
		return err
	}
	if *verify {
		return json.NewEncoder(os.Stdout).Encode(map[string]any{
			"verified": true, "id": kitID, "baseline": config.Baseline, "target": config.Target,
			"installDirectory": paths.program, "dataDirectory": paths.data, "archiveBytes": len(archive),
		})
	}
	// 从解压目录双击 ToolNest.exe 会丢失隔离参数；唯一公开快捷方式始终经过此门禁。
	if !strings.EqualFold(filepath.Dir(executable), paths.program) {
		return errors.New("请先安装「更新流程测试版」，再从其专用开始菜单入口打开")
	}
	if *serve {
		return serveSource(paths, config, archive, signature)
	}
	release, err := maintenanceLock(paths.program)
	if err != nil {
		return err
	}
	defer release()
	if err := verifyApplication(filepath.Join(paths.program, "ToolNest.exe"), config); err != nil {
		return err
	}
	if err := initialize(paths); err != nil {
		return err
	}
	connection, err := ensureSource(paths, executable)
	if err != nil {
		return err
	}
	command := exec.Command(filepath.Join(paths.program, "ToolNest.exe"),
		"--data-dir", paths.data, "--test-root", kitID,
		"--test-update-source", connection.Feed,
		"--test-update-key-file", filepath.Join(paths.program, "update.pub"))
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	return command.Start()
}

func resolveLocations() (locations, error) {
	var paths locations
	if !regexp.MustCompile(`^[a-f0-9]{32}$`).MatchString(kitID) {
		return paths, errors.New("缺少固定的隔离组合标识")
	}
	local, err := windows.KnownFolderPath(windows.FOLDERID_LocalAppData, windows.KF_FLAG_DEFAULT)
	if err != nil {
		return paths, err
	}
	paths.program = filepath.Join(local, "Programs", "ToolNest-UpdateTest-"+kitID)
	paths.data = filepath.Join(local, "ToolNest-UpdateTest", kitID, "data")
	paths.source = filepath.Join(local, "ToolNest-UpdateTest", kitID, "source")
	for _, path := range []string{paths.program, paths.data, paths.source} {
		if err := noRedirect(path); err != nil {
			return paths, err
		}
	}
	return paths, nil
}

func noRedirect(path string) error {
	for current := filepath.Clean(path); ; current = filepath.Dir(current) {
		pointer, err := windows.UTF16PtrFromString(current)
		if err != nil {
			return err
		}
		attributes, err := windows.GetFileAttributes(pointer)
		if err == nil && attributes&windows.FILE_ATTRIBUTE_REPARSE_POINT != 0 {
			return errors.New("隔离目录不允许 junction、符号链接或其他重定向")
		}
		if err != nil && !errors.Is(err, windows.ERROR_FILE_NOT_FOUND) && !errors.Is(err, windows.ERROR_PATH_NOT_FOUND) {
			return err
		}
		if filepath.Dir(current) == current {
			return nil
		}
	}
}

func readAssets(directory string) (kitConfig, []byte, string, error) {
	var config kitConfig
	for _, name := range []string{"kit.json", "update.pub", "update.tar.gz", "update.sig"} {
		if err := noRedirect(filepath.Join(directory, name)); err != nil {
			return config, nil, "", err
		}
	}
	data, err := os.ReadFile(filepath.Join(directory, "kit.json"))
	if err != nil {
		return config, nil, "", err
	}
	version := regexp.MustCompile(`^0\.1\.[0-9]+$`)
	if len(data) > 1<<20 || json.Unmarshal(data, &config) != nil || config.ID != kitID || config.PublicKey != trustedPublicKey ||
		!version.MatchString(config.Baseline) || !version.MatchString(config.Target) || config.Baseline == config.Target {
		return config, nil, "", errors.New("组合身份或版本配置无效")
	}
	publicText, err := os.ReadFile(filepath.Join(directory, "update.pub"))
	if err != nil || strings.TrimSpace(string(publicText)) != trustedPublicKey {
		return config, nil, "", errors.New("公钥与组合的固定信任身份不符")
	}
	archive, err := os.ReadFile(filepath.Join(directory, "update.tar.gz"))
	if err != nil {
		return config, nil, "", err
	}
	signatureText, err := os.ReadFile(filepath.Join(directory, "update.sig"))
	if err != nil {
		return config, nil, "", err
	}
	public, err := base64.StdEncoding.DecodeString(trustedPublicKey)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return config, nil, "", errors.New("组合公钥无效")
	}
	signature := strings.TrimSpace(string(signatureText))
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	digest := sha256.Sum256(archive)
	if err != nil || !ed25519.Verify(public, digest[:], signatureBytes) {
		return config, nil, "", errors.New("手测更新归档验签失败")
	}
	return config, archive, signature, nil
}

func verifyApplication(path string, config kitConfig) error {
	if err := noRedirect(path); err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	identity := hex.EncodeToString(digest[:])
	if identity != config.BaselineSHA256 && identity != config.TargetSHA256 {
		return errors.New("应用不是本组合的任一已验证版本")
	}
	return nil
}

func initialize(paths locations) error {
	for _, directory := range []string{paths.data, paths.source} {
		if err := os.MkdirAll(directory, 0700); err != nil {
			return err
		}
	}
	// 示例 PATH 完全位于测试子树；反复打开和重装均保留用户在隔离体验中做的修改。
	for scope, text := range map[string]string{"user": `C:\ToolNestDemo\Git\cmd;C:\ToolNestDemo\Node;%USERPROFILE%\ToolNestDemo\bin`, "system": `C:\ToolNestDemo\Windows;C:\ToolNestDemo\Windows\System32`} {
		key, _, err := registry.CreateKey(registry.CURRENT_USER, `Software\ToolNest\Tests\`+kitID+`\`+scope, registry.QUERY_VALUE|registry.SET_VALUE|registry.WOW64_64KEY)
		if err != nil {
			return err
		}
		_, _, readErr := key.GetValue("Path", nil)
		if errors.Is(readErr, windows.ERROR_FILE_NOT_FOUND) {
			readErr = key.SetExpandStringValue("Path", text)
		}
		key.Close()
		if readErr != nil {
			return readErr
		}
	}
	return initializeData(paths)
}

func initializeData(paths locations) error {
	filename := filepath.Join(paths.data, "settings.json")
	for _, existing := range []string{filename, filename + ".previous"} {
		if _, err := os.Stat(existing); err == nil {
			return nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	settings := app.Settings{Theme: "light", CloseBehavior: "ask", StartInTray: true, OnboardingDone: false,
		SnapshotDir: filepath.Join(paths.data, "snapshots"), ScheduleMode: "interval", IntervalHours: 6, DailyHour: 9,
		ProxyMode: "direct", AutoCheck: false, AutoDownload: false}
	// 预置设置会让应用跳过首次初始化，因此必须先创建对应空目录；已有配置则不重建失效目录。
	if err := (core.Store{Directory: settings.SnapshotDir}).Ensure(); err != nil {
		return err
	}
	data, err := json.MarshalIndent(map[string]any{"version": 1, "settings": settings, "schedule": core.Schedule{Mode: "interval", Hours: 6, Hour: 9}}, "", "  ")
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filename, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(data)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	return closeErr
}

type sourceConnection struct {
	Feed, PublicKey string
}

func healthy(paths locations) (sourceConnection, bool) {
	var connection sourceConnection
	data, err := os.ReadFile(filepath.Join(paths.source, "fixture-private.json"))
	if err != nil || json.Unmarshal(data, &connection) != nil || connection.PublicKey != trustedPublicKey || !lab.LoopbackURL(connection.Feed) {
		return connection, false
	}
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: time.Second}
	defer client.CloseIdleConnections()
	response, err := client.Get(strings.TrimSuffix(connection.Feed, "/manifest") + "/health")
	if err != nil {
		return connection, false
	}
	defer response.Body.Close()
	data, err = io.ReadAll(io.LimitReader(response.Body, 128))
	return connection, err == nil && response.StatusCode == http.StatusOK && string(data) == "toolnest-acceptance\n"
}

func ensureSource(paths locations, executable string) (sourceConnection, error) {
	if connection, ok := healthy(paths); ok {
		return connection, nil
	}
	command := exec.Command(executable, "--serve")
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NO_WINDOW}
	log, err := os.OpenFile(filepath.Join(paths.source, "server.log"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return sourceConnection{}, err
	}
	command.Stdout, command.Stderr = log, log
	err = command.Start()
	log.Close()
	if err != nil {
		return sourceConnection{}, err
	}
	go command.Wait()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		if connection, ok := healthy(paths); ok {
			return connection, nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return sourceConnection{}, errors.New("本地更新源未就绪，请查看测试数据目录中的 source\\server.log")
}

func validMode(mode string) bool {
	return mode == "good" || mode == "source-not-found" || mode == "http-error" || mode == "bad-signature"
}

func serveSource(paths locations, config kitConfig, archive []byte, signature string) error {
	instance, secondary, err := lifecycle.AcquireInstance(context.Background(), "ToolNest.ManualSource."+kitID, lifecycle.AutostartLaunch, func(lifecycle.Launch) {})
	if err != nil || secondary {
		return err
	}
	defer instance.Close()
	environment, err := lab.StartSignedRelease(archive, signature, config.PublicKey, lab.ReleaseOptions{
		Version: config.Target, CurrentVersion: config.Baseline, Notes: config.Notes, ChunkDelay: 40 * time.Millisecond,
	})
	if err != nil {
		return err
	}
	defer environment.Close()
	if err := os.MkdirAll(paths.source, 0700); err != nil {
		return err
	}
	if err := core.AtomicWrite(filepath.Join(paths.source, "mode.txt"), []byte("good"), true); err != nil {
		return err
	}
	if err := core.AtomicWrite(filepath.Join(paths.source, "server.pid"), []byte(strconv.Itoa(os.Getpid())), true); err != nil {
		return err
	}
	if err := environment.WriteConfig(paths.source); err != nil {
		return err
	}
	ticker := time.NewTicker(150 * time.Millisecond)
	defer ticker.Stop()
	// 独立源不注册自启、不改系统代理，最多存活四小时；关闭测试应用不把源带入正式程序。
	deadline := time.NewTimer(4 * time.Hour)
	defer deadline.Stop()
	lastStats := ""
	for {
		select {
		case <-deadline.C:
			return nil
		case <-ticker.C:
			mode, err := os.ReadFile(filepath.Join(paths.source, "mode.txt"))
			if err != nil {
				return err
			}
			if string(mode) == "stop" {
				return nil
			}
			if err := environment.SetMode(string(mode)); err != nil {
				return err
			}
			stats, _ := json.MarshalIndent(map[string]any{"mode": string(mode), "payloadRequests": environment.PayloadRequests.Load(),
				"httpRequests": environment.HTTPRequests.Load(), "socksRequests": environment.SOCKSRequests.Load(), "leakedCredentials": environment.LeakedCredentials.Load()}, "", "  ")
			if string(stats) != lastStats {
				if err := core.AtomicWrite(filepath.Join(paths.source, "stats.json"), stats, true); err != nil {
					return err
				}
				lastStats = string(stats)
			}
		}
	}
}

func stopSource(paths locations) error {
	pidText, err := os.ReadFile(filepath.Join(paths.source, "server.pid"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	pid, err := strconv.ParseUint(string(pidText), 10, 32)
	if err != nil {
		return errors.New("本地源进程标识损坏；未结束任何进程")
	}
	process, err := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if errors.Is(err, windows.ERROR_INVALID_PARAMETER) {
		return nil
	}
	if err != nil {
		return err
	}
	defer windows.CloseHandle(process)
	// 已退出进程的 PID 仍可能可打开，但读取映像路径会失败；此时源已经正常停止。
	if state, err := windows.WaitForSingleObject(process, 0); err == nil && state == windows.WAIT_OBJECT_0 {
		return nil
	}
	buffer := make([]uint16, 32768)
	length := uint32(len(buffer))
	if err := windows.QueryFullProcessImageName(process, 0, &buffer[0], &length); err != nil {
		return err
	}
	if !strings.EqualFold(windows.UTF16ToString(buffer[:length]), filepath.Join(paths.program, "ToolNest-TestLauncher.exe")) {
		return nil // PID 已复用时不能影响另一个进程。
	}
	if err := core.AtomicWrite(filepath.Join(paths.source, "mode.txt"), []byte("stop"), true); err != nil {
		return err
	}
	if result, err := windows.WaitForSingleObject(process, 5000); err == nil && result == windows.WAIT_OBJECT_0 {
		return nil
	}
	return errors.New("本地源尚未正常退出；未强制结束任何进程")
}

func maintenanceLock(directory string) (func(), error) {
	if err := noRedirect(filepath.Join(directory, ".manual-maintenance.lock")); err != nil {
		return nil, err
	}
	path, _ := windows.UTF16PtrFromString(filepath.Join(directory, ".manual-maintenance.lock"))
	handle, err := windows.CreateFile(path, windows.GENERIC_READ|windows.GENERIC_WRITE, 0, nil, windows.OPEN_ALWAYS, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, errors.New("测试组合正在安装或卸载，请完成后再打开")
	}
	return func() { windows.CloseHandle(handle) }, nil
}

func checkIdle(paths locations) error {
	if err := noRedirect(paths.program); err != nil {
		return err
	}
	entries, err := os.ReadDir(paths.program)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	occupied := false
	for _, entry := range entries {
		if err := noRedirect(filepath.Join(paths.program, entry.Name())); err != nil {
			return err
		}
		if entry.Name() != ".manual-maintenance.lock" {
			occupied = true
		}
	}
	if occupied {
		data, err := os.ReadFile(filepath.Join(paths.program, "kit.json"))
		var config kitConfig
		if err != nil || json.Unmarshal(data, &config) != nil || config.ID != kitID {
			return errors.New("测试安装目录已占用且归属不符")
		}
	}
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snapshot)
	entry := windows.ProcessEntry32{Size: uint32(unsafe.Sizeof(windows.ProcessEntry32{}))}
	for err = windows.Process32First(snapshot, &entry); err == nil; err = windows.Process32Next(snapshot, &entry) {
		if !strings.EqualFold(windows.UTF16ToString(entry.ExeFile[:]), "ToolNest.exe") {
			continue
		}
		process, openErr := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, entry.ProcessID)
		if openErr != nil {
			return errors.New("存在无法核实路径的 ToolNest 进程，请退出后重试")
		}
		buffer := make([]uint16, 32768)
		length := uint32(len(buffer))
		queryErr := windows.QueryFullProcessImageName(process, 0, &buffer[0], &length)
		windows.CloseHandle(process)
		if queryErr != nil {
			return queryErr
		}
		path := windows.UTF16ToString(buffer[:length])
		if within(paths.program, path) || within(paths.data, path) {
			return errors.New("请先正常退出本组合的 ToolNest（包括托盘）；不会自动关闭它")
		}
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	return nil
}

func within(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}
