package brand

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/png"
	"math"

	"golang.org/x/image/draw"
)

// Image 与界面中的嵌套六边形保持同一识别符号；小尺寸不依赖文字或细碎装饰。
func Image(size int, failed bool) *image.NRGBA {
	scale := 4
	canvas := image.NewNRGBA(image.Rect(0, 0, size*scale, size*scale))
	accent := color.NRGBA{R: 65, G: 107, B: 140, A: 255}
	unit := float64(size*scale) / 32
	segments := [][4]float64{
		{16, 6.5, 24.2, 11.25}, {24.2, 11.25, 24.2, 20.75}, {24.2, 20.75, 16, 25.5},
		{16, 25.5, 7.8, 20.75}, {7.8, 20.75, 7.8, 11.25}, {7.8, 11.25, 16, 6.5},
		{16, 11, 20.4, 13.55}, {20.4, 13.55, 20.4, 18.65}, {20.4, 18.65, 16, 21.2},
		{16, 21.2, 11.6, 18.65}, {11.6, 18.65, 11.6, 13.55}, {11.6, 13.55, 16, 11},
		{11.6, 13.55, 16, 16.1}, {20.4, 13.55, 16, 16.1}, {16, 16.1, 16, 21.2},
	}
	for vertical := 0; vertical < canvas.Bounds().Dy(); vertical++ {
		for horizontal := 0; horizontal < canvas.Bounds().Dx(); horizontal++ {
			x, y := (float64(horizontal)+0.5)/unit, (float64(vertical)+0.5)/unit
			dx, dy := math.Max(math.Abs(x-16)-8, 0), math.Max(math.Abs(y-16)-8, 0)
			if dx*dx+dy*dy > 6*6 {
				continue
			}
			ink := accent
			for _, segment := range segments {
				lengthX, lengthY := segment[2]-segment[0], segment[3]-segment[1]
				amount := min(1, max(0, ((x-segment[0])*lengthX+(y-segment[1])*lengthY)/(lengthX*lengthX+lengthY*lengthY)))
				distanceX, distanceY := x-segment[0]-amount*lengthX, y-segment[1]-amount*lengthY
				if distanceX*distanceX+distanceY*distanceY < 0.72*0.72 {
					ink = color.NRGBA{R: 247, G: 251, B: 255, A: 255}
				}
			}
			if failed && math.Hypot(x-25.5, y-25.5) <= 6 {
				ink = color.NRGBA{R: 186, G: 62, B: 66, A: 255}
				if math.Abs(x-25.5) < 0.8 && (y > 21.5 && y < 26 || y > 27.5 && y < 29) {
					ink = color.NRGBA{R: 255, G: 255, B: 255, A: 255}
				}
			}
			canvas.SetNRGBA(horizontal, vertical, ink)
		}
	}
	result := image.NewNRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(result, result.Bounds(), canvas, canvas.Bounds(), draw.Src, nil)
	return result
}

func PNG(size int, failed bool) []byte {
	var output bytes.Buffer
	if png.Encode(&output, Image(size, failed)) != nil {
		return nil
	}
	return output.Bytes()
}

// ICO 使用带透明度的 DIB，兼容安装器与系统的小图标读取；资源工具再把同一文件嵌入 PE。
func ICO() []byte {
	sizes := []int{16, 20, 24, 32, 40, 48, 64, 128, 256}
	header := make([]byte, 6+16*len(sizes))
	binary.LittleEndian.PutUint16(header[2:], 1)
	binary.LittleEndian.PutUint16(header[4:], uint16(len(sizes)))
	var images bytes.Buffer
	for index, size := range sizes {
		pixels := Image(size, false)
		maskStride := ((size + 31) / 32) * 4
		data := make([]byte, 40+size*size*4+size*maskStride)
		binary.LittleEndian.PutUint32(data, 40)
		binary.LittleEndian.PutUint32(data[4:], uint32(size))
		binary.LittleEndian.PutUint32(data[8:], uint32(size*2))
		binary.LittleEndian.PutUint16(data[12:], 1)
		binary.LittleEndian.PutUint16(data[14:], 32)
		for row := 0; row < size; row++ {
			for column := 0; column < size; column++ {
				ink := pixels.NRGBAAt(column, size-1-row)
				offset := 40 + (row*size+column)*4
				copy(data[offset:], []byte{ink.B, ink.G, ink.R, ink.A})
				if ink.A == 0 {
					data[40+size*size*4+row*maskStride+column/8] |= 1 << (7 - column%8)
				}
			}
		}
		entry := header[6+index*16:]
		entry[0], entry[1] = byte(size), byte(size)
		binary.LittleEndian.PutUint16(entry[4:], 1)
		binary.LittleEndian.PutUint16(entry[6:], 32)
		binary.LittleEndian.PutUint32(entry[8:], uint32(len(data)))
		binary.LittleEndian.PutUint32(entry[12:], uint32(len(header)+images.Len()))
		images.Write(data)
	}
	return append(header, images.Bytes()...)
}
