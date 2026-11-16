package ui

import (
	"sync"

	native "github.com/egoist/mygo/ui"

	"toolnest/internal/brand/assets"
)

const misansFamily = "ToolNest MiSans"

var registerMisans sync.Once

// ensureMisans 在每个渲染进程只注册一次内嵌字体；页内切换主题或 DPI 不重复解压。
// 注册失败时保留系统字体回退，不让字体加载阻塞工具界面。
func ensureMisans() {
	registerMisans.Do(func() {
		fonts, err := assets.MiSans()
		if err != nil {
			return
		}
		for _, data := range fonts {
			_ = native.RegisterFont(data, misansFamily)
		}
	})
}
