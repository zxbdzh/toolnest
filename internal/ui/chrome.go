package ui

import native "github.com/egoist/mygo/ui"

type windowControls interface {
	Minimize()
	ToggleMaximize()
	IsMaximized() bool
}

func (controller *Controller) captionButtons(context *native.Context) {
	native.Row(context.Key("window-controls")).Gap(3).Margin(0, 0, 0, 8).Children(func() {
		if captionButton(context, "最小化窗口", minimizeIcon, false).Clicked() && controller.window != nil {
			controller.window.Minimize()
		}
		label, icon := "最大化窗口", maximizeIcon
		if controller.window != nil && controller.window.IsMaximized() {
			label, icon = "还原窗口", restoreWindowIcon
		}
		// 注册真实按钮命中区域，保留 Windows 11 的悬停贴靠布局。
		if captionButton(context, label, icon, false).MaximizeButton().Clicked() && controller.window != nil {
			controller.window.ToggleMaximize()
		}
		if captionButton(context, "关闭窗口", closeWindowIcon, true).Clicked() {
			// 关闭仍经过配置、托盘可用性与业务确认，不能直接销毁窗口。
			controller.RequestClose()
		}
	})
}

func captionButton(context *native.Context, label string, icon *native.SVG, closeButton bool) native.Element {
	theme := context.Theme()
	button := native.ButtonBase(context.Key(label)).Label(label).Tooltip(label).Width(32).Height(32).
		Radius(10).AlignItems(native.Center).Justify(native.Center).FocusRing(false).
		Transition(colorTransition(context))
	background, foreground := native.Transparent, theme.TextMuted
	if button.Pressed() {
		background = theme.SurfacePressed
	} else if button.Hovered() {
		background = theme.SurfaceHover
	}
	if closeButton && (button.Hovered() || button.Pressed()) {
		background, foreground = theme.Danger, native.Hex("#ffffff")
		if button.Pressed() {
			background = background.Mix(native.Hex("#000000"), 0.16)
		}
	}
	button.Background(background)
	if button.FocusVisible() {
		button.Draw(func(painter *native.Painter, bounds native.Rect) {
			painter.Stroke(native.Rect{X: bounds.X + 2, Y: bounds.Y + 2, W: bounds.W - 4, H: bounds.H - 4}, theme.Accent, 8, 1)
		})
	}
	button.Children(func() {
		native.Icon(context, icon).Width(14).Height(14).TextColor(foreground)
	})
	return button
}

var minimizeIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="M5 12h14"/></svg>`))
var maximizeIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"><rect x="5" y="5" width="14" height="14" rx="2"/></svg>`))
var restoreWindowIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linejoin="round"><path d="M9 8V5a1 1 0 0 1 1-1h9a1 1 0 0 1 1 1v9a1 1 0 0 1-1 1h-3"/><rect x="4" y="8" width="12" height="12" rx="1.5"/></svg>`))
var closeWindowIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round"><path d="m6 6 12 12M18 6 6 18"/></svg>`))
