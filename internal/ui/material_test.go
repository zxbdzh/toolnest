package ui

import (
	"image/color"
	"testing"
	"time"

	native "github.com/egoist/mygo/ui"
)

func TestControlPressCancelsAndCommitsOnlyOnRelease(t *testing.T) {
	for _, reduceMotion := range []bool{false, true} {
		name := "animated"
		if reduceMotion {
			name = "reduce-motion"
		}
		t.Run(name, func(t *testing.T) {
			committed := 0
			tester := native.NewTesterWith(func(context *native.Context) {
				theme := refinedTheme(native.LightTheme())
				context.SetTheme(&theme)
				native.Column(context).Padding(24).Children(func() {
					if primaryButton(context, "保存", controlOptions{key: "save"}).Clicked() {
						committed++
					}
				})
			}, 320, 140, native.TesterOptions{Preferences: native.Preferences{ReduceMotion: reduceMotion}})
			bounds, exists := tester.Find("保存")
			if !exists {
				t.Fatal("primary control missing")
			}
			x, y := bounds.X+bounds.W/2, bounds.Y+bounds.H/2
			tester.Press(x, y)
			tester.Advance(75 * time.Millisecond)
			if committed != 0 {
				t.Fatal("press feedback committed before release")
			}
			tester.Release(300, 120)
			tester.Advance(240 * time.Millisecond)
			if committed != 0 {
				t.Fatal("releasing outside the control did not cancel")
			}
			// 在回弹结束前重新按下，验证可中断反馈不会吞掉或重复提交操作。
			tester.Press(x, y)
			tester.Advance(30 * time.Millisecond)
			tester.Release(x, y)
			tester.Advance(30 * time.Millisecond)
			if committed != 1 {
				t.Fatalf("expected exactly one release action, got %d", committed)
			}
		})
	}
}

func TestSnapshotSelectionKeepsFullThemeAndAccessibleControls(t *testing.T) {
	controller := New(fixture())
	controller.page, controller.tab = "path", 1
	tester := native.NewTester(controller.render, 1180, 780)
	for _, label := range []string{"查看快照 snapshot-1", "恢复快照…"} {
		if button, exists := tester.AccessByRole(native.RoleButton, label); !exists || button.Disabled {
			t.Fatalf("snapshot action lost behind list styling: %s", label)
		}
	}
	if err := tester.Click("选择快照 snapshot-1"); err != nil {
		t.Fatal(err)
	}
	if checkbox, exists := tester.AccessByRole(native.RoleCheckBox, "选择快照 snapshot-1"); !exists || !checkbox.Checked {
		t.Fatal("inset selection styling hid checkbox state")
	}
	if controller.renderTheme().Accent.A != 255 {
		t.Fatal("list-only accent override leaked into the application theme")
	}
}

func TestFrostedMaterialSamplesBackdropAndHonorsAccessibility(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, preferences := range []native.Preferences{{}, {ReduceTransparency: true}, {HighContrast: true}, {ReduceTransparency: true, HighContrast: true}} {
			render := func(background native.Color) color.RGBA {
				tester := native.NewTesterWith(func(context *native.Context) {
					source := native.LightTheme()
					if dark {
						source = native.DarkTheme()
					}
					theme := refinedTheme(source)
					context.SetTheme(&theme)
					context.Root().Background(background)
					frostedSurface(context, native.Box(context).Fill(), false)
				}, 240, 120, native.TesterOptions{Dark: dark, Preferences: preferences})
				return tester.Image().RGBAAt(120, 60)
			}
			blue, warm := render(native.Hex("#335c88")), render(native.Hex("#88664c"))
			if preferences.HighContrast {
				if blue != warm || blue.A != 255 {
					t.Fatalf("accessibility fallback depends on backdrop: dark=%v preferences=%+v", dark, preferences)
				}
			} else if blue == warm {
				t.Fatalf("frosted surface stopped sampling its actual backdrop: dark=%v", dark)
			}
		}
	}
}

func TestWindowsTransparencySwitchDoesNotChangeApplicationGlass(t *testing.T) {
	for _, dark := range []bool{false, true} {
		render := func(reduceTransparency bool) color.RGBA {
			controller := New(fixture())
			tester := native.NewTesterWith(controller.render, 1180, 780, native.TesterOptions{
				Dark: dark, Preferences: native.Preferences{ReduceTransparency: reduceTransparency},
			})
			return tester.Image().RGBAAt(110, 190)
		}
		if enabled, disabled := render(false), render(true); enabled != disabled {
			t.Fatalf("Windows transparency setting changed app glass: dark=%v on=%v off=%v", dark, enabled, disabled)
		}
	}
}

func TestApplicationFrostActuallyBlursContent(t *testing.T) {
	render := func(reduceTransparency bool) color.RGBA {
		tester := native.NewTesterWith(func(context *native.Context) {
			theme := refinedTheme(native.LightTheme())
			context.SetTheme(&theme)
			native.Row(context).Fill().Draw(func(painter *native.Painter, bounds native.Rect) {
				painter.Fill(native.Rect{X: 0, Y: 0, W: 120, H: 120}, native.Hex("#ffffff"), 0)
				painter.Fill(native.Rect{X: 120, Y: 0, W: 120, H: 120}, native.Hex("#000000"), 0)
			}).Children(func() {
				native.Box(context).Fill().Material(frostMaterial{tint: native.Transparent})
			})
		}, 240, 120, native.TesterOptions{Preferences: native.Preferences{ReduceTransparency: reduceTransparency}})
		return tester.Image().RGBAAt(119, 60)
	}
	for _, reduceTransparency := range []bool{false, true} {
		color := render(reduceTransparency)
		if color.R < 80 || color.R > 190 || color.A != 255 {
			t.Fatalf("expected mixed edge pixels from actual blur, got %v with system transparency off=%v", color, reduceTransparency)
		}
	}
}

func TestFrostedShellKeepsNavigationAndActionsClickable(t *testing.T) {
	for _, width := range []int{620, 1180} {
		for _, preferences := range []native.Preferences{{}, {ReduceMotion: true}, {ReduceTransparency: true}, {HighContrast: true}} {
			controller := New(fixture())
			tester := native.NewTesterWith(controller.render, width, 780, native.TesterOptions{Preferences: preferences})
			// 环境色场虽然在布局树里是全屏节点，也不能遮挡正常页面的鼠标命中。
			if err := tester.Click("应用设置"); err != nil {
				t.Fatal(err)
			}
			if controller.page != "settings" || !tester.HasText("字号") {
				t.Fatalf("ambient backdrop blocked settings at width %d", width)
			}
			if err := tester.Click("快照"); err != nil {
				t.Fatal(err)
			}
			if controller.page != "path" || controller.tab != 1 {
				t.Fatalf("ambient backdrop blocked snapshots at width %d", width)
			}
			if checkbox, exists := tester.AccessByRole(native.RoleCheckBox, "选择快照 snapshot-1"); !exists || checkbox.Disabled {
				t.Fatal("frosted list lost its independent checkbox")
			}
		}
	}
}

func TestNavigationMotionRemainsInterruptibleWithoutDelayingPageChange(t *testing.T) {
	for _, reduceMotion := range []bool{false, true} {
		controller := New(fixture())
		tester := native.NewTesterWith(controller.render, 1180, 780, native.TesterOptions{Preferences: native.Preferences{ReduceMotion: reduceMotion}})
		if err := tester.Click("快照"); err != nil {
			t.Fatal(err)
		}
		if controller.page != "path" || controller.tab != 1 || !tester.HasText("快照库") {
			t.Fatal("page change waited for the navigation animation")
		}
		tester.Advance(60 * time.Millisecond)
		if err := tester.Click("概览"); err != nil {
			t.Fatal(err)
		}
		tester.Advance(30 * time.Millisecond)
		if err := tester.Click("自动备份"); err != nil {
			t.Fatal(err)
		}
		tester.Advance(400 * time.Millisecond)
		if controller.page != "path" || controller.tab != 2 || !tester.HasText("当前计划") {
			t.Fatal("interrupted navigation animation restored an outdated page")
		}
	}
}
