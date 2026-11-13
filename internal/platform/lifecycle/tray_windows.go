//go:build windows

package lifecycle

import (
	"errors"
	"fmt"

	"github.com/egoist/mygo"
)

// MyGoTrayOptions requires caller-owned normal/error PNG icons (no resource files outside this package).
type MyGoTrayOptions struct{ NormalPNG, ErrorPNG []byte }
type myGoTray struct {
	native            *mygo.Tray
	status            *mygo.MenuItem
	updates           *mygo.MenuItem
	updateState       string
	normal, errorIcon []byte
	closed            bool
}

// MyGoPost dispatches through the fixed MyGo main-loop API. Bind after App readiness;
// for second launches arriving earlier, main must queue Hooks.Post until it is ready.
func MyGoPost(fn func()) { mygo.RunOnMain(fn) }

// NewMyGoTrayFactory must be used from App.OnReady/WhenReady after the main UI exists.
func NewMyGoTrayFactory(opts MyGoTrayOptions) TrayFactory {
	return func(handle func(TrayAction)) (Tray, error) {
		if len(opts.NormalPNG) == 0 || len(opts.ErrorPNG) == 0 || handle == nil {
			return nil, errors.New("normal and error tray PNGs and action handler required")
		}
		item := func(id, label string, action TrayAction) *mygo.MenuItem {
			return &mygo.MenuItem{ID: id, Label: label, Click: func(_ *mygo.MenuItem, _ *mygo.Window) { handle(action) }}
		}
		status := &mygo.MenuItem{ID: "status", Label: "ToolNest 正在运行", Disabled: true}
		updates := item("updates", "版本与更新", UpdateAction)
		menu := mygo.NewMenu([]*mygo.MenuItem{status, mygo.Separator(), item("show", "显示主窗口", ShowAction), item("backup", "立即备份", BackupAction), item("settings", "设置", SettingsAction), updates, mygo.Separator(), item("exit", "退出 ToolNest", ExitAction)})
		native, err := mygo.NewTray(mygo.TrayOptions{ID: "toolnest.main", Icon: opts.NormalPNG, ToolTip: "ToolNest 正在运行", Menu: menu})
		if err != nil {
			return nil, err
		}
		t := &myGoTray{native: native, status: status, updates: updates, normal: append([]byte(nil), opts.NormalPNG...), errorIcon: append([]byte(nil), opts.ErrorPNG...)}
		// 固定版本 NewTray 会忽略 Shell_NotifyIcon 的失败；Bounds 才会查询系统实际图标。
		if !t.Available() {
			native.Destroy()
			return nil, errors.New("Windows notification-area icon could not be verified")
		}
		return t, nil
	}
}
func (t *myGoTray) Available() bool {
	if t.closed {
		return false
	}
	b := t.native.Bounds()
	return b.Width > 0 && b.Height > 0
}
func (t *myGoTray) SetState(message string, failed bool) error {
	if !t.Available() {
		return errors.New("tray unavailable")
	}
	prefix := "ToolNest"
	icon := t.normal
	if failed {
		prefix += " 错误"
		icon = t.errorIcon
	}
	label := fmt.Sprintf("%s：%s", prefix, message)
	if err := t.native.SetIcon(icon, false); err != nil {
		return err
	}
	t.native.SetToolTip(label)
	t.status.SetLabel(label)
	if !t.Available() {
		return errors.New("tray disappeared during status update")
	}
	return nil
}
func (t *myGoTray) Close() {
	if !t.closed {
		t.closed = true
		t.native.Destroy()
	}
}

func (t *myGoTray) SetUpdateState(state string) {
	if t.closed || state == t.updateState {
		return
	}
	label := "版本与更新"
	switch state {
	case "ready", "waiting-safe-switch":
		label = "更新已就绪…"
	case "blocked", "failed":
		label = "更新需要处理…"
	}
	t.updates.SetLabel(label)
	t.updateState = state
}
