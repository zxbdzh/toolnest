//go:build windows

package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var user32 = windows.NewLazySystemDLL("user32.dll")
var createDesktop = user32.NewProc("CreateDesktopW")
var closeDesktop = user32.NewProc("CloseDesktop")
var inputDesktop = user32.NewProc("OpenInputDesktop")
var foregroundWindow = user32.NewProc("GetForegroundWindow")
var windowProcess = user32.NewProc("GetWindowThreadProcessId")

type configuration struct {
	Desktop, Root, Application, Worker, Scenario string
	EntryTests, UpdatedApplication               string
	FixtureRunner, SignedUpdate                  string
	BaselineVersion, TargetVersion               string
	SandboxConfig                                string
	KitResult                                    string
	TimeoutSeconds                               int
}

type report struct {
	Desktop               string `json:"desktop"`
	InputDesktopUnchanged bool   `json:"inputDesktopUnchanged"`
	WorkerOnForeground    bool   `json:"workerOnForeground"`
	InputDesktop          string `json:"inputDesktop"`
	ChildrenRemaining     uint32 `json:"childrenRemaining"`
	WorkerExit            uint32 `json:"workerExit"`
	Samples               int    `json:"samples"`
	Error                 string `json:"error,omitempty"`
}

type jobAccounting struct {
	UserTime, KernelTime, PeriodUserTime, PeriodKernelTime               int64
	PageFaultCount, TotalProcesses, ActiveProcesses, TerminatedProcesses uint32
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	flags := flag.NewFlagSet("desktop-runner", flag.ContinueOnError)
	application := flags.String("application", "", "isolated ToolNest.exe")
	worker := flags.String("worker", "", "fixed background worker script")
	root := flags.String("out", "", "new private evidence directory")
	scenario := flags.String("scenario", "smoke", "smoke, inspect, entry, update-ui or sandbox")
	sandboxConfig := flags.String("sandbox-config", "", "external restricted Windows Sandbox configuration")
	entryTests := flags.String("entry-tests", "", "compiled entry.test.exe on the private desktop")
	updatedApplication := flags.String("updated-application", "", "isolated newer ToolNest.exe")
	fixtureRunner := flags.String("fixture-runner", "", "loopback-only acceptance-runner.exe")
	signedUpdate := flags.String("signed-update", "", "external signed application fixture directory")
	baselineVersion := flags.String("baseline-version", "0.1.0", "stable baseline fixture version")
	targetVersion := flags.String("target-version", "0.1.1", "stable target fixture version")
	kitResult := flags.String("kit-result", "", "explicit isolated installer kit identity")
	timeout := flags.Duration("timeout", 60*time.Second, "bounded execution duration")
	if err := flags.Parse(os.Args[1:]); err != nil {
		return err
	}
	maximum := 3 * time.Minute
	// HTTP 与 SOCKS5 两条完整更新链串行执行，含验签、重启和首帧确认；允许与安装链相同的上限。
	if *scenario == "sandbox" || *scenario == "installed-kit" || *scenario == "update-ui" {
		maximum = 15 * time.Minute
	}
	if flags.NArg() != 0 || !filepath.IsAbs(*root) || !filepath.IsAbs(*worker) || !filepath.IsAbs(*application) || *timeout < 5*time.Second || *timeout > maximum || (*scenario != "smoke" && *scenario != "inspect" && *scenario != "entry" && *scenario != "update-ui" && *scenario != "sandbox" && *scenario != "installed-kit") {
		return errors.New("explicit absolute paths, supported scenario and bounded timeout required")
	}
	if !strings.EqualFold(filepath.Base(*application), "ToolNest.exe") {
		return errors.New("only the isolated ToolNest executable is supported")
	}
	files := []string{*application, *worker}
	if *scenario == "installed-kit" {
		reserved := filepath.Join(os.TempDir(), "toolnest-acceptance") + string(filepath.Separator)
		if !filepath.IsAbs(*kitResult) || !strings.HasPrefix(filepath.Clean(*kitResult), reserved) || filepath.Base(*kitResult) != "kit-result.json" ||
			filepath.Base(*worker) != "Installed-Kit-Worker.ps1" || !strings.HasPrefix(filepath.Clean(*root), reserved) {
			return errors.New("installed kit requires reserved external identity, evidence and fixed private-desktop worker")
		}
		files = append(files, *kitResult)
	} else if *kitResult != "" {
		return errors.New("kit identity is only allowed by the installed-kit scenario")
	}
	if *scenario == "sandbox" {
		if !filepath.IsAbs(*sandboxConfig) || !strings.EqualFold(filepath.Ext(*sandboxConfig), ".wsb") || filepath.Base(*worker) != "Sandbox-Background-Worker.ps1" {
			return errors.New("sandbox scenario requires its fixed worker and explicit external .wsb")
		}
		files = append(files, *sandboxConfig)
	} else if *sandboxConfig != "" {
		return errors.New("sandbox configuration is only accepted by the sandbox scenario")
	}
	if *scenario == "entry" {
		if !filepath.IsAbs(*entryTests) || !filepath.IsAbs(*updatedApplication) || !strings.EqualFold(filepath.Base(*entryTests), "entry.test.exe") || !strings.EqualFold(filepath.Base(*updatedApplication), "ToolNest.exe") {
			return errors.New("entry scenario requires explicit entry.test.exe and updated ToolNest.exe")
		}
		files = append(files, *entryTests, *updatedApplication)
	} else if *entryTests != "" || (*scenario != "update-ui" && *updatedApplication != "") {
		return errors.New("entry-specific files are only accepted by the entry scenario")
	}
	if *scenario == "update-ui" {
		if !filepath.IsAbs(*updatedApplication) || !filepath.IsAbs(*fixtureRunner) || !filepath.IsAbs(*signedUpdate) || !strings.EqualFold(filepath.Base(*fixtureRunner), "acceptance-runner.exe") {
			return errors.New("UI update requires explicit application and signed loopback fixtures")
		}
		files = append(files, *updatedApplication, *fixtureRunner)
		if !stableFixtureVersion(*baselineVersion) || !stableFixtureVersion(*targetVersion) || *baselineVersion == *targetVersion {
			return errors.New("distinct stable fixture versions required")
		}
		for _, filename := range []string{"update-" + *targetVersion + ".tar.gz", "update-" + *targetVersion + ".sig", "update.pub"} {
			files = append(files, filepath.Join(*signedUpdate, filename))
		}
	} else if *fixtureRunner != "" || *signedUpdate != "" {
		return errors.New("signed loopback fixtures are only accepted by the UI update scenario")
	}
	for _, name := range files {
		info, err := os.Lstat(name)
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return errors.New("regular application and worker files required")
		}
	}
	if err := os.Mkdir(*root, 0700); err != nil {
		return err
	}
	identifier := fmt.Sprintf("ToolNest.Test.%d.%d", os.Getpid(), time.Now().UnixNano())
	account, err := windows.OpenCurrentProcessToken()
	if err != nil {
		return err
	}
	defer account.Close()
	owner, err := account.GetTokenUser()
	if err != nil {
		return err
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;;GA;;;" + owner.User.Sid.String() + ")")
	if err != nil {
		return err
	}
	security := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}
	name, _ := windows.UTF16PtrFromString(identifier)
	// 句柄不申请 SWITCHDESKTOP；本工具从不切换输入桌面，所有子窗口留在独立桌面。
	desktop, _, callErr := createDesktop.Call(uintptr(unsafe.Pointer(name)), 0, 0, 0, 0xC7, uintptr(unsafe.Pointer(&security)))
	runtime.KeepAlive(sd)
	if desktop == 0 {
		return fmt.Errorf("create private desktop: %w", callErr)
	}
	defer closeDesktop.Call(desktop)
	config := configuration{Desktop: identifier, Root: *root, Application: *application, Worker: *worker, Scenario: *scenario, EntryTests: *entryTests, UpdatedApplication: *updatedApplication, FixtureRunner: *fixtureRunner, SignedUpdate: *signedUpdate, BaselineVersion: *baselineVersion, TargetVersion: *targetVersion, SandboxConfig: *sandboxConfig, KitResult: *kitResult, TimeoutSeconds: int(timeout.Seconds())}
	data, _ := json.Marshal(config)
	configPath := filepath.Join(*root, "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return err
	}
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(job)
	limits := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	limits.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation, uintptr(unsafe.Pointer(&limits)), uint32(unsafe.Sizeof(limits))); err != nil {
		return err
	}
	system, err := windows.GetSystemDirectory()
	if err != nil {
		return err
	}
	shell := filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe")
	literal := func(value string) string { return "'" + strings.ReplaceAll(value, "'", "''") + "'" }
	bootstrap := "[Console]::OutputEncoding=[Text.UTF8Encoding]::new($false); & ([scriptblock]::Create([IO.File]::ReadAllText(" + literal(*worker) + ",[Text.Encoding]::UTF8))) -Config " + literal(configPath)
	arguments := []string{shell, "-NoLogo", "-NoProfile", "-NonInteractive", "-MTA", "-ExecutionPolicy", "Bypass", "-Command", bootstrap}
	quoted := make([]string, len(arguments))
	for index, value := range arguments {
		quoted[index] = syscall.EscapeArg(value)
	}
	command, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	program, _ := windows.UTF16PtrFromString(shell)
	desktopPath, _ := windows.UTF16PtrFromString("WinSta0\\" + identifier)
	startup := windows.StartupInfo{Cb: uint32(unsafe.Sizeof(windows.StartupInfo{})), Desktop: desktopPath}
	logName, _ := windows.UTF16PtrFromString(filepath.Join(*root, "process.log"))
	inheritable := windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), InheritHandle: 1}
	log, err := windows.CreateFile(logName, windows.GENERIC_WRITE, windows.FILE_SHARE_READ, &inheritable, windows.CREATE_NEW, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(log)
	nullName, _ := windows.UTF16PtrFromString("NUL")
	null, err := windows.CreateFile(nullName, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE, &inheritable, windows.OPEN_EXISTING, windows.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(null)
	startup.Flags = windows.STARTF_USESTDHANDLES
	startup.StdInput = null
	startup.StdOutput = log
	startup.StdErr = log
	var process windows.ProcessInformation
	if err := windows.CreateProcess(program, command, nil, nil, true, windows.CREATE_NO_WINDOW|windows.CREATE_SUSPENDED, nil, nil, &startup, &process); err != nil {
		return err
	}
	defer windows.CloseHandle(process.Process)
	defer windows.CloseHandle(process.Thread)
	if err := windows.AssignProcessToJobObject(job, process.Process); err != nil {
		_ = windows.TerminateProcess(process.Process, 1)
		return err
	}
	initialInput, _, _ := inputDesktop.Call(0, 0, 1)
	if initialInput == 0 {
		return errors.New("cannot observe input desktop; refusing to resume worker")
	}
	defer closeDesktop.Call(initialInput)
	result := report{Desktop: identifier, InputDesktopUnchanged: true, InputDesktop: desktopName(initialInput)}
	if _, err := windows.ResumeThread(process.Thread); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	for {
		result.Samples++
		hwnd, _, _ := foregroundWindow.Call()
		var pid uint32
		windowProcess.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
		if pid != 0 {
			foreground, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
			if err == nil {
				var member int32
				ok, _, _ := windows.NewLazySystemDLL("kernel32.dll").NewProc("IsProcessInJob").Call(uintptr(foreground), uintptr(job), uintptr(unsafe.Pointer(&member)))
				if ok != 0 && member != 0 {
					result.WorkerOnForeground = true
				}
				windows.CloseHandle(foreground)
			}
		}
		current, _, _ := inputDesktop.Call(0, 0, 1)
		if current != 0 {
			if !sameDesktop(initialInput, current) {
				result.InputDesktopUnchanged = false
			}
			closeDesktop.Call(current)
		} else {
			result.Error = "input desktop observation became unavailable; isolation cannot be certified"
			break
		}
		state, err := windows.WaitForSingleObject(process.Process, 20)
		if err != nil {
			result.Error = err.Error()
			break
		}
		if state == windows.WAIT_OBJECT_0 {
			_ = windows.GetExitCodeProcess(process.Process, &result.WorkerExit)
			break
		}
		if ctx.Err() != nil {
			result.Error = "background desktop worker timed out"
			break
		}
	}
	// Job 只包含本次暂停启动后登记的工作进程与子进程，清理不触碰用户窗口或其他任务。
	_ = windows.TerminateJobObject(job, 1)
	for deadline := time.Now().Add(3 * time.Second); ; {
		var accounting jobAccounting
		err := windows.QueryInformationJobObject(job, windows.JobObjectBasicAccountingInformation, uintptr(unsafe.Pointer(&accounting)), uint32(unsafe.Sizeof(accounting)), nil)
		if err != nil {
			result.Error = "cannot verify job cleanup"
			break
		}
		result.ChildrenRemaining = accounting.ActiveProcesses
		if accounting.ActiveProcesses == 0 {
			break
		}
		if time.Now().After(deadline) {
			result.Error = "private desktop child cleanup did not complete"
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	data, _ = json.MarshalIndent(result, "", "  ")
	_ = os.WriteFile(filepath.Join(*root, "isolation.json"), data, 0600)
	if result.Error != "" {
		return errors.New(result.Error)
	}
	if result.WorkerExit != 0 || result.WorkerOnForeground || !result.InputDesktopUnchanged {
		return fmt.Errorf("background run failed; see %s", filepath.Join(*root, "isolation.json"))
	}
	fmt.Println("Background desktop evidence:", *root)
	return nil
}

func sameDesktop(left, right uintptr) bool {
	first, second := desktopName(left), desktopName(right)
	return first != "" && first == second
}

func stableFixtureVersion(value string) bool {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return false
	}
	for _, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return false
		}
		for _, character := range part {
			if character < '0' || character > '9' {
				return false
			}
		}
	}
	return true
}

func desktopName(handle uintptr) string {
	getName := user32.NewProc("GetUserObjectInformationW")
	buffer := make([]uint16, 256)
	var size uint32
	ok, _, _ := getName.Call(handle, 2, uintptr(unsafe.Pointer(&buffer[0])), uintptr(len(buffer)*2), uintptr(unsafe.Pointer(&size)))
	if ok == 0 {
		return ""
	}
	return windows.UTF16ToString(buffer)
}
