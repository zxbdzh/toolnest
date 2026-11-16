package assets

import (
	"bytes"
	"compress/zlib"
	"embed"
	"io"
	"sync"
)

// 字体字节随程序编译；使用 zlib 仅压缩仓库体积，运行时解压后交给 MyGo 注册。
// 不修改字体、不写临时字体文件，也不把 MiSans 作为独立文件随安装包分发。
//
//go:embed MiSans-Regular.ttf.zlib MiSans-Medium.ttf.zlib MiSans-Semibold.ttf.zlib
var fontFiles embed.FS

var (
	fontOnce sync.Once
	fontData map[string][]byte
	fontErr  error
)

// MiSans 返回三个字重的原始 TTF 字节，键为注册时使用的字体族名称。
func MiSans() (map[string][]byte, error) {
	fontOnce.Do(func() {
		fontData = make(map[string][]byte, 3)
		for _, name := range []string{"MiSans-Regular.ttf.zlib", "MiSans-Medium.ttf.zlib", "MiSans-Semibold.ttf.zlib"} {
			compressed, err := fontFiles.ReadFile(name)
			if err != nil {
				fontErr = err
				return
			}
			reader, err := zlib.NewReader(bytes.NewReader(compressed))
			if err != nil {
				fontErr = err
				return
			}
			data, err := io.ReadAll(reader)
			closeErr := reader.Close()
			if err != nil {
				fontErr = err
				return
			}
			if closeErr != nil {
				fontErr = closeErr
				return
			}
			fontData[name] = data
		}
	})
	return fontData, fontErr
}
