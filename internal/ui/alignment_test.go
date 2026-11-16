package ui

import (
	"fmt"
	"math"
	"testing"
	"time"

	native "github.com/egoist/mygo/ui"
)

func TestSegmentedHighlightMatchesActualSegment(t *testing.T) {
	labels := []string{"用户 PATH", "系统 PATH", "与当前比较"}
	selected := 2
	tester := native.NewTester(func(context *native.Context) {
		ensureMisans()
		theme := refinedTheme(native.LightTheme())
		context.SetTheme(&theme)
		context.Root().Background(native.Hex("#23374f"))
		native.Column(context).Padding(16).Children(func() {
			segmentedControl(context, "snapshot-detail-tabs", &selected, labels...)
		})
	}, 560, 100)
	group, found := tester.AccessByRole(native.RoleRadioGroup, labels[selected])
	if !found {
		t.Fatal("segmented group missing")
	}
	option, found := tester.AccessByRole(native.RoleRadio, labels[selected])
	if !found {
		t.Fatal("selected option missing")
	}
	image := tester.Image()
	left, right := image.Bounds().Max.X, -1
	for y := int(group.Bounds.Y); y < int(group.Bounds.Y+group.Bounds.H); y++ {
		for x := int(group.Bounds.X); x < int(group.Bounds.X+group.Bounds.W); x++ {
			pixel := image.RGBAAt(x, y)
			if pixel.R > 190 && pixel.G > 190 && pixel.B > 190 {
				left, right = min(left, x), max(right, x)
			}
		}
	}
	if right < left {
		t.Fatal("selection highlight not visible")
	}
	center := float64(left+right+1) / 2
	optionCenter := float64(option.Bounds.X + option.Bounds.W/2)
	if math.Abs(center-optionCenter) > 1 {
		t.Fatalf("selection highlight is offset from selected label: highlight center=%.2f, option center=%.2f, option=%+v", center, optionCenter, option.Bounds)
	}
	last, found := tester.AccessByRole(native.RoleRadio, labels[len(labels)-1])
	if !found || group.Bounds.X+group.Bounds.W-last.Bounds.X-last.Bounds.W > 4 {
		t.Fatalf("compact segmented track stretches past its options: track=%+v last=%+v", group.Bounds, last.Bounds)
	}
}

func assertSegmentedAlignment(t *testing.T, tester *native.Tester, width, height int, labels ...string) {
	t.Helper()
	var group native.AccessInfo
	for _, label := range labels {
		if candidate, found := tester.AccessByRole(native.RoleRadioGroup, label); found {
			group = candidate
			break
		}
	}
	if group.Bounds.W <= 0 || group.Bounds.X < 0 || group.Bounds.Y < 0 ||
		group.Bounds.X+group.Bounds.W > float32(width)+0.5 || group.Bounds.Y+group.Bounds.H > float32(height)+0.5 {
		t.Fatalf("segmented group outside viewport: labels=%v bounds=%+v", labels, group.Bounds)
	}
	var first, last native.Rect
	for index, label := range labels {
		option, found := tester.AccessByRole(native.RoleRadio, label)
		if !found || option.Disabled || option.Bounds.W <= 0 {
			t.Fatalf("segment unavailable: %s %+v", label, option)
		}
		if math.Abs(float64(option.Bounds.Y+option.Bounds.H/2-group.Bounds.Y-group.Bounds.H/2)) > 0.5 ||
			option.Bounds.X < group.Bounds.X || option.Bounds.X+option.Bounds.W > group.Bounds.X+group.Bounds.W {
			t.Fatalf("segment not centered inside track: label=%s track=%+v segment=%+v", label, group.Bounds, option.Bounds)
		}
		if index == 0 {
			first = option.Bounds
		} else if math.Abs(float64(last.X+last.W-option.Bounds.X)) > 0.5 {
			t.Fatalf("gap or overlap between segments: previous=%+v next=%+v", last, option.Bounds)
		}
		last = option.Bounds
	}
	left := first.X - group.Bounds.X
	right := group.Bounds.X + group.Bounds.W - last.X - last.W
	if left < 2 || left > 4 || math.Abs(float64(left-right)) > 0.5 {
		t.Fatalf("track has uneven padding or excess empty width: labels=%v left=%v right=%v", labels, left, right)
	}
}

func TestSharedSegmentedControlsAcrossPagesAndScale(t *testing.T) {
	for _, scenario := range []struct {
		width, height int
		dark          bool
		size          string
		scale         float32
	}{
		{620, 520, false, "compact", 1},
		{620, 520, true, "comfortable", 1.25},
		{780, 600, false, "comfortable", 1.5},
		{900, 650, true, "standard", 2},
		{1180, 780, false, "standard", 1},
		{1180, 780, true, "comfortable", 1.25},
		{2048, 1114, false, "comfortable", 1.5},
		{2048, 1114, true, "compact", 2},
	} {
		t.Run(fmt.Sprintf("%dx%d-dark%v-%s-scale%v", scenario.width, scenario.height, scenario.dark, scenario.size, scenario.scale), func(t *testing.T) {
			actions := fixture()
			actions.state.Settings.TextSize = scenario.size
			controller := New(actions)
			controller.ShowSettings()
			// 几何矩阵用实底和即时反馈检查，不在每个尺寸重复跑整面 CPU 磨砂；真实材质另有像素回归和截图。
			tester := native.NewTesterWith(controller.render, scenario.width, scenario.height, native.TesterOptions{
				Dark: scenario.dark, Preferences: native.Preferences{HighContrast: true, ReduceMotion: true},
			})
			tester.SetScale(scenario.scale)
			check := func(labels ...string) {
				assertSegmentedAlignment(t, tester, scenario.width, scenario.height, labels...)
			}
			check("通用", "高级", "更新")
			check("跟随系统", "浅色", "深色")
			check("小", "标准", "大")
			check("每次询问", "隐藏托盘", "退出应用")
			if err := tester.Click("高级"); err != nil {
				t.Fatal(err)
			}
			if err := tester.Click("配置代理"); err != nil {
				t.Fatal(err)
			}
			check("跟随系统", "直连", "自定义")
			controller.page, controller.tab = "path", 2
			controller.draft.ScheduleEnabled = true
			tester.SetSize(scenario.width, scenario.height)
			check("间隔备份", "每天定时")
			controller.tab = 0
			tester.SetSize(scenario.width, scenario.height)
			if float32(scenario.width)-sidebarWidth(float32(scenario.width)) < 800 {
				check("用户 PATH", "系统 PATH")
			}
			controller.tab, controller.snapshotDetailOpen = 1, true
			tester.SetSize(scenario.width, scenario.height)
			check("用户 PATH", "系统 PATH", "与当前比较")
			for _, label := range []string{"系统 PATH", "与当前比较", "用户 PATH"} {
				if err := tester.Click(label); err != nil {
					t.Fatal(err)
				}
				tester.Advance(250 * time.Millisecond)
				check("用户 PATH", "系统 PATH", "与当前比较")
			}
		})
	}
}
