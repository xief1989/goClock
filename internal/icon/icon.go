// Package icon 用代码绘制 GoClock 的时钟图标，并可编码为 Windows 的 ICO 文件。
// 图标完全由程序生成，因此仓库中不需要携带二进制资源。
package icon

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"math"
)

// ssaa 是绘制时的超采样倍数，用于获得平滑的圆形边缘。
const ssaa = 4

// Sizes 是 ICO 文件中包含的图标尺寸，Windows 会按需挑选最合适的一张。
var Sizes = []int{16, 20, 24, 32, 40, 48, 64, 128, 256}

var (
	// brandBlue 是表盘外圈与指针的主色。
	brandBlue = color.NRGBA{R: 0x25, G: 0x63, B: 0xEB, A: 0xFF}
	// faceWhite 是表盘内部的底色。
	faceWhite = color.NRGBA{R: 0xFF, G: 0xFF, B: 0xFF, A: 0xFF}
)

// Render 绘制指定边长的时钟图标，背景透明。
func Render(size int) *image.NRGBA {
	s := size * ssaa
	hi := image.NewNRGBA(image.Rect(0, 0, s, s))

	cx := float64(s) / 2
	cy := float64(s) / 2
	r := float64(s) * 0.47

	// 外圈实心蓝色圆，再挖出白色表盘，形成一圈蓝色边框。
	fillCircle(hi, cx, cy, r, brandBlue)
	fillCircle(hi, cx, cy, r*0.80, faceWhite)

	// 指针停在 10:10，这是钟表最经典的展示角度。
	drawHand(hi, cx, cy, 305, r*0.40, r*0.095, brandBlue)
	drawHand(hi, cx, cy, 60, r*0.58, r*0.070, brandBlue)
	fillCircle(hi, cx, cy, r*0.085, brandBlue)

	return downsample(hi, size)
}

// fillCircle 在图像上填充一个实心圆。
func fillCircle(img *image.NRGBA, cx, cy, r float64, c color.NRGBA) {
	minX := int(math.Floor(cx - r))
	maxX := int(math.Ceil(cx + r))
	minY := int(math.Floor(cy - r))
	maxY := int(math.Ceil(cy + r))
	rr := r * r
	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			dx := float64(x) + 0.5 - cx
			dy := float64(y) + 0.5 - cy
			if dx*dx+dy*dy <= rr {
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

// drawHand 从圆心朝 deg 方向画一根圆头指针，deg 以 12 点方向为 0 度顺时针增长。
func drawHand(img *image.NRGBA, cx, cy, deg, length, width float64, c color.NRGBA) {
	rad := deg * math.Pi / 180
	ex := cx + length*math.Sin(rad)
	ey := cy - length*math.Cos(rad)
	half := width / 2

	minX := int(math.Floor(math.Min(cx, ex) - half - 1))
	maxX := int(math.Ceil(math.Max(cx, ex) + half + 1))
	minY := int(math.Floor(math.Min(cy, ey) - half - 1))
	maxY := int(math.Ceil(math.Max(cy, ey) + half + 1))

	for y := minY; y <= maxY; y++ {
		for x := minX; x <= maxX; x++ {
			px := float64(x) + 0.5
			py := float64(y) + 0.5
			if distanceToSegment(px, py, cx, cy, ex, ey) <= half {
				img.SetNRGBA(x, y, c)
			}
		}
	}
}

// distanceToSegment 返回点 (px,py) 到线段 (x1,y1)-(x2,y2) 的距离。
func distanceToSegment(px, py, x1, y1, x2, y2 float64) float64 {
	dx := x2 - x1
	dy := y2 - y1
	lenSq := dx*dx + dy*dy
	if lenSq == 0 {
		return math.Hypot(px-x1, py-y1)
	}
	t := ((px-x1)*dx + (py-y1)*dy) / lenSq
	t = math.Max(0, math.Min(1, t))
	return math.Hypot(px-(x1+t*dx), py-(y1+t*dy))
}

// downsample 把超采样画布按 ssaa 倍缩小，按 alpha 加权平均以保留边缘过渡。
func downsample(src *image.NRGBA, size int) *image.NRGBA {
	dst := image.NewNRGBA(image.Rect(0, 0, size, size))
	total := ssaa * ssaa
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			var sumR, sumG, sumB, sumA int
			for dy := 0; dy < ssaa; dy++ {
				for dx := 0; dx < ssaa; dx++ {
					c := src.NRGBAAt(x*ssaa+dx, y*ssaa+dy)
					a := int(c.A)
					sumR += int(c.R) * a
					sumG += int(c.G) * a
					sumB += int(c.B) * a
					sumA += a
				}
			}
			if sumA == 0 {
				continue
			}
			dst.SetNRGBA(x, y, color.NRGBA{
				R: uint8(sumR / sumA),
				G: uint8(sumG / sumA),
				B: uint8(sumB / sumA),
				A: uint8(sumA / total),
			})
		}
	}
	return dst
}

// EncodeICO 生成包含 Sizes 中全部尺寸的 ICO 文件内容。
func EncodeICO() []byte {
	images := make([]*image.NRGBA, 0, len(Sizes))
	for _, s := range Sizes {
		images = append(images, Render(s))
	}

	var dir bytes.Buffer
	var body bytes.Buffer

	binary.Write(&dir, binary.LittleEndian, uint16(0)) // 保留字段
	binary.Write(&dir, binary.LittleEndian, uint16(1)) // 资源类型：1 表示图标
	binary.Write(&dir, binary.LittleEndian, uint16(len(images)))

	// 图像数据紧跟在目录之后，每个目录项固定 16 字节。
	offset := 6 + 16*len(images)
	for _, img := range images {
		data := encodeDIB(img)
		side := img.Bounds().Dx()
		// ICO 规定 256 用 0 表示。
		sideByte := byte(side)
		if side >= 256 {
			sideByte = 0
		}
		dir.WriteByte(sideByte)                                    // 宽
		dir.WriteByte(sideByte)                                    // 高
		dir.WriteByte(0)                                           // 调色板颜色数，32 位色填 0
		dir.WriteByte(0)                                           // 保留字段
		binary.Write(&dir, binary.LittleEndian, uint16(1))         // 颜色平面数
		binary.Write(&dir, binary.LittleEndian, uint16(32))        // 位深
		binary.Write(&dir, binary.LittleEndian, uint32(len(data))) // 数据长度
		binary.Write(&dir, binary.LittleEndian, uint32(offset))    // 数据偏移
		offset += len(data)
		body.Write(data)
	}

	return append(dir.Bytes(), body.Bytes()...)
}

// encodeDIB 把图像编码为 ICO 内部使用的 32 位 DIB（自底向上，BGRA）。
func encodeDIB(img *image.NRGBA) []byte {
	w := img.Bounds().Dx()
	h := img.Bounds().Dy()
	// AND 掩码每行按 4 字节对齐，虽然 32 位色不再依赖它，但结构上必须存在。
	maskRow := ((w + 31) / 32) * 4

	var buf bytes.Buffer
	binary.Write(&buf, binary.LittleEndian, uint32(40)) // BITMAPINFOHEADER 大小
	binary.Write(&buf, binary.LittleEndian, int32(w))
	binary.Write(&buf, binary.LittleEndian, int32(h*2)) // 高度需含掩码部分
	binary.Write(&buf, binary.LittleEndian, uint16(1))
	binary.Write(&buf, binary.LittleEndian, uint16(32))
	binary.Write(&buf, binary.LittleEndian, uint32(0)) // BI_RGB，不压缩
	binary.Write(&buf, binary.LittleEndian, uint32(w*h*4+maskRow*h))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, int32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))
	binary.Write(&buf, binary.LittleEndian, uint32(0))

	for y := h - 1; y >= 0; y-- {
		for x := 0; x < w; x++ {
			c := img.NRGBAAt(x, y)
			buf.Write([]byte{c.B, c.G, c.R, c.A})
		}
	}
	buf.Write(make([]byte, maskRow*h))

	return buf.Bytes()
}
