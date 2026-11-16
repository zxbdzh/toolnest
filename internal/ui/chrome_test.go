package ui

import (
	"testing"

	"github.com/egoist/mygo"
	native "github.com/egoist/mygo/ui"
)

type testWindowControls struct {
	minimized int
	toggled   int
	maximized bool
}

func (window *testWindowControls) Minimize()         { window.minimized++ }
func (window *testWindowControls) IsMaximized() bool { return window.maximized }
func (window *testWindowControls) ToggleMaximize() {
	window.toggled++
	window.maximized = !window.maximized
}

func TestWindowOptionsKeepFrameWithCustomCaption(t *testing.T) {
	options := New(fixture()).WindowOptions("ToolNest")
	if options.TitleBarStyle != mygo.TitleBarHidden || !options.HideWindowButtons || options.Frameless {
		t.Fatal("custom caption must retain system resize edges, shadow and rounded corners")
	}
	if options.AlwaysOnTop || options.DisableMove || options.DisableResize || options.Content == nil {
		t.Fatal("custom caption changed the normal window behavior")
	}
}

func TestCaptionButtonsOperateWindowWithoutSubmittingBusinessActions(t *testing.T) {
	actions := fixture()
	controller := New(actions)
	window := &testWindowControls{}
	controller.window = window
	tester := native.NewTester(controller.render, 1180, 780)
	if err := tester.Click("最小化窗口"); err != nil {
		t.Fatal(err)
	}
	if window.minimized != 1 || len(actions.closed) != 0 {
		t.Fatal("minimizing must not hide to tray or request exit")
	}
	if err := tester.Click("最大化窗口"); err != nil {
		t.Fatal(err)
	}
	if !window.maximized || window.toggled != 1 {
		t.Fatal("maximize button did not maximize exactly once")
	}
	if err := tester.Click("还原窗口"); err != nil {
		t.Fatal(err)
	}
	if window.maximized || window.toggled != 2 {
		t.Fatal("restore button did not restore exactly once")
	}
	if actions.backups != 0 || len(actions.saved) != 0 || len(actions.closed) != 0 {
		t.Fatal("window controls submitted unrelated business actions")
	}
}

func TestCustomCloseKeepsTrayAndConfirmationPolicy(t *testing.T) {
	for _, behavior := range []string{"ask", "tray", "exit"} {
		t.Run(behavior, func(t *testing.T) {
			actions := fixture()
			actions.state.Settings.CloseBehavior = behavior
			controller := New(actions)
			tester := native.NewTester(controller.render, 620, 520)
			if err := tester.Click("关闭窗口"); err != nil {
				t.Fatal(err)
			}
			if behavior == "ask" {
				if controller.modal != "close" || len(actions.closed) != 0 {
					t.Fatal("custom close bypassed the user's close confirmation")
				}
				return
			}
			if len(actions.closed) != 1 || actions.closed[0] != behavior {
				t.Fatal("custom close did not preserve the configured tray/exit behavior")
			}
		})
	}
}

func TestCaptionFitsPagesAndKeyboardAccessAtMinimumSize(t *testing.T) {
	for _, dark := range []bool{false, true} {
		controller := New(fixture())
		tester := native.NewTesterWith(controller.render, 620, 520, native.TesterOptions{Dark: dark})
		for _, page := range []string{"概览", "PATH 保护", "快照", "应用设置"} {
			if err := tester.Click(page); err != nil {
				t.Fatal(err)
			}
			for _, label := range []string{"最小化窗口", "最大化窗口", "关闭窗口"} {
				accessible, exists := tester.AccessByRole(native.RoleButton, label)
				if !exists || accessible.Disabled || accessible.Bounds.W < 32 || accessible.Bounds.H < 32 ||
					accessible.Bounds.X < 160 || accessible.Bounds.X+accessible.Bounds.W > 620 ||
					accessible.Bounds.Y < 0 || accessible.Bounds.Y+accessible.Bounds.H > 64 {
					t.Fatalf("page=%s dark=%v control=%s inaccessible or outside title bar: %#v", page, dark, label, accessible)
				}
			}
		}
	}
}
