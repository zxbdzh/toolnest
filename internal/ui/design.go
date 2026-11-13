package ui

import (
	"strings"

	native "github.com/egoist/mygo/ui"
)

// 浮光采用雾蓝银白；正文用实底和高对比文字，材质只留给导航壳层。
func refinedTheme(source *native.Theme) native.Theme {
	theme := *source.WithAccent(native.Hex("#416b8c"))
	theme.Font, theme.FontSize, theme.Spacing, theme.Radius = "Microsoft YaHei UI", 14, 4, 8
	if theme.Dark {
		theme.Background, theme.Surface = native.Hex("#202b35"), native.Hex("#25313d")
		theme.SurfaceHover, theme.SurfacePressed = native.Hex("#354e63"), native.Hex("#3c566c")
		theme.Text, theme.TextMuted, theme.Border = native.Hex("#e2eaf0"), native.Hex("#b0c0cc"), native.Hex("#3c4d5c")
		theme.Accent, theme.AccentHover, theme.AccentPressed = native.Hex("#416b8c"), native.Hex("#345773"), native.Hex("#2c4962")
		theme.AccentText = native.Hex("#ffffff")
	} else {
		theme.Background, theme.Surface = native.Hex("#f3f6f9"), native.Hex("#edf2f6")
		theme.SurfaceHover, theme.SurfacePressed = native.Hex("#e4edf4"), native.Hex("#d6e3ed")
		theme.Text, theme.TextMuted, theme.Border = native.Hex("#253441"), native.Hex("#5c6c78"), native.Hex("#dfe6ed")
		theme.Accent, theme.AccentHover, theme.AccentPressed = native.Hex("#416b8c"), native.Hex("#345773"), native.Hex("#2c4962")
		theme.AccentText = native.Hex("#ffffff")
	}
	theme.Focus = mistLink(&theme)
	theme.Selection = theme.SurfaceHover
	return theme
}

func panelColor(theme *native.Theme) native.Color {
	if theme.Dark {
		return native.Hex("#293641")
	}
	return native.Hex("#ffffff")
}

func mistLink(theme *native.Theme) native.Color {
	if theme.Dark {
		return native.Hex("#abcbe4")
	}
	return native.Hex("#416b8c")
}

func mistSurface(theme *native.Theme, light, dark string) native.Color {
	if theme.Dark {
		return native.Hex(dark)
	}
	return native.Hex(light)
}

func inputColor(theme *native.Theme) native.Color {
	if theme.Dark {
		return theme.Background
	}
	return theme.Surface
}

func panel(context *native.Context) native.Element {
	theme := context.Theme()
	return native.Column(context).FillWidth().Padding(22).Gap(12).Radius(16).
		Background(panelColor(theme)).Border(1, theme.Border.Alpha(0.65))
}

func divider(context *native.Context) {
	native.Row(context).FillWidth().Height(1).Background(context.Theme().Border.Alpha(0.65))
}

func iconTile(context *native.Context, icon *native.SVG, size float32) {
	native.Row(context).Width(size).Height(size).Justify(native.Center).Radius(size * 0.27).
		Background(mistSurface(context.Theme(), "#e8f0f6", "#344b5d")).Children(func() {
		native.Icon(context, icon).Width(size * 0.5).Height(size * 0.5).TextColor(mistLink(context.Theme()))
	})
}

func emptyState(context *native.Context, title, detail string) {
	native.Column(context).Grow(1).MinHeight(110).FillWidth().Justify(native.Center).AlignItems(native.Center).Gap(12).Padding(24).Children(func() {
		iconTile(context, nestIcon, 44)
		native.Text(context, title).FontSize(16).FontWeight(600).TextColor(context.Theme().Text)
		native.Text(context, detail).FontSize(13).TextColor(context.Theme().TextMuted)
	})
}

// 托盘在无 Explorer 的测试桌面不可用，不代表 PATH 失败；技术原文保留在详情。
func errorSummary(detail string) string {
	if strings.Contains(detail, "托盘暂不可用") || strings.Contains(detail, "托盘不可用") || strings.Contains(strings.ToLower(detail), "notification-area") {
		return "系统托盘暂不可用 · 窗口仍可正常使用，关闭时会再次确认"
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
