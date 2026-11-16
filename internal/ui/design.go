package ui

import (
	"strings"
	"time"

	"toolnest/internal/app"

	native "github.com/egoist/mygo/ui"
)

// 灰阶承载界面层级，灰蓝只标识主操作、选中和链接；避免整页偏绿或偏青。
func refinedTheme(source *native.Theme, settings ...app.Settings) native.Theme {
	baseSize := float32(14)
	if len(settings) > 0 {
		switch settings[0].TextSize {
		case "compact":
			baseSize = 13
		case "comfortable":
			baseSize = 16
		}
	}
	theme := *source.WithAccent(native.Hex("#52697f"))
	// 字号档位直接写进主题，原生输入框、表格和按钮共享同一正文基准。
	theme.Font, theme.FontSize, theme.Spacing, theme.Radius = misansFamily, baseSize, 4, controlRadius
	if theme.Dark {
		theme.Background, theme.Surface = native.Hex("#202123"), native.Hex("#292b2e")
		theme.SurfaceHover, theme.SurfacePressed = native.Hex("#34373b"), native.Hex("#3e4247")
		theme.Text, theme.TextMuted, theme.Border = native.Hex("#f0f0f2"), native.Hex("#a6a8af"), native.Hex("#3b3d42")
		theme.Accent, theme.AccentHover, theme.AccentPressed = native.Hex("#718da8"), native.Hex("#819bb4"), native.Hex("#607d97")
		theme.AccentText = native.Hex("#ffffff")
	} else {
		theme.Background, theme.Surface = native.Hex("#f5f5f7"), native.Hex("#eceef1")
		theme.SurfaceHover, theme.SurfacePressed = native.Hex("#edf0f4"), native.Hex("#e1e5eb")
		theme.Text, theme.TextMuted, theme.Border = native.Hex("#282a30"), native.Hex("#626976"), native.Hex("#dedfe4")
		theme.Accent, theme.AccentHover, theme.AccentPressed = native.Hex("#52697f"), native.Hex("#455c71"), native.Hex("#394d60")
		theme.AccentText = native.Hex("#ffffff")
	}
	theme.Focus = mistLink(&theme)
	// MyGo 会对非活动窗口的表格行自动套用 InactiveAccent；保持同一强调色，
	// 避免窗口失焦、截图或后台巡检时突然换出另一套高对比底色。
	theme.InactiveAccent = theme.Accent
	theme.Selection = theme.Accent.Alpha(0.16)
	return theme
}

func panelColor(theme *native.Theme) native.Color {
	if theme.Dark {
		return native.Hex("#232629")
	}
	return native.Hex("#ffffff")
}

// 实底色只用于表格与减少透明度回退；工作面本身由磨砂材质承载。
func contentColor(theme *native.Theme) native.Color {
	if theme.Dark {
		return native.Hex("#242528")
	}
	return native.Hex("#fbfbfc")
}

func frostedSurface(context *native.Context, element native.Element, weak bool) native.Element {
	tint, rim := native.Hex("#ffffff").Alpha(0.62), native.Hex("#ffffff").Alpha(0.62)
	if context.Theme().Dark {
		tint, rim = native.Hex("#202832").Alpha(0.74), native.Hex("#ffffff").Alpha(0.09)
	}
	if context.Preferences().HighContrast {
		rim = context.Theme().Border
	}
	return element.Material(frostMaterial{tint: tint, weak: weak}).
		Border(1, rim).TextColor(context.Theme().Text).
		Shadow(0, 5, 18, -6, native.Hex("#152a49").Alpha(0.12)).
		InsetShadow(0, 1, 0, 0, native.Hex("#ffffff").Alpha(0.18))
}

func ambientBackdrop(context *native.Context) {
	top, bottom := native.Hex("#d5e4f1"), native.Hex("#e9e3ef")
	if context.Theme().Dark {
		top, bottom = native.Hex("#1a2938"), native.Hex("#2b2638")
	}
	// 基础环境色挂根背景；后面的透色色场显式放到正常布局之下。
	context.Root().Gradient(top, bottom, 135)
	if context.Preferences().HighContrast {
		return
	}
	width, height := context.Size()
	blue, pearl := native.Hex("#9ebfe1").Alpha(0.6), native.Hex("#f4ddd3").Alpha(0.65)
	if context.Theme().Dark {
		blue, pearl = native.Hex("#425e7f").Alpha(0.45), native.Hex("#66536d").Alpha(0.36)
	}
	// 静态环境色场只负责让磨砂有东西可透，不运行背景动画，也不采集用户桌面。
	// 负层级必须显式指定，否则绝对背景会挡住点击与内容。
	native.Box(context.Key("ambient-blue")).Absolute().Left(0).Top(0).Right(0).Bottom(0).ZIndex(-1).
		RadialGradient(native.RadialGradient{Center: native.Point{X: 0.12, Y: 0.15}, Radius: max(width, height) * 0.72,
			Stops: []native.GradientStop{{Color: blue, At: 0}, {Color: native.Transparent, At: 1}}})
	native.Box(context.Key("ambient-pearl")).Absolute().Left(0).Top(0).Right(0).Bottom(0).ZIndex(-1).
		RadialGradient(native.RadialGradient{Center: native.Point{X: 0.86, Y: 0.7}, Radius: max(width, height) * 0.65,
			Stops: []native.GradientStop{{Color: pearl, At: 0}, {Color: native.Transparent, At: 1}}})
}

func mistLink(theme *native.Theme) native.Color {
	return theme.Accent
}

// 顶栏和底栏沿用工作面的磨砂，不叠第二次模糊；辅助功能回退保持实底。
func chromeSurface(context *native.Context, element native.Element) native.Element {
	theme := context.Theme()
	element.BorderColor(theme.Border.Alpha(0.3)).TextColor(theme.Text)
	if context.Preferences().HighContrast {
		return element.Background(contentColor(theme))
	}
	return element.Background(native.Transparent)
}

func sidebarSurface(context *native.Context, element native.Element) native.Element {
	return frostedSurface(context, element.Radius(22), true)
}

// 对话框单独采样页面，正文分组不再重复执行大面积模糊。
func floatingSurface(context *native.Context, element native.Element) native.Element {
	theme := context.Theme()
	element.Radius(14).Border(1, theme.Border.Alpha(0.7)).TextColor(theme.Text).
		Shadow(0, 14, 40, -9, native.Hex("#101725").Alpha(0.24)).
		InsetShadow(0, 1, 0, 0, native.Hex("#ffffff").Alpha(0.35))
	if context.Preferences().HighContrast {
		return element.Background(panelColor(theme))
	}
	tint := native.Hex("#ffffff").Alpha(0.78)
	if theme.Dark {
		tint = native.Hex("#202832").Alpha(0.86)
	}
	return element.Material(frostMaterial{tint: tint})
}

func inputColor(theme *native.Theme) native.Color {
	if theme.Dark {
		return theme.Background
	}
	return theme.Surface
}

func panel(context *native.Context) native.Element {
	return surfacePanel(context, nil)
}

// 只给需要明确边界的表格和详情加细边，普通分组交给留白和分隔线。
func sectionSurface(context *native.Context, key any) native.Element {
	panelContext := context
	if key != nil {
		panelContext = context.Key(key)
	}
	return native.Column(panelContext).FillWidth().Padding(0, 16).Gap(0).
		TextColor(context.Theme().Text)
}

func motionTransition(context *native.Context) native.ElementTransition {
	if context.Preferences().ReduceMotion {
		return native.ElementTransition{Duration: -1, Colors: true}
	}
	// 弹层只从自身轻微聚焦，不让整页内容上飘；关闭沿同一路径收回。
	return native.ElementTransition{
		Duration:     200 * time.Millisecond,
		ExitDuration: 150 * time.Millisecond,
		Colors:       true,
		Enter:        &native.Motion{Opacity: 0, Scale: 0.985},
		Exit:         &native.Motion{Opacity: 0, Scale: 0.99},
	}
}

func contentInset(width float32) float32 {
	if width < 700 {
		return 14
	}
	return 20
}

func noticePanel(context *native.Context, key string) native.Element {
	width, _ := context.Size()
	inset := contentInset(width - sidebarWidth(width))
	// 反馈条沿用工作面，不在正文里再插一张不透明卡片。
	return chromeSurface(context, native.Column(context.Key(key)).Margin(0, inset, 6, inset).
		Padding(4, 12).Gap(3).BorderWidth(0, 0, 1, 0)).
		Transition(motionTransition(context))
}

func divider(context *native.Context) {
	native.Row(context).FillWidth().Height(1).Background(context.Theme().Border.Alpha(0.8))
}

func iconTile(context *native.Context, icon *native.SVG, size float32) {
	native.Row(context).Width(size).Height(size).Justify(native.Center).Radius(6).
		Background(context.Theme().SurfaceHover).Children(func() {
		native.Icon(context, icon).Width(size * 0.5).Height(size * 0.5).TextColor(context.Theme().Text)
	})
}

func colorTransition(context *native.Context) native.ElementTransition {
	if context.Preferences().ReduceMotion {
		return native.ElementTransition{Colors: true, Duration: -1}
	}
	return native.ElementTransition{Colors: true, Shadows: true, Duration: 150 * time.Millisecond}
}

func selectionColor(theme *native.Theme) native.Color {
	if theme.Dark {
		return native.Hex("#718da8").Alpha(0.25)
	}
	return native.Hex("#ffffff").Alpha(0.8)
}

func emptyState(context *native.Context, title, detail string) {
	// 空状态贴着内容起点，不用居中大留白；说明直接告诉用户下一步。
	native.Column(context).Grow(1).MinHeight(68).FillWidth().Justify(native.Start).Gap(4).Padding(12, 0).Children(func() {
		native.Text(context, title).FontSize(controlTextSize(context)).FontWeight(500).TextColor(context.Theme().Text)
		native.Text(context, detail).FontSize(detailSize(context)).TextColor(context.Theme().TextMuted).LineHeight(1.35)
	})
}

// 托盘在无 Explorer 的测试桌面不可用，不代表 PATH 失败；技术原文保留在详情。
func errorSummary(detail string) string {
	if strings.Contains(detail, "托盘暂不可用") || strings.Contains(detail, "托盘不可用") || strings.Contains(strings.ToLower(detail), "notification-area") {
		return "系统托盘暂不可用 · 后台任务继续运行，可重新打开 ToolNest"
	}
	for _, separator := range []string{"\n", ": ", "："} {
		if prefix, _, found := strings.Cut(detail, separator); found && prefix != "" {
			return prefix + " · 请查看详情"
		}
	}
	if len([]rune(detail)) > 72 {
		return "操作未完成 · 请查看详情后重试"
	}
	return detail
}

var nestIcon = func() *native.SVG {
	icon, _ := native.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M4 8.5 12 4l8 4.5v7L12 20l-8-4.5z"/><path d="M8 10.5 12 8l4 2.5v3L12 16l-4-2.5z"/></svg>`))
	return icon
}()

var userScopeIcon = func() *native.SVG {
	icon, _ := native.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="8" r="3.5"/><path d="M5 20c0-4 2.8-6 7-6s7 2 7 6"/></svg>`))
	return icon
}()

var systemScopeIcon = func() *native.SVG {
	icon, _ := native.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="13" rx="2"/><path d="M8 20h8m-4-3v3"/></svg>`))
	return icon
}()

var settingsIcon = func() *native.SVG {
	icon, _ := native.ParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7h9m4 0h3M4 17h3m4 0h9"/><circle cx="15" cy="7" r="2"/><circle cx="9" cy="17" r="2"/></svg>`))
	return icon
}()

var networkIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><ellipse cx="12" cy="12" rx="4" ry="9"/><path d="M3 12h18M5 6h14M5 18h14"/></svg>`))
var archiveIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="5" rx="1.5"/><path d="M5 9v10a1 1 0 0 0 1 1h12a1 1 0 0 0 1-1V9M10 13h4"/></svg>`))
var updateIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="M12 16V4m-4 4 4-4 4 4M4 15v4a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-4"/></svg>`))

var eyeIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M2.5 12S6 5.5 12 5.5 21.5 12 21.5 12 18 18.5 12 18.5 2.5 12 2.5 12Z"/><circle cx="12" cy="12" r="2.6"/></svg>`))
var textSizeIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M4 7V5h9v2M8.5 5v14m-2 0h4M15 11v-1h6v1m-3-1v9m-2 0h4"/></svg>`))
var shieldIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3 5 6v5c0 4.6 2.8 8 7 10 4.2-2 7-5.4 7-10V6l-7-3Z"/><path d="m9 12 2 2 4-4"/></svg>`))
var folderIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z"/></svg>`))
var copyIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="8" y="8" width="12" height="12" rx="2"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/></svg>`))
var chevronRightIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m9 6 6 6-6 6"/></svg>`))
var refreshIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11a8 8 0 1 0-2.3 6.3M20 5v6h-6"/></svg>`))
var backupIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4v10m-4-4 4 4 4-4M4 17v2a1 1 0 0 0 1 1h14a1 1 0 0 0 1-1v-2"/></svg>`))
var recordsIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M6 3h9l4 4v14H6a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2Z"/><path d="M14 3v5h5M8 13h7M8 17h5"/></svg>`))
var sparkleIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.6" stroke-linecap="round" stroke-linejoin="round"><path d="m12 3 1.7 5.3L19 10l-5.3 1.7L12 17l-1.7-5.3L5 10l5.3-1.7L12 3ZM19 16l.7 2.3L22 19l-2.3.7L19 22l-.7-2.3L16 19l2.3-.7L19 16Z"/></svg>`))
var checkIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round"><path d="m5 12 4.5 4.5L19 7"/></svg>`))

var infoIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="12" r="9"/><path d="M12 11v5m0-8h.01"/></svg>`))
var keyboardIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="2.5" y="6" width="19" height="12" rx="2"/><path d="M6 10h.01M10 10h.01M14 10h.01M18 10h.01M7 14h10"/></svg>`))

var powerIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3v9M7 5.5a8 8 0 1 0 10 0"/></svg>`))
var windowCloseIcon = native.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="4" width="18" height="16" rx="3"/><path d="M3 9h18M16 13l3 3m0-3-3 3"/></svg>`))
