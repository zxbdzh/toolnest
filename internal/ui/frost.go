package ui

import (
	native "github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/shader"
)

// 应用内材质只采样本窗口已绘制的内容，不采集桌面，也不依赖系统 Acrylic。
// 不调用 glass.Blur：它会读取 Windows 的透明效果开关并主动画成实底。
var applicationFrost = shader.MustCompile(`package main

var Tint vec4
var Edge float

func Fragment(dst vec4, pos vec2) vec4 {
	back := backdropSample(pos)
	lightness := dot(back.rgb, vec3(0.2126, 0.7152, 0.0722))
	color := mix(vec3(lightness), back.rgb, 1.18)
	color = mix(color, Tint.rgb, Tint.a)
	distance := min(min(pos.x, pos.y), min(rectSize().x-pos.x, rectSize().y-pos.y))
	rim := (1.0-smoothstep(0.0, 1.2, distance))*Edge
	return vec4(mix(color, vec3(1.0), rim), 1.0)
}
`)

type frostMaterial struct {
	tint native.Color
	weak bool
}

func (material frostMaterial) PaintMaterial(painter *native.Painter, bounds native.Rect, radii [4]float32) {
	// 用户明确选择应用内磨砂，因此系统透明开关不再控制此材质；高对比仍优先可读性。
	if painter.Preferences().HighContrast {
		painter.FillRadii(bounds, radii, contentColor(painter.Theme()))
		return
	}
	tint, edge := material.tint, float32(0.12)
	if material.weak {
		tint = tint.Alpha(0.36)
		edge = 0.2
	}
	applicationFrost.Draw(painter, bounds, shader.Options{
		Uniforms:     map[string]any{"Tint": tint, "Edge": edge},
		Radius:       max(radii[0], radii[1], radii[2], radii[3]),
		BackdropBlur: 20,
	})
}
