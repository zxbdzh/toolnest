//go:build windows

package toolnest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"

	"toolnest/internal/app"
	"toolnest/internal/brand"
	"toolnest/internal/network"
	"toolnest/internal/platform"
	"toolnest/internal/platform/lifecycle"
	"toolnest/internal/ui"
	"toolnest/internal/update"
	"toolnest/packaging"
	installation "toolnest/packaging/runtime"
)

// uiDispatch 在桌面准备之前排队，既避免丢失第二次启动，也避免阻塞管道线程。
type uiDispatch struct {
	mu      sync.Mutex
	ready   bool
	stopped bool
	pending []func()
}

func (queue *uiDispatch) post(action func()) {
	queue.mu.Lock()
	if queue.stopped {
		queue.mu.Unlock()
		return
	}
	if !queue.ready {
		queue.pending = append(queue.pending, action)
		queue.mu.Unlock()
		return
	}
	queue.mu.Unlock()
	// WhenReady 已 ready 时也是异步派发；RunOnMain 本身会等待，不能当作 Post。
	mygo.App.WhenReady(action)
}

func (queue *uiDispatch) activate() {
	queue.mu.Lock()
	queue.ready = true
	pending := queue.pending
	queue.pending = nil
	queue.mu.Unlock()
	for _, action := range pending {
		mygo.App.WhenReady(action)
	}
}

func (queue *uiDispatch) stop() {
	queue.mu.Lock()
	defer queue.mu.Unlock()
	queue.stopped = true
	queue.pending = nil
}

type desktop struct {
	env            environment
	controller     *app.Controller
	view           *ui.Controller
	window         *mygo.Window
	lifecycle      *lifecycle.Controller
	tray           lifecycle.Tray
	dispatch       uiDispatch
	approved       atomic.Bool
	hidden         bool
	lastTrayState  string
	lastTrayFailed bool
	trayStateValid bool
	systemError    atomic.Pointer[string]
	trayError      atomic.Pointer[string]
	detachClose    func()
	requireUpdate  atomic.Bool
	stderr         io.Writer
	stop           chan struct{}
	stopOnce       sync.Once
}

type desktopActions struct {
	*app.Controller
	host *desktop
}

func (actions desktopActions) View() app.View {
	view := actions.Controller.View()
	if actions.host.env.testing() {
		view.LegacyCount = 0
		view.Isolated = true
	}
	if message := actions.host.errorText(); message != "" {
		if view.Error != "" {
			view.Error += "；"
		}
		view.Error += message
	}
	return view
}

func (actions desktopActions) MoveSnapshots(directory string) {
	if actions.host.env.testing() && !within(actions.host.env.root, directory) {
		actions.host.report(errors.New("隔离模式只能迁移到本次数据根内的目录"))
		return
	}
	actions.Controller.MoveSnapshots(directory)
}

func (actions desktopActions) ImportLegacy() {
	if actions.host.env.testing() {
		actions.host.report(errors.New("隔离入口不导入真实用户的旧快照，请使用专用业务验收夹具"))
		return
	}
	actions.Controller.ImportLegacy()
}

func runDesktop(env environment, stdout, stderr io.Writer) error {
	desktop := &desktop{env: env, stderr: stderr, stop: make(chan struct{})}
	defer desktop.dispatch.stop()
	namespace := "ToolNest"
	if env.testing() {
		namespace += ".Test." + env.testRoot
	}
	launch := lifecycle.ManualLaunch
	if env.autostart {
		launch = lifecycle.AutostartLaunch
	}
	instance, secondary, err := lifecycle.AcquireInstance(context.Background(), namespace, launch, func(second lifecycle.Launch) {
		desktop.dispatch.post(func() {
			if desktop.lifecycle != nil {
				desktop.lifecycle.SecondLaunch(second)
			}
		})
	})
	if err != nil || secondary {
		return err
	}
	defer instance.Close()
	if env.installed {
		// 只锁真正安装的 exe，开发启动不能创建生产安装目录或占用维护锁。
		release, err := installation.AcquireInstallation(env.installDir)
		if err != nil {
			return err
		}
		defer release()
	}
	registry, err := platform.NewRegistry(platform.Options{TestRoot: env.testRoot})
	if err != nil {
		return err
	}
	startup, legacy, err := newIntegrations(env)
	if err != nil {
		return err
	}
	hooks := app.Hooks{
		Copy: func(text string) error { return mygo.Clipboard.Write(transfer.TextData(text)) },
		ChooseDirectory: func() (string, error) {
			paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: desktop.window, Title: "选择快照目录", Directory: true, CreateDirectories: true})
			if err != nil || len(paths) == 0 {
				return "", err
			}
			return paths[0], nil
		},
		OpenDirectory: mygo.Shell.OpenPath,
		OpenURL:       mygo.Shell.OpenExternal,
		Startup: func() bool {
			enabled, err := startup.Enabled(context.Background())
			if err != nil {
				desktop.report(err)
			}
			return enabled
		},
		SetStartup: func(enabled bool) error {
			// 测试 RunStore 只写独立子树，不会被 Windows 当成真实自启入口执行。
			_, err := startup.SetEnabled(context.Background(), enabled)
			return err
		},
		Theme: func(theme string) {
			desktop.dispatch.post(func() { mygo.Theme.SetSource(mygo.ThemeSource(theme)) })
		},
		Hide: desktop.hide,
		Quit: desktop.finishQuit,
		Notify: func(title, message string) error {
			if !mygo.NotificationsSupported() {
				err := errors.New("系统通知不可用，失败详情保留在 ToolNest 与托盘")
				desktop.report(err)
				return err
			}
			err := mygo.NewNotification(mygo.NotificationOptions{ID: "toolnest.backup.failure", Group: "toolnest.backup", Title: title, Body: message}).Show()
			if err != nil {
				desktop.report(fmt.Errorf("系统通知发送失败，原始失败仍保留: %w", err))
			}
			return err
		},
		LegacyEntries: legacy.entries,
		DisableLegacy: legacy.disable,
		UndoLegacy:    legacy.undo,
		RestartUpdate: desktop.restartUpdate,
	}
	vaultNamespace := "ToolNest"
	if env.testing() {
		vaultNamespace += "/Tests/" + env.testRoot
	}
	releasePage := ""
	if packaging.Repository != "" && !env.testing() {
		releasePage = "https://github.com/" + packaging.Repository + "/releases"
	}
	desktop.controller, err = app.New(app.Options{Root: env.root, Version: packaging.Version(), Registry: registry,
		Vault: network.WindowsVault{Namespace: vaultNamespace}, Hooks: hooks, Source: env.source, PublicKey: env.publicKey,
		InstallDir: env.installDir, Executable: filepath.Base(env.executable), UpdateEndpoint: env.endpoint, ReleasePage: releasePage})
	if err != nil {
		return err
	}
	manager := desktop.controller.Updater()
	if env.confirmToken == "" && manager.Status().Phase != update.Unconfigured {
		if err := manager.Recover(context.Background()); err != nil {
			if errors.Is(err, update.ErrStartup) || errors.Is(err, update.ErrRecovery) {
				return fmt.Errorf("更新未完成安全恢复，请检查保留副本后重试: %w", err)
			}
			desktop.report(err)
		}
		if manager.Prepared() != nil {
			// 下次启动的准备包仍由另一个进程等待本进程真正退出后切换。
			if err := startUpdateHelper(env, manager, false); err == nil {
				return nil
			} else if !errors.Is(err, update.ErrBlocked) {
				desktop.report(err)
			}
		}
	}
	if data, err := readHelperResult(filepath.Join(env.root, "updates", "helper-result.json")); err == nil && len(data) <= 65536 {
		var result struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(data, &result) == nil && result.Error != "" {
			desktop.report(fmt.Errorf("上次更新助手未完成，原版本及诊断记录已保留: %s", result.Error))
		}
	}
	mygo.App.SetName("ToolNest")
	mygo.App.SetVersion(packaging.Version())
	mygo.App.SetPath(mygo.PathUserData, env.root)
	mygo.App.SetPath(mygo.PathCache, filepath.Join(env.root, "cache"))
	mygo.App.SetPath(mygo.PathLogs, filepath.Join(env.root, "logs"))
	mygo.Theme.SetSource(mygo.ThemeSource(desktop.controller.View().Settings.Theme))
	mygo.App.OnBeforeQuit(func(event *mygo.QuitEvent) {
		if desktop.approved.Load() {
			return
		}
		// 系统/菜单退出必须先经过同一业务门，不能先关窗口再询问。
		event.PreventDefault()
		desktop.dispatch.post(desktop.controller.Quit)
	})
	mygo.App.WhenReady(func() { desktop.ready(launch, stdout) })
	err = mygo.App.Run()
	desktop.stopOnce.Do(func() { close(desktop.stop) })
	return err
}

// 旧版助手退出后，新版或杀软可能短暂持有结果文件。
// 只容忍 Windows 共享冲突并做有界重试，避免把瞬时读失败误报成更新失败。
func readHelperResult(path string) ([]byte, error) {
	deadline := time.Now().Add(time.Second)
	for {
		data, err := os.ReadFile(path)
		if err == nil || !errors.Is(err, syscall.Errno(32)) || time.Now().After(deadline) {
			return data, err
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (desktop *desktop) ready(launch lifecycle.Launch, stdout io.Writer) {
	desktop.view = ui.New(desktopActions{Controller: desktop.controller, host: desktop})
	title := "ToolNest"
	if desktop.env.testing() {
		title = "ToolNest 更新测试版"
	}
	options := desktop.view.WindowOptions(title)
	options.Hidden = true
	desktop.window = mygo.NewWindow(options)
	if err := desktop.window.SetIcon(brand.PNG(256, false)); err != nil {
		desktop.report(fmt.Errorf("应用图标未能设置: %w", err))
	}
	desktop.detachClose = desktop.view.AttachWindow(desktop.window)
	desktop.window.OnShow(func() { desktop.hidden = false })
	desktop.window.OnHide(func() { desktop.hidden = true })
	controller, err := lifecycle.NewController(lifecycle.Hooks{Post: desktop.dispatch.post, ShowMain: desktop.show,
		HideMain: func() { desktop.window.Hide() }, OpenSettings: func() { desktop.view.ShowSettings(); desktop.window.Invalidate() },
		OpenUpdates: func() { desktop.view.ShowUpdates(); desktop.window.Invalidate() },
		BackupNow:   desktop.controller.Backup, RequestExit: desktop.controller.Quit, ReportError: desktop.report})
	if err != nil {
		desktop.report(err)
		desktop.show()
	} else {
		desktop.lifecycle = controller
		normal, failed := trayPNG(false), trayPNG(true)
		factory := lifecycle.NewMyGoTrayFactory(lifecycle.MyGoTrayOptions{NormalPNG: normal, ErrorPNG: failed})
		if desktop.env.resumeTray {
			launch = lifecycle.AutostartLaunch
		}
		startInTray := desktop.controller.View().Settings.StartInTray || desktop.env.resumeTray
		controller.Start(launch, startInTray, func(action func(lifecycle.TrayAction)) (lifecycle.Tray, error) {
			tray, err := factory(action)
			desktop.tray = tray
			return tray, err
		})
	}
	desktop.syncTray()
	desktop.dispatch.activate()
	desktop.controller.Start()
	go desktop.monitor()
	if desktop.env.confirmToken != "" {
		// Native Content 的 Ready 是首帧完成回调；窗口构造成功不足以确认新版健康。
		desktop.window.OnReadyToShow(func() { desktop.confirmStartup() })
		desktop.window.Invalidate()
	} else {
		desktop.confirmStartup()
	}
	if desktop.env.smoke {
		go func() {
			time.Sleep(1500 * time.Millisecond)
			desktop.dispatch.post(func() {
				view := desktop.controller.View()
				_ = json.NewEncoder(stdout).Encode(map[string]any{"version": view.Version, "window": !desktop.window.IsDestroyed(), "tray": view.TrayAvailable,
					"testRoot": desktop.env.testRoot, "update": view.Update.State, "autostart": view.Settings.LaunchAtLogin, "schedule": view.Settings.ScheduleEnabled,
					"error": view.Error, "desktopError": desktop.errorText()})
				desktop.controller.Quit()
			})
		}()
	}
}

func (desktop *desktop) confirmStartup() {
	// 桌面、设置、快照读取与计划循环均已初始化后才确认，不把进程创建当健康。
	if desktop.env.confirmToken != "" {
		if message := desktop.controller.View().Error; message != "" {
			_ = desktop.controller.Updater().RecordStartupFailure(desktop.env.confirmToken, message)
			desktop.report(fmt.Errorf("新版未通过数据初始化检查，拒绝确认启动: %s", message))
			desktop.dispatch.post(desktop.controller.Quit)
			return
		}
		if err := desktop.controller.Updater().ConfirmStartup(context.Background(), desktop.env.confirmToken); err != nil {
			_ = desktop.controller.Updater().RecordStartupFailure(desktop.env.confirmToken, err.Error())
			desktop.report(err)
			desktop.dispatch.post(desktop.controller.Quit)
			return
		}
	}
	if desktop.env.installed {
		if err := installation.SyncInstalledVersion(desktop.env.installDir, packaging.Version()); err != nil {
			desktop.report(err)
		}
	}
}

func (desktop *desktop) show() {
	if desktop.window != nil && !desktop.window.IsDestroyed() {
		desktop.window.Show()
		desktop.window.Restore()
		desktop.window.Focus()
	}
}

func (desktop *desktop) report(err error) {
	if err == nil {
		return
	}
	if errors.Is(err, lifecycle.ErrTrayUnavailable) {
		desktop.reportTrayError(err)
		return
	}
	fmt.Fprintln(desktop.stderr, err)
	message := err.Error()
	if strings.Contains(message, "Windows notification-area icon could not be verified") {
		message = fmt.Sprintf("系统托盘暂不可用，窗口将保持显示（%s）", message)
	}
	desktop.systemError.Store(&message)
	desktop.dispatch.post(func() {
		if desktop.window != nil && !desktop.window.IsDestroyed() {
			desktop.window.Invalidate()
		}
	})
}

func (desktop *desktop) errorText() string {
	var messages []string
	if value := desktop.systemError.Load(); value != nil {
		messages = append(messages, *value)
	}
	if value := desktop.trayError.Load(); value != nil {
		messages = append(messages, *value)
	}
	return strings.Join(messages, "；")
}

func (desktop *desktop) reportTrayError(err error) {
	message := err.Error()
	if previous := desktop.trayError.Load(); previous != nil && *previous == message {
		return
	}
	fmt.Fprintln(desktop.stderr, err)
	desktop.trayError.Store(&message)
}

func (desktop *desktop) hide() error {
	var err error
	mygo.RunOnMain(func() {
		// 不只信任缓存的 TrayAvailable；Explorer 重启后隐藏可能使应用失联。
		if desktop.tray == nil || !desktop.tray.Available() {
			desktop.controller.SetTrayAvailable(false)
			desktop.show()
			err = errors.New("托盘不可用，窗口保持可见")
			return
		}
		desktop.window.Hide()
	})
	return err
}

func (desktop *desktop) syncTray() {
	available := desktop.tray != nil && desktop.tray.Available()
	desktop.controller.SetTrayAvailable(available)
	if !available {
		// 息屏后的托盘查询失败不等于用户要求打开窗口；恢复后重新发送最新状态。
		desktop.trayStateValid = false
		return
	}
	view := desktop.controller.View()
	if tray, ok := desktop.tray.(interface{ SetUpdateState(string) }); ok {
		tray.SetUpdateState(view.Update.State)
	}
	message := view.Status
	integrationError := ""
	if value := desktop.systemError.Load(); value != nil {
		integrationError = *value
	}
	failed := view.Error != "" || view.Update.Error != "" || integrationError != ""
	switch {
	case view.Error != "":
		message = view.Error
	case integrationError != "":
		message = integrationError
	case view.Update.Error != "":
		message = view.Update.Error
	case view.Busy != "":
		message = view.Busy
	case view.Update.State == string(update.Ready):
		message = "更新已就绪，等待安全重启"
	case view.Settings.ScheduleEnabled:
		message += " · 下次备份 " + view.NextRun
	}
	if !desktop.trayStateValid || message != desktop.lastTrayState || failed != desktop.lastTrayFailed {
		if err := desktop.tray.SetState(message, failed); err != nil {
			desktop.controller.SetTrayAvailable(false)
			desktop.reportTrayError(err)
			desktop.trayStateValid = false
			return
		}
		desktop.lastTrayState, desktop.lastTrayFailed = message, failed
		desktop.trayStateValid = true
		// 托盘恢复只清除它自己的临时错误，不抹掉备份、更新或其他系统错误。
		desktop.trayError.Store(nil)
	}
}

func (desktop *desktop) monitor() {
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-desktop.stop:
			return
		case <-timer.C:
			desktop.dispatch.post(func() {
				if !desktop.approved.Load() {
					desktop.syncTray()
				}
			})
		}
	}
}

func (desktop *desktop) restartUpdate() error {
	if desktop.controller.Updater().Prepared() == nil {
		return update.ErrNotReady
	}
	// 此 hook 只在用户重启意图已排空关键写入时调用，不创建新的退出请求。
	desktop.requireUpdate.Store(true)
	return nil
}

func (desktop *desktop) finishQuit() {
	desktop.dispatch.post(func() {
		if desktop.approved.Load() {
			return
		}
		// 此 hook 只在 app 已保存并关闭业务门后调用；托盘隐藏绝不进入此分支。
		manager := desktop.controller.Updater()
		// 普通退出只保留准备包，由下次真实启动应用；不偷偷拉起新版。
		if desktop.requireUpdate.Load() && manager.Prepared() != nil {
			if err := startUpdateHelper(desktop.env, manager, desktop.hidden); err != nil {
				desktop.report(fmt.Errorf("更新未切换，原版本和准备包保留: %w", err))
				if !desktop.env.smoke {
					_, _ = mygo.Dialog.Message(mygo.MessageOptions{Parent: desktop.window, Type: mygo.MessageError, Title: "更新未切换",
						Message: "本次安全退出，但更新助手未能启动；原版本和准备包保持不变。", Detail: err.Error()})
				}
			}
		}
		desktop.approved.Store(true)
		desktop.stopOnce.Do(func() { close(desktop.stop) })
		// 解除只负责关闭确认的监听器，随后 App.Quit 关闭窗口不会再次弹相同询问。
		if desktop.detachClose != nil {
			desktop.detachClose()
		}
		if desktop.lifecycle != nil {
			desktop.lifecycle.Close()
		}
		mygo.App.Quit()
	})
}
