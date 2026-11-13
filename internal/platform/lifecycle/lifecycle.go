// Package lifecycle coordinates Windows desktop entry points without owning UI or PATH writes.
package lifecycle

import (
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"
)

var (
	ErrConflict    = errors.New("entry changed or belongs to another application")
	ErrNotOwned    = errors.New("entry ownership is not confirmed")
	ErrUnsupported = errors.New("Windows desktop integration is unavailable")
)

const AutostartArgument = "--autostart"

// Launch is deliberately not a general command dispatcher: helper/update modes stay with main.
type Launch uint8

const (
	ManualLaunch Launch = iota
	AutostartLaunch
)

// ParseLaunch only accepts the exact Run argument list; unknown/helper arguments never hide UI.
func ParseLaunch(args []string) Launch {
	if slices.Equal(args, []string{AutostartArgument}) {
		return AutostartLaunch
	}
	return ManualLaunch
}

// Hooks are bound by main/UI. Post must enqueue onto the UI event loop and must not block.
// RequestExit asks the application's busy-operation coordinator to quit; it must not force-kill work.
// BackupNow must enqueue work, not execute a blocking backup on the UI thread.
type Hooks struct {
	Post         func(func())
	ShowMain     func()
	HideMain     func()
	OpenSettings func()
	BackupNow    func()
	RequestExit  func()
	ReportError  func(error)
	OpenUpdates  func()
}

type TrayAction uint8

const (
	ShowAction TrayAction = iota
	BackupAction
	SettingsAction
	ExitAction
	UpdateAction
)

// Tray must positively verify native availability; construction alone is not sufficient.
// SetState and Available run on the UI thread. Close must not exit the application.
type Tray interface {
	Available() bool
	SetState(message string, failed bool) error
	Close()
}

type TrayFactory func(func(TrayAction)) (Tray, error)

// Controller never installs a close-window handler: ask/remember/exit remain UI decisions.
type Controller struct {
	mu           sync.Mutex
	hooks        Hooks
	tray         Tray
	closed       bool
	trayLost     bool
	probePending bool
	watchStop    chan struct{}
}

func NewController(h Hooks) (*Controller, error) {
	if h.Post == nil || h.ShowMain == nil || h.HideMain == nil || h.OpenSettings == nil || h.BackupNow == nil || h.RequestExit == nil || h.ReportError == nil {
		return nil, errors.New("all lifecycle Hooks are required")
	}
	return &Controller{hooks: h}, nil
}

// Start must be called on the UI thread after the window exists. No autostart preference is changed.
func (c *Controller) Start(launch Launch, startInTray bool, factory TrayFactory) {
	c.mu.Lock()
	alreadyStarted := c.closed || c.tray != nil
	c.mu.Unlock()
	if alreadyStarted {
		return
	} // 固定托盘 ID 不能重复构造后再销毁，否则会移除原有图标。
	var t Tray
	var err error
	if factory != nil {
		t, err = factory(c.HandleAction)
	} else {
		err = errors.New("tray factory is not configured")
	}
	if err == nil && (t == nil || !t.Available()) {
		err = errors.New("system tray is unavailable")
	}
	c.mu.Lock()
	if c.closed || c.tray != nil {
		c.mu.Unlock()
		if t != nil {
			t.Close()
		}
		return
	}
	if err == nil {
		c.tray = t
		c.watchStop = make(chan struct{})
		go c.watchTray(c.watchStop)
	}
	c.mu.Unlock()
	if err != nil {
		if t != nil {
			t.Close()
		}
		c.hooks.ShowMain()
		c.hooks.ReportError(fmt.Errorf("托盘不可用，窗口保持可见: %w", err))
		return
	}
	if launch == AutostartLaunch && startInTray {
		c.hooks.HideMain()
	} else {
		c.hooks.ShowMain()
	}
}

func (c *Controller) post(fn func()) {
	c.hooks.Post(func() {
		c.mu.Lock()
		closed := c.closed
		c.mu.Unlock()
		if !closed {
			fn()
		}
	})
}

// SecondLaunch is safe before UI readiness when Post queues until the loop is ready.
func (c *Controller) SecondLaunch(launch Launch) {
	// 自启的重复进程不能打扰用户；手动打开无论初次偏好如何都唤回主窗口。
	if launch != AutostartLaunch {
		c.post(c.hooks.ShowMain)
	}
}

func (c *Controller) HandleAction(action TrayAction) {
	c.post(func() {
		switch action {
		case ShowAction:
			c.hooks.ShowMain()
		case BackupAction:
			c.hooks.BackupNow()
		case SettingsAction:
			c.hooks.ShowMain()
			c.hooks.OpenSettings()
		case ExitAction:
			c.hooks.RequestExit()
		case UpdateAction:
			if c.hooks.OpenUpdates != nil {
				c.hooks.ShowMain()
				c.hooks.OpenUpdates()
			}
		}
	})
}

// Hide only hides after a fresh availability check; UI close-confirmation calls this explicitly.
func (c *Controller) Hide() {
	c.post(func() {
		c.mu.Lock()
		t := c.tray
		c.mu.Unlock()
		if t == nil || !t.Available() {
			c.hooks.ShowMain()
			c.hooks.ReportError(errors.New("托盘不可用，不能隐藏窗口"))
			return
		}
		c.hooks.HideMain()
	})
}

// SetStatus keeps failures visible in-app even when native notifications are unavailable.
func (c *Controller) SetStatus(message string, failed bool) {
	c.post(func() {
		c.mu.Lock()
		t := c.tray
		c.mu.Unlock()
		if t == nil {
			return
		}
		if !t.Available() {
			c.hooks.ShowMain()
			c.hooks.ReportError(errors.New("托盘已不可用，窗口已恢复"))
			return
		}
		if err := t.SetState(message, failed); err != nil {
			c.hooks.ShowMain()
			c.hooks.ReportError(err)
		}
	})
}

// ProbeTray must be dispatched from the UI loop; tests can invoke it without timers.
func (c *Controller) ProbeTray() {
	c.mu.Lock()
	t, closed := c.tray, c.closed
	c.probePending = false
	c.mu.Unlock()
	if closed || t == nil {
		return
	}
	available := t.Available()
	c.mu.Lock()
	lost := c.trayLost
	c.trayLost = !available
	c.mu.Unlock()
	// 托盘消失后立即恢复主入口；持续失效不重复抢焦点或弹出同一错误。
	if !available && !lost {
		c.hooks.ShowMain()
		c.hooks.ReportError(errors.New("托盘已不可用，窗口已恢复"))
	}
}
func (c *Controller) watchTray(stop <-chan struct{}) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		c.mu.Lock()
		if c.closed || c.probePending {
			c.mu.Unlock()
			continue
		}
		c.probePending = true
		c.mu.Unlock()
		c.post(c.ProbeTray)
	}
}

// Close must run on the UI thread, after main has safely stopped critical operations.
// It releases tray resources synchronously before the UI queue shuts down.
func (c *Controller) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	if c.watchStop != nil {
		close(c.watchStop)
	}
	t := c.tray
	c.tray = nil
	c.mu.Unlock()
	if t != nil {
		t.Close()
	}
}
