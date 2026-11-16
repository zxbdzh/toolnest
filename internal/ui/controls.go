package ui

import (
	"time"

	native "github.com/egoist/mygo/ui"
)

const (
	controlHeight       = float32(32)
	controlRadius       = float32(10)
	panelRadius         = float32(12)
	listRowHeight       = float32(48)
	tableRowHeight      = float32(38)
	baseControlTextSize = float32(14)
)

type buttonKind int

const (
	buttonSecondary buttonKind = iota
	buttonPrimary
	buttonQuiet
	buttonDanger
)

type controlOptions struct {
	key       any
	label     string
	icon      *native.SVG
	textColor native.Color
	fillWidth bool
	disabled  bool
	height    float32
}

// 三档字号只调整相对字阶，不改控件高度；标准档比上一版 17 DIP 收小一档。
// 每个 Text 都显式按当前主题取字号，因此设置保存后同一帧即可完整生效。
func textSize(context *native.Context) float32 {
	return max(13, context.Theme().FontSize)
}

func detailSize(context *native.Context) float32 { return max(12, context.Theme().FontSize-2) }

func sectionSize(context *native.Context) float32 { return max(14, context.Theme().FontSize+3) }

func pageTitleSize(context *native.Context) float32 { return max(16, context.Theme().FontSize+3) }

func numericSize(context *native.Context) float32 { return max(22, context.Theme().FontSize+11) }

func controlTextSize(context *native.Context) float32 { return textSize(context) }

// controls 用 ButtonBase 接管外观，同时保留 MyGo 的点击、键盘和可访问语义。
// 所有状态色来自同一组语义色，页面不再逐处调高度、圆角、边框和 hover。
func controlButton(context *native.Context, kind buttonKind, text string, options controlOptions) native.Element {
	theme := context.Theme()
	size := controlTextSize(context)
	if options.key != nil {
		context.Key(options.key)
	}
	accessible := options.label
	if accessible == "" {
		accessible = text
	}
	height := options.height
	if height <= 0 {
		height = controlHeight
	}
	button := native.ButtonBase(context).Label(accessible).Height(height).Padding(0, 13).Gap(7).Radius(height / 2).
		AlignItems(native.Center).Justify(native.Center).FontSize(size).FontWeight(500).
		Disabled(options.disabled).DimDisabled(false).FocusRing(false)
	if options.fillWidth {
		button.FillWidth()
	}

	base, foreground, border := panelColor(theme).Alpha(0.64), theme.Text, native.Hex("#ffffff").Alpha(0.7)
	hover, pressed := panelColor(theme).Alpha(0.92), theme.SurfacePressed.Alpha(0.75)
	if theme.Dark {
		base, border = native.Hex("#ffffff").Alpha(0.08), native.Hex("#ffffff").Alpha(0.1)
		hover, pressed = native.Hex("#ffffff").Alpha(0.13), native.Hex("#ffffff").Alpha(0.18)
	}
	if options.textColor != (native.Color{}) {
		foreground = options.textColor
	}
	switch kind {
	case buttonPrimary:
		base, foreground, border = theme.Accent, theme.AccentText, native.Hex("#ffffff").Alpha(0.12)
		hover, pressed = theme.AccentHover, theme.AccentPressed
	case buttonQuiet:
		base, border = native.Transparent, native.Transparent
		hover, pressed = theme.SurfaceHover, theme.SurfacePressed
		if options.textColor == (native.Color{}) {
			foreground = theme.Text
		}
	case buttonDanger:
		base, foreground, border = native.Transparent, theme.Danger, theme.Danger.Alpha(0.45)
		hover, pressed = theme.Danger.Alpha(0.08), theme.Danger.Alpha(0.14)
	}
	if theme.Dark && options.disabled && kind == buttonSecondary {
		base, border = theme.Surface.Alpha(0.6), theme.Border.Alpha(0.7)
	}
	if options.textColor != (native.Color{}) {
		foreground = options.textColor
	}
	switch {
	case button.Pressed() && !options.disabled:
		base = pressed
	case button.Hovered() && !options.disabled:
		base = hover
	}
	button.Background(base).Border(1, border).TextColor(foreground)
	if kind == buttonPrimary && !options.disabled {
		bottom := base.Mix(native.Hex("#17263a"), 0.12)
		button.Gradient(base, bottom, 180)
	}
	if !options.disabled && kind != buttonQuiet && kind != buttonDanger {
		// 柔和环境透色代替原生控件的硬边；主要操作仍用实色保证文字对比。
		highlight := native.Hex("#ffffff").Alpha(0.35)
		if kind == buttonPrimary || theme.Dark {
			highlight = native.Hex("#ffffff").Alpha(0.16)
		}
		button.InsetShadow(0, 1, 0, 0, highlight).
			Shadow(0, 3, 7, -2, native.Hex("#162033").Alpha(0.12))
		if button.Hovered() {
			button.NoShadow().Shadow(0, 5, 12, -3, native.Hex("#162033").Alpha(0.17))
		}
		if button.Pressed() {
			button.NoShadow().
				InsetShadow(0, 1, 2, 0, native.Hex("#162033").Alpha(0.15))
		}
		pressScale := float32(1)
		if button.Pressed() {
			pressScale = 0.965
		}
		// 使用可中断的无回弹弹簧；细微按压不参与布局计算，不推动周围控件。
		button.AnimatePose(native.Pose{Scale: pressScale}, native.Animation{Spring: native.SpringOptions{Duration: 180 * time.Millisecond}})
	}
	transition := colorTransition(context)
	if button.Pressed() {
		transition.Duration = -1
	}
	button.Transition(transition)
	if options.disabled {
		button.Background(native.Transparent).BorderColor(theme.Border.Alpha(0.45)).TextColor(theme.TextMuted)
		foreground = theme.TextMuted
	}
	if button.FocusVisible() {
		// 键盘焦点用 2px 内侧下划线，不与 hover / press 背景争视觉层级。
		button.Draw(func(painter *native.Painter, bounds native.Rect) {
			painter.Fill(native.Rect{X: bounds.X + 10, Y: bounds.Y + bounds.H - 3, W: bounds.W - 20, H: 2}, theme.Accent, 1)
		})
	}
	button.Children(func() {
		if options.icon != nil {
			native.Icon(context, options.icon).Width(16).Height(16).TextColor(foreground)
		}
		// 可见文字保持简洁，辅助名称由 ButtonBase.Label 提供；两者不互相覆盖。
		native.Text(context, text).FontSize(size).TextColor(foreground).SingleLine()
	})
	return button
}

// iconButton 把行内“查看”从实心矩形收为方形幽灵按钮；Label 仍是完整可访问名称。
// hover 仅淡入底色，不在选中行里再叠一块不透明白卡。
func iconButton(context *native.Context, icon *native.SVG, options controlOptions) native.Element {
	theme := context.Theme()
	if options.key != nil {
		context.Key(options.key)
	}
	height := options.height
	if height <= 0 {
		height = 32
	}
	button := native.ButtonBase(context).Label(options.label).Width(height).Height(height).Radius(controlRadius).
		AlignItems(native.Center).Justify(native.Center).Disabled(options.disabled).DimDisabled(false).FocusRing(false).
		Transition(colorTransition(context))
	base := native.Transparent
	foreground := theme.TextMuted
	if options.textColor != (native.Color{}) {
		foreground = options.textColor
	}
	if button.Pressed() && !options.disabled {
		base = theme.SurfacePressed.Alpha(0.75)
	} else if button.Hovered() && !options.disabled {
		base = theme.SurfaceHover.Alpha(0.82)
	}
	button.Background(base)
	if options.disabled {
		foreground = theme.TextMuted.Alpha(0.75)
	}
	if button.FocusVisible() {
		button.Draw(func(painter *native.Painter, bounds native.Rect) {
			painter.Stroke(bounds, theme.Accent, controlRadius, 1)
		})
	}
	button.Children(func() {
		native.Icon(context, icon).Width(16).Height(16).TextColor(foreground)
	})
	return button
}

func secondaryButton(context *native.Context, text string, options controlOptions) native.Element {
	return controlButton(context, buttonSecondary, text, options)
}

func primaryButton(context *native.Context, text string, options controlOptions) native.Element {
	return controlButton(context, buttonPrimary, text, options)
}

func dangerButton(context *native.Context, text string, options controlOptions) native.Element {
	return controlButton(context, buttonDanger, text, options)
}

func quietButton(context *native.Context, text string, options controlOptions) native.Element {
	return controlButton(context, buttonQuiet, text, options)
}

func subtleButton(context *native.Context, text string, options controlOptions) native.Element {
	if options.textColor == (native.Color{}) {
		options.textColor = context.Theme().TextMuted
	}
	return controlButton(context, buttonQuiet, text, options)
}

// compactAction 是列表/表格行的统一行内入口：无边框、同高、同图标尺寸。
// 与页内主按钮分开，避免每个列表行再出现一块白色矩形。
func compactAction(context *native.Context, text string, options controlOptions) native.Element {
	if options.icon == nil {
		options.icon = chevronRightIcon
	}
	options.height = 28
	options.textColor = context.Theme().TextMuted
	return controlButton(context, buttonQuiet, text, options)
}

// textField 统一普通输入框与搜索框的边界、底色和字阶；焦点仍由 MyGo 绘制。
func textField(context *native.Context, field native.Element) native.Element {
	theme := context.Theme()
	background, border := native.Hex("#ffffff").Alpha(0.52), native.Hex("#ffffff").Alpha(0.68)
	if theme.Dark {
		background, border = native.Hex("#121b25").Alpha(0.4), native.Hex("#ffffff").Alpha(0.1)
	}
	if context.Preferences().HighContrast {
		background, border = contentColor(theme), theme.Border
	}
	return field.Height(controlHeight).Padding(0, 11).Radius(controlRadius).FontSize(controlTextSize(context)).
		Border(1, border).Background(background).TextColor(theme.Text).
		Transition(colorTransition(context))
}

func searchField(context *native.Context, key any, value *string, placeholder, label string) native.Element {
	field := native.SearchField(context.Key(key), value).Placeholder(placeholder).Label(label)
	return textField(context, field).MinWidth(0)
}

func textInput(context *native.Context, key any, value *string, placeholder, label string) native.Element {
	field := native.TextInput(context.Key(key), value).Placeholder(placeholder).Label(label)
	return textField(context, field).MinWidth(150)
}

func numberInput(context *native.Context, key any, value *float64, minimum, maximum float64, label string) native.Element {
	field := native.NumberInput(context.Key(key), value, minimum, maximum, 1).Label(label)
	return textField(context, field).Width(104)
}

// 紧凑分段使用一层半透明轨道；滑块平移，不让整组内容随着点击缩放。
func segmentedControl(context *native.Context, key any, selected *int, labels ...string) bool {
	theme := context.Theme()
	size := controlTextSize(context)
	const inset, border, radius = 2, 1, 14
	const contentInset = inset + border
	segmentWidth := float32(68)
	for _, label := range labels {
		width, _ := context.MeasureText(0, native.Span{Text: label, Size: size, Weight: 500})
		segmentWidth = max(segmentWidth, width+24)
	}
	before := *selected
	parts := native.SegmentedBase(context.Key(key), selected, len(labels))
	position := parts.Track.AnimateTo("selection-position", float32(*selected), native.Animation{Spring: native.SpringOptions{Duration: 200 * time.Millisecond}})
	trackColor := theme.Accent.Alpha(0.07)
	if theme.Dark {
		trackColor = native.Hex("#000000").Alpha(0.16)
	}
	// 轨道、按钮和滑块共用完整段宽；边框也计入起点，避免每切一段累积几像素偏差。
	// 显式收口轨道宽度，不能由 Column 拉伸成横贯详情页的空白条。
	parts.Track.Width(segmentWidth*float32(len(labels))+2*contentInset).Height(controlHeight).
		AlignSelf(native.Start).Shrink(0).Gap(0).Padding(inset).Radius(controlHeight/2).
		Background(trackColor).Border(border, theme.Border.Alpha(0.3)).FocusRing(false).Label(labels[*selected])
	parts.Track.Draw(func(painter *native.Painter, bounds native.Rect) {
		pill := native.Rect{X: bounds.X + contentInset + position*segmentWidth, Y: bounds.Y + contentInset, W: segmentWidth, H: bounds.H - 2*contentInset}
		painter.Fill(native.Rect{X: pill.X, Y: pill.Y + 1, W: pill.W, H: pill.H}, native.Hex("#132033").Alpha(0.08), radius)
		selectedColor := native.Hex("#ffffff").Alpha(0.88)
		if theme.Dark {
			selectedColor = native.Hex("#5b687a").Alpha(0.64)
		}
		painter.Fill(pill, selectedColor, radius)
	}).Children(func() {
		for index, label := range labels {
			segment := parts.Segment(index).Width(segmentWidth).Height(controlHeight - 2*contentInset).Shrink(0).Padding(0).
				Radius(radius).AlignItems(native.Center).Justify(native.Center).FocusRing(false)
			if segment.Pressed() && index != *selected {
				segment.Background(theme.SurfacePressed)
			}
			segment.Children(func() {
				weight := 400
				if index == *selected {
					weight = 500
				}
				native.Text(context, label).FontSize(size).FontWeight(weight).TextColor(theme.Text).SingleLine()
			})
		}
	})
	return *selected != before
}

func surfacePanel(context *native.Context, key any) native.Element {
	// 无稳定身份的局部分组不占用 Context.Key；只有页面级容器才需要显式 key。
	panelContext := context
	if key != nil {
		panelContext = context.Key(key)
	}
	// 表格等高信息密度区域用薄透色，不在已有磨砂上重复执行整面模糊。
	theme := context.Theme()
	return native.Column(panelContext).FillWidth().Padding(0, 16).Gap(4).
		Border(1, theme.Border.Alpha(0.45)).Radius(panelRadius).Background(panelColor(theme).Alpha(0.4)).TextColor(theme.Text)
}

func sectionPanel(context *native.Context, key any) native.Element {
	// 普通分组沿用工作面的材质，不嵌套带阴影的独立玻璃卡。
	return sectionSurface(context, key)
}

// compactPanel 让内容型分组随实际行高收口，避免少量列表被拉成整屏白盒；
// 父容器 Grow(1) 时，横向宽度仍由 FillWidth 保持，纵向只给一个很大的上限。
func compactPanel(context *native.Context, key any) native.Element {
	return sectionSurface(context, key).MaxHeight(10000)
}

// listRows 返回带 1px 行间分隔线的连续列表；外层容器与行分别占满宽度。
// 这样调用方既能把它放进 Grow(1) 的两列布局，也不会把标题挤成竖排。
func listRows(context *native.Context, key any) native.Element {
	return native.Column(context.Key(key)).FillWidth().Dividers(1, context.Theme().Border.Alpha(0.65))
}

func dataTable(context *native.Context, key any, state *native.ListState, columns []native.TableColumn, rows int, cell func(int, int)) native.Element {
	theme := context.Theme()
	return native.Table(context.Key(key), state, columns, rows, cell).FillWidth().Grow(1).MinHeight(0).
		Background(panelColor(theme).Alpha(0.45)).Border(1, theme.Border.Alpha(0.5)).Radius(controlRadius).
		Dividers(1, theme.Border.Alpha(0.45))
}

// adaptiveTable 让少量行按内容高度收口，避免三行快照下面留出整屏空白。
// 高度包含表头、单元格上下内边距和 1px 分隔线；超过阈值时仍交给表格自身滚动。
func adaptiveTable(context *native.Context, key any, state *native.ListState, columns []native.TableColumn, rows, maxRows int, rowHeight float32, cell func(int, int)) native.Element {
	theme := context.Theme()
	table := native.Table(context.Key(key), state, columns, rows, cell).FillWidth().MinHeight(0).
		Background(panelColor(theme).Alpha(0.45)).Border(1, theme.Border.Alpha(0.5)).Radius(controlRadius).
		Dividers(1, theme.Border.Alpha(0.45))
	if rows <= maxRows {
		// 表头固定一行，数据行按实际内容高度；另留 4px 边界避免末行被裁。
		// 原生表格的单元格有固定上下内边距；表头与每行一起计算，避免高行被摘要区覆盖。
		table.Height(theme.Space(8) + float32(rows)*(rowHeight+theme.Space(5)) + 3)
	} else {
		table.Grow(1)
	}
	return table
}

func tableCellText(context *native.Context, text string, mono bool, muted bool) {
	element := native.Text(context, text).FontSize(controlTextSize(context)).SingleLine().Height(tableRowHeight - 10).Tooltip(text)
	if mono {
		element.Font("Cascadia Mono")
	}
	if muted {
		element.TextColor(context.Theme().TextMuted)
	}
}
