//go:build windows

package toolnest

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/egoist/mygo"
	"golang.org/x/sys/windows"

	"toolnest/internal/app"
	"toolnest/internal/core"
	"toolnest/internal/platform/lifecycle"
	"toolnest/internal/ui"
)

type trayReadOnlyRegistry struct{ core.Registry }

func (trayReadOnlyRegistry) Read(context.Context) (core.State, error) {
	return core.State{}, nil
}

// 真窗口回归只在已核实的非输入桌面执行；普通 go test 不创建窗口。
// Explorer 的可用性在接口处注入，电源消息只发送给本窗口，不操作宿主的锁屏或显示器。
func TestTrayHiddenNativeWindow(t *testing.T) {
	if !privateEntryDesktop() {
		t.Skip("requires a verified non-input private desktop")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, os.Args[0])
	command.Env = append(os.Environ(), "TOOLNEST_TRAY_NATIVE_HELPER=1", "TOOLNEST_TRAY_NATIVE_ROOT="+t.TempDir())
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("hidden-window subprocess: %v\n%s", err, output)
	}
	t.Log("32 loss/recovery cycles; HWND visibility and OnShow unchanged; manual wake still works")
}

func TestMain(suite *testing.M) {
	// MyGo 事件循环必须运行在主 goroutine；测试用例本身由 testing 在子 goroutine 执行。
	if os.Getenv("TOOLNEST_TRAY_NATIVE_HELPER") == "1" {
		root := os.Getenv("TOOLNEST_TRAY_NATIVE_ROOT")
		if !privateEntryDesktop() || root == "" || !within(os.TempDir(), root) {
			fmt.Fprintln(os.Stderr, "native test requires private desktop and temporary data")
			os.Exit(1)
		}
		if err := runHiddenNativeWindow(root); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(suite.Run())
}

func runHiddenNativeWindow(root string) error {
	controller, err := app.New(app.Options{Root: root, Version: "0.0.1", Registry: trayReadOnlyRegistry{}})
	if err != nil {
		return err
	}
	tray := &recoveringTray{available: true}
	host := &desktop{controller: controller, tray: tray, stderr: io.Discard}
	finished := make(chan error, 1)
	mygo.App.WhenReady(func() {
		host.view = ui.New(desktopActions{Controller: controller, host: host})
		options := host.view.WindowOptions("ToolNest tray regression")
		options.Width, options.Height, options.Hidden = 800, 600, true
		host.window = mygo.NewWindow(options)
		host.view.AttachWindow(host.window)
		shows := 0
		host.window.OnShow(func() { host.hidden = false; shows++ })
		host.window.OnHide(func() { host.hidden = true })
		host.dispatch.activate()
		host.lifecycle, err = lifecycle.NewController(lifecycle.Hooks{
			Post: host.dispatch.post, ShowMain: host.show, HideMain: host.window.Hide,
			OpenSettings: host.view.ShowSettings, BackupNow: controller.Backup,
			RequestExit: func() {}, ReportError: host.report,
		})
		if err != nil {
			finished <- err
			mygo.App.Quit()
			return
		}
		host.lifecycle.Start(lifecycle.ManualLaunch, false, func(func(lifecycle.TrayAction)) (lifecycle.Tray, error) { return tray, nil })
		go func() {
			result := exerciseHiddenNativeWindow(host, tray, &shows)
			mygo.RunOnMain(func() { host.lifecycle.Close(); host.dispatch.stop(); host.window.Destroy() })
			finished <- result
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		return err
	}
	return <-finished
}

func exerciseHiddenNativeWindow(host *desktop, tray *recoveringTray, shows *int) error {
	if err := host.hide(); err != nil {
		return err
	}
	mygo.RunOnMain(func() { *shows = 0 })
	user32 := windows.NewLazySystemDLL("user32.dll")
	send := user32.NewProc("SendMessageW")
	visible := user32.NewProc("IsWindowVisible")
	name, _ := windows.UTF16PtrFromString("TaskbarCreated")
	taskbarCreated, _, _ := user32.NewProc("RegisterWindowMessageW").Call(uintptr(unsafe.Pointer(name)))
	if taskbarCreated == 0 {
		return errors.New("TaskbarCreated message registration failed")
	}
	var session uint32
	if err := windows.ProcessIdToSessionId(windows.GetCurrentProcessId(), &session); err != nil {
		return err
	}
	var failure error
	check := func(stage string) {
		nativeVisible, _, _ := visible.Call(host.window.NativeHandle())
		if failure == nil && (nativeVisible != 0 || host.window.IsVisible() || !host.hidden || *shows != 0) {
			failure = fmt.Errorf("%s exposed hidden window: native=%d hidden=%v shows=%d", stage, nativeVisible, host.hidden, *shows)
		}
	}
	for cycle := range 32 {
		mygo.RunOnMain(func() {
			tray.available = false
			host.lifecycle.ProbeTray()
			host.lifecycle.SetStatus("后台检查", false)
			host.syncTray()
			handle := host.window.NativeHandle()
			// WM_WTSSESSION_CHANGE、WM_POWERBROADCAST、TaskbarCreated 只定向重放，不广播。
			send.Call(handle, 0x02b1, 7, uintptr(session))
			send.Call(handle, 0x0218, 4, 0)
			setting := struct {
				ID     windows.GUID
				Length uint32
				State  uint32
			}{ID: windows.GUID{Data1: 0x2b84c20e, Data2: 0xad23, Data3: 0x4ddf, Data4: [8]byte{0x93, 0xdb, 0x05, 0xff, 0xbd, 0x7e, 0xfc, 0xa5}}, Length: 4}
			for _, state := range []uint32{0, 2, 1} {
				setting.State = state
				send.Call(handle, 0x0218, 0x8013, uintptr(unsafe.Pointer(&setting)))
				runtime.KeepAlive(setting)
			}
			send.Call(handle, 0x0218, 18, 0)
			send.Call(handle, 0x0218, 7, 0)
			send.Call(handle, 0x02b1, 8, uintptr(session))
			send.Call(handle, taskbarCreated, 0, 0)
			host.window.Invalidate()
			check(fmt.Sprintf("cycle %d lost", cycle))
		})
		time.Sleep(45 * time.Millisecond)
		mygo.RunOnMain(func() {
			check("queued notifications")
			tray.available, tray.failure = true, errors.New("temporary shell update failure")
			host.syncTray()
			host.lifecycle.SetStatus("备份完成", false)
		})
		time.Sleep(45 * time.Millisecond)
		mygo.RunOnMain(func() {
			check("status write failure")
			tray.failure = nil
			host.lifecycle.ProbeTray()
			host.syncTray()
			host.lifecycle.SecondLaunch(lifecycle.AutostartLaunch)
			check("tray recovered")
		})
		if failure != nil {
			return failure
		}
	}
	// 留足两个真实探测周期，防止只检查同步调用而遗漏定时器造成的弹窗。
	time.Sleep(4200 * time.Millisecond)
	mygo.RunOnMain(func() { check("background timers") })
	if failure != nil {
		return failure
	}
	host.lifecycle.SecondLaunch(lifecycle.ManualLaunch)
	mygo.RunOnMain(func() {
		if !host.window.IsVisible() || host.hidden || *shows != 1 {
			failure = errors.New("manual launch did not restore the window exactly once")
		}
	})
	return failure
}

type recoveringTray struct {
	available bool
	failure   error
	writes    int
}

func (tray *recoveringTray) Available() bool { return tray.available }
func (tray *recoveringTray) Close()          {}
func (tray *recoveringTray) SetState(string, bool) error {
	tray.writes++
	return tray.failure
}

func TestTrayRecoveryResendsStateAndKeepsIndependentErrors(t *testing.T) {
	controller, err := app.New(app.Options{Root: t.TempDir(), Version: "0.0.1", Registry: trayReadOnlyRegistry{}})
	if err != nil {
		t.Fatal(err)
	}
	tray := &recoveringTray{available: true}
	var output bytes.Buffer
	host := &desktop{controller: controller, tray: tray, hidden: true, stderr: &output}
	host.report(errors.New("独立的系统错误"))
	host.syncTray()
	host.syncTray()
	if tray.writes != 1 {
		t.Fatal("unchanged tray state should not be rewritten", tray.writes)
	}
	tray.available = false
	host.report(lifecycle.ErrTrayUnavailable)
	host.syncTray()
	if controller.View().TrayAvailable || host.trayStateValid || !host.hidden {
		t.Fatal("temporary tray loss did not preserve the background state")
	}
	tray.available, tray.failure = true, errors.New("temporary tray update failure")
	host.syncTray()
	host.syncTray()
	if controller.View().TrayAvailable || host.trayStateValid || !host.hidden {
		t.Fatal("failed tray updates were accepted or changed visibility")
	}
	if strings.Count(output.String(), tray.failure.Error()) != 1 {
		t.Fatal("the same transient error was logged repeatedly", output.String())
	}
	tray.failure = nil
	host.syncTray()
	if !controller.View().TrayAvailable || !host.trayStateValid || host.trayError.Load() != nil || !host.hidden {
		t.Fatal("tray recovery did not restore status quietly")
	}
	if host.errorText() != "独立的系统错误" {
		t.Fatal("tray recovery cleared an unrelated error", host.errorText())
	}
	if tray.writes != 4 {
		t.Fatal("recovered tray did not receive the unchanged status again", tray.writes)
	}
	host.syncTray()
	if tray.writes != 4 {
		t.Fatal("successful recovery should resume status deduplication", tray.writes)
	}
}
