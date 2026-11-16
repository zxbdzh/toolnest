package ui

import (
	"time"

	native "github.com/egoist/mygo/ui"
)

func sidebarWidth(width float32) float32 {
	if width < 900 {
		return 160
	}
	return 208
}

func (controller *Controller) sidebar(context *native.Context, width float32) {
	theme := context.Theme()
	// 导航只包裹实际入口，不再把空白撑成一整条后台侧栏。
	sidebar := native.Column(context.Key("sidebar")).Width(sidebarWidth(width)-24).
		Margin(12).Gap(14).Padding(12, 0, 4, 0)
	sidebar.Children(func() {
		native.Row(context).Height(40).Gap(9).Padding(0, 12).Margin(0, 0, 10, 0).DragWindow().Children(func() {
			native.Row(context).Width(28).Height(28).Radius(9).AlignItems(native.Center).Justify(native.Center).
				Gradient(native.Hex("#7b8da2"), native.Hex("#53677d"), 155).
				InsetShadow(0, 1, 0, 0, native.Hex("#ffffff").Alpha(0.32)).Children(func() {
				native.Icon(context, nestIcon).Width(17).Height(17).TextColor(native.Hex("#ffffff"))
			})
			native.Text(context, "ToolNest").FontSize(sectionSize(context)).FontWeight(500)
		})
		selectedPage := -1
		if controller.page == "home" {
			selectedPage = 0
		} else if controller.page == "path" {
			selectedPage = controller.tab + 1
		}
		navigationDock(context, "navigation-dock", selectedPage).Children(func() {
			for _, item := range []struct {
				label, page string
				tab         int
				icon        *native.SVG
			}{
				{"概览", "home", 0, homeIcon},
				{"PATH 保护", "path", 0, shieldIcon},
				{"快照", "path", 1, archiveIcon},
				{"自动备份", "path", 2, backupIcon},
				{"活动记录", "path", 3, recordsIcon},
			} {
				selected := controller.page == item.page && (item.page == "home" || controller.tab == item.tab)
				if navigationItem(context, item.label, item.icon, selected).Clicked() {
					controller.page, controller.tab = item.page, item.tab
				}
			}
		})
		native.Spacer(context)
		if controller.view.Isolated {
			native.Text(context, "隔离测试 · 合成数据").FontSize(detailSize(context)).TextColor(theme.Warning).Padding(8)
		}
		selectedUtility := -1
		if controller.page == "settings" {
			selectedUtility = 0
			if controller.settingsSection == 4 {
				selectedUtility = 1
			}
		}
		navigationDock(context, "utility-dock", selectedUtility).Children(func() {
			if navigationItem(context, "应用设置", settingsIcon, controller.page == "settings" && controller.settingsSection != 4).Tooltip("应用设置 · Ctrl+,").Clicked() {
				controller.ShowSettings()
			}
			if navigationItem(context, "关于与更新", updateIcon, controller.page == "settings" && controller.settingsSection == 4).Clicked() {
				controller.ShowUpdates()
			}
		})
		native.Text(context, "v"+controller.view.Version).FontSize(detailSize(context)).TextColor(theme.TextMuted).Padding(6, 8, 2, 8).SingleLine()
	})
}

func navigationDock(context *native.Context, key string, selected int) native.Element {
	theme := context.Theme()
	dock := sidebarSurface(context, native.Column(context.Key(key)).FillWidth().Gap(6).Padding(8))
	position := dock.AnimateTo("active-position", float32(max(0, selected)), native.Animation{Spring: native.SpringOptions{Duration: 240 * time.Millisecond}})
	visible := float32(0)
	if selected >= 0 {
		visible = 1
	}
	opacity := dock.AnimateTo("active-opacity", visible, native.Animation{Duration: 150 * time.Millisecond})
	// 一个连续滑移的选中底承接页面切换，子按钮只处理命中与文本，不重复画七个背景。
	return dock.Draw(func(painter *native.Painter, bounds native.Rect) {
		pill := native.Rect{X: bounds.X + 8, Y: bounds.Y + 8 + position*46, W: bounds.W - 16, H: 40}
		fill, edge := native.Hex("#ffffff").Alpha(0.7*opacity), native.Hex("#ffffff").Alpha(0.58*opacity)
		if theme.Dark {
			fill, edge = native.Hex("#a4b7cd").Alpha(0.18*opacity), native.Hex("#ffffff").Alpha(0.1*opacity)
		}
		painter.Fill(pill, fill, 15)
		painter.Stroke(pill, edge, 15, 1)
	})
}

func sectionLabel(context *native.Context, label string) {
	native.Text(context, label).FontSize(detailSize(context)).FontWeight(600).TextColor(context.Theme().TextMuted).
		Padding(8, 12, 4, 12)
}

func navigationItem(context *native.Context, label string, icon *native.SVG, selected bool) native.Element {
	theme := context.Theme()
	button := native.ButtonBase(context.Key("nav-"+label)).Label(label).Tooltip(label).FillWidth().Height(40).
		Padding(0, 8).Gap(9).Justify(native.Start).Radius(15).AlignItems(native.Center).
		FocusRing(false).Transition(colorTransition(context))
	iconSize := float32(19)
	if width, _ := context.Size(); width < 900 {
		button.Padding(0, 8).Gap(8)
		iconSize = 16
	}
	color, iconColor := theme.Text, theme.TextMuted
	base := native.Transparent
	if selected {
		iconColor = theme.Accent
		if theme.Dark {
			iconColor = native.Hex("#c1d1e3")
		}
	}
	switch {
	case button.Pressed():
		base = theme.SurfacePressed
	case button.Hovered() && !selected:
		base = native.Hex("#ffffff").Alpha(0.4)
		if theme.Dark {
			base = native.Hex("#ffffff").Alpha(0.06)
		}
	}
	button.Background(base)
	pressScale := float32(1)
	if button.Pressed() {
		pressScale = 0.975
	}
	button.AnimatePose(native.Pose{Scale: pressScale}, native.Animation{Spring: native.SpringOptions{Duration: 180 * time.Millisecond}})
	if button.FocusVisible() {
		button.Draw(func(painter *native.Painter, bounds native.Rect) {
			painter.Fill(native.Rect{X: bounds.X + 2, Y: bounds.Y + 7, W: 3, H: max(12, bounds.H-14)}, theme.Accent, 1.5)
		})
	}
	button.Children(func() {
		iconScale := float32(1)
		if button.Hovered() && !button.Pressed() {
			iconScale = 1.09
		}
		native.Icon(context, icon).Width(iconSize).Height(iconSize).TextColor(iconColor).
			AnimatePose(native.Pose{Scale: iconScale}, native.Animation{Spring: native.SpringOptions{Duration: 220 * time.Millisecond}})
		text := native.Text(context, label).FontSize(controlTextSize(context)).TextColor(color).SingleLine()
		if selected {
			text.FontWeight(500)
		}
	})
	return button
}

func (controller *Controller) shortcuts(context *native.Context) {
	// 模态确认期间不切换页面，也不提交刷新，以免键盘操作绕过恢复预览。
	if controller.modal != "" {
		return
	}
	for index := range 5 {
		context.OnShortcut(native.Cmd, native.Key1+native.Key(index), func() {
			controller.page, controller.tab = "path", index-1
			if index == 0 {
				controller.page, controller.tab = "home", 0
			}
		})
	}
	context.OnShortcut(native.Cmd, native.KeyComma, controller.ShowSettings)
	context.OnShortcut(native.Cmd, native.KeyR, func() {
		controller.launch("读取 PATH", controller.actions.Refresh)
	})
}

var homeIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="3" width="7" height="7" rx="1.5"/><rect x="14" y="3" width="7" height="7" rx="1.5"/><rect x="3" y="14" width="7" height="7" rx="1.5"/><rect x="14" y="14" width="7" height="7" rx="1.5"/></svg>`))
var historyIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M3 11a9 9 0 1 1 2 7M3 4v7h7M12 7v5l3 2"/></svg>`))
