package ui

import (
	"image"
	"math"
	"strings"

	"github.com/lxn/walk"
	"github.com/lxn/win"

	"goclock/internal/capture"
)

// shotTool 是截图工具栏上的绘图工具。
type shotTool int

const (
	toolNone shotTool = iota
	toolRect
	toolEllipse
	toolArrow
	toolPen
	toolMosaic
	toolText
)

// shotColors 是标注可选的颜色，第一个是默认色。
var shotColors = []walk.Color{
	walk.RGB(0xF5, 0x22, 0x2D), // 红
	walk.RGB(0xFA, 0xAD, 0x14), // 黄
	walk.RGB(0x52, 0xC4, 0x1A), // 绿
	walk.RGB(0x16, 0x77, 0xFF), // 蓝
	walk.RGB(0xFF, 0xFF, 0xFF), // 白
	walk.RGB(0x1F, 0x29, 0x37), // 黑
}

// 三档粗细分别对应的线宽、马赛克笔刷宽度、文字字号（96 DPI 下的像素）。
var (
	strokeWidths = [3]float64{2, 4, 6}
	mosaicWidths = [3]int{16, 28, 44}
	textSizes    = [3]int{16, 22, 30}
)

// mosaicBlock 是马赛克方块的边长（96 DPI 下的像素）。
const mosaicBlock = 10

// blurRadius 是模糊每一遍盒式模糊的半径（96 DPI 下的像素），连做三遍近似高斯模糊，正文文字完全看不清。
const blurRadius = 8

// annotation 是画在截图上的一个标注，坐标为遮罩窗口的客户区坐标（物理像素）。
type annotation struct {
	tool  shotTool
	color walk.Color
	// level 是粗细档位，0~2。
	level int
	// pts：矩形、椭圆、箭头与框选打码是起点和终点两个点；画笔与涂抹打码是整条轨迹；文字是左上角一个点。
	pts  []image.Point
	text string
	// blur 与 area 只对打码有效：blur 表示用模糊而不是马赛克，area 表示框选一块区域而不是画笔涂抹。
	blur bool
	area bool
}

// strokeWidth 返回线条类标注的线宽（物理像素）。
func (a *annotation) strokeWidth(scale float64) float32 {
	return float32(strokeWidths[a.level] * scale)
}

// span 返回由起点、终点围成的规整矩形。
func (a *annotation) span() image.Rectangle {
	if len(a.pts) == 0 {
		return image.Rectangle{}
	}
	p, q := a.pts[0], a.pts[len(a.pts)-1]
	return image.Rect(p.X, p.Y, q.X, q.Y).Canon()
}

// meaningful 判断标注是否值得保留：手抖产生的零长度箭头、零面积矩形直接丢弃。
func (a *annotation) meaningful() bool {
	switch {
	case a.tool == toolRect, a.tool == toolEllipse, a.tool == toolMosaic && a.area:
		r := a.span()
		return r.Dx() >= 2 && r.Dy() >= 2
	}
	switch a.tool {
	case toolArrow:
		r := a.span()
		return r.Dx()+r.Dy() >= 4
	case toolText:
		return strings.TrimSpace(a.text) != ""
	default:
		return len(a.pts) > 0
	}
}

// annotRenderer 持有绘制标注所需的上下文。
type annotRenderer struct {
	hdc   win.HDC
	scale float64
	// off 是目标画布的原点在遮罩坐标中的位置，绘制时所有坐标都要减去它。
	off image.Point
	// mosaic、blur 是整屏马赛克化、模糊化之后的画面（遮罩坐标），为 0 时对应的打码不绘制。
	mosaic win.HDC
	blur   win.HDC
}

// draw 把标注依次画到画布上。
func (r *annotRenderer) draw(list []*annotation) {
	g := newGraphics(r.hdc)
	defer g.Close()
	for _, a := range list {
		switch a.tool {
		case toolMosaic:
			// 马赛克走 GDI 路径裁剪，与 GDI+ 交替使用同一个 DC 时要先结束 GDI+ 的批处理。
			g.Close()
			r.drawMosaic(a)
			g = newGraphics(r.hdc)
		case toolText:
			g.Close()
			r.drawTextAnnot(a)
			g = newGraphics(r.hdc)
		default:
			if g != nil {
				r.drawShape(g, a)
			}
		}
	}
}

// shift 把遮罩坐标换算成画布坐标。
func (r *annotRenderer) shift(p image.Point) image.Point {
	return p.Sub(r.off)
}

// drawShape 绘制矩形、椭圆、箭头与画笔。
func (r *annotRenderer) drawShape(g *gpGraphics, a *annotation) {
	width := a.strokeWidth(r.scale)
	switch a.tool {
	case toolRect:
		g.drawRect(a.color, width, a.span().Sub(r.off))
	case toolEllipse:
		g.drawEllipse(a.color, width, a.span().Sub(r.off))
	case toolArrow:
		from, to := r.shift(a.pts[0]), r.shift(a.pts[len(a.pts)-1])
		g.fillPolygon(a.color, arrowPolygon(from, to, float64(width)))
	case toolPen:
		pts := make([]image.Point, len(a.pts))
		for i, p := range a.pts {
			pts[i] = r.shift(p)
		}
		if len(pts) == 1 {
			// 只点了一下：画一个与线宽相同的圆点。
			d := int(width + 0.5)
			c := pts[0]
			g.fillEllipse(a.color, image.Rect(c.X-d/2, c.Y-d/2, c.X-d/2+d, c.Y-d/2+d))
			return
		}
		g.drawPolyline(a.color, 255, width, pts)
	}
}

// drawMosaic 打码：贴上整屏马赛克化（或模糊化）之后的画面。框选打码直接贴进矩形；
// 涂抹打码把轨迹加粗成路径作为裁剪区，只贴轨迹覆盖到的部分。
func (r *annotRenderer) drawMosaic(a *annotation) {
	src := r.mosaic
	if a.blur {
		src = r.blur
	}
	if src == 0 || len(a.pts) == 0 {
		return
	}
	if a.area {
		b := a.span().Sub(r.off)
		blit(r.hdc, b, src, b.Min.Add(r.off))
		return
	}
	width := dip(mosaicWidths[a.level], r.scale)
	save := win.SaveDC(r.hdc)
	defer win.RestoreDC(r.hdc, save)

	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID}
	pen := win.ExtCreatePen(win.PS_GEOMETRIC|win.PS_ENDCAP_ROUND|win.PS_JOIN_ROUND, uint32(width), &lb, 0, nil)
	if pen == 0 {
		return
	}
	defer win.DeleteObject(win.HGDIOBJ(pen))
	oldPen := win.SelectObject(r.hdc, win.HGDIOBJ(pen))
	defer win.SelectObject(r.hdc, oldPen)

	procBeginPath.Call(uintptr(r.hdc))
	first := r.shift(a.pts[0])
	win.MoveToEx(r.hdc, first.X, first.Y, nil)
	if len(a.pts) == 1 {
		// 单点时补一段极短的线，否则加粗后的路径是空的。
		win.LineTo(r.hdc, int32(first.X+1), int32(first.Y))
	}
	for _, p := range a.pts[1:] {
		q := r.shift(p)
		win.LineTo(r.hdc, int32(q.X), int32(q.Y))
	}
	procEndPath.Call(uintptr(r.hdc))
	procWidenPath.Call(uintptr(r.hdc))
	procSelectClipPath.Call(uintptr(r.hdc), rgnAnd)

	b := a.bounds(r.scale).Sub(r.off)
	blit(r.hdc, b, src, b.Min.Add(r.off))
}

// drawTextAnnot 绘制文字标注。
func (r *annotRenderer) drawTextAnnot(a *annotation) {
	font := newFont(dip(textSizes[a.level], r.scale), false)
	if font == 0 {
		return
	}
	defer win.DeleteObject(win.HGDIOBJ(font))
	p := r.shift(a.pts[0])
	size := measureText(r.hdc, a.text, font)
	drawText(r.hdc, a.text, image.Rectangle{Min: p, Max: p.Add(size)}, font, a.color, 0)
}

// bounds 返回标注在遮罩坐标中占据的范围（含线宽），用于局部重绘。
func (a *annotation) bounds(scale float64) image.Rectangle {
	if len(a.pts) == 0 {
		return image.Rectangle{}
	}
	var r image.Rectangle
	switch a.tool {
	case toolText:
		// 文字的实际尺寸要量过才知道，这里按最长一行粗略估一个足够大的范围。
		px := dip(textSizes[a.level], scale)
		lines := strings.Split(a.text, "\n")
		longest := 1
		for _, l := range lines {
			longest = max(longest, len([]rune(l)))
		}
		r = image.Rect(0, 0, (longest+2)*px, (len(lines)+1)*px*3/2).Add(a.pts[0])
		return r
	default:
		r = image.Rectangle{Min: a.pts[0], Max: a.pts[0].Add(image.Pt(1, 1))}
		for _, p := range a.pts[1:] {
			r = r.Union(image.Rectangle{Min: p, Max: p.Add(image.Pt(1, 1))})
		}
	}
	pad := int(strokeWidths[a.level]*scale) + 2
	if a.tool == toolArrow {
		pad = int(strokeWidths[a.level]*scale*4) + 4
	}
	if a.tool == toolMosaic {
		pad = dip(mosaicWidths[a.level], scale)/2 + 2
		if a.area {
			pad = 2
		}
	}
	return r.Inset(-pad)
}

// arrowPolygon 计算一支实心箭头的多边形：尾部细、往前逐渐变粗，末端是三角形箭头。
// width 是线宽，箭头大小随之等比放大。起点终点重合时返回 nil。
func arrowPolygon(from, to image.Point, width float64) []pointF {
	dx, dy := float64(to.X-from.X), float64(to.Y-from.Y)
	length := math.Hypot(dx, dy)
	if length < 1 {
		return nil
	}
	ux, uy := dx/length, dy/length
	nx, ny := -uy, ux

	headLen := math.Min(math.Max(width*4.5, 12), length*0.6)
	headHalf := headLen * 0.55
	shaftHalf := math.Max(width*0.6, 1)
	tailHalf := math.Max(width*0.15, 0.5)

	bx, by := float64(to.X)-ux*headLen, float64(to.Y)-uy*headLen
	fx, fy := float64(from.X), float64(from.Y)
	pt := func(x, y float64) pointF { return pointF{float32(x), float32(y)} }
	return []pointF{
		pt(fx+nx*tailHalf, fy+ny*tailHalf),
		pt(bx+nx*shaftHalf, by+ny*shaftHalf),
		pt(bx+nx*headHalf, by+ny*headHalf),
		pt(float64(to.X), float64(to.Y)),
		pt(bx-nx*headHalf, by-ny*headHalf),
		pt(bx-nx*shaftHalf, by-ny*shaftHalf),
		pt(fx-nx*tailHalf, fy-ny*tailHalf),
	}
}

// pixelate 生成整屏马赛克化的画面：每个 block×block 的方块填成它的平均色。
func pixelate(src *capture.Bitmap, block int) (*capture.Bitmap, error) {
	dst, err := capture.NewBitmap(src.Width, src.Height)
	if err != nil {
		return nil, err
	}
	w, h := src.Width, src.Height
	for by := 0; by < h; by += block {
		for bx := 0; bx < w; bx += block {
			x1, y1 := min(bx+block, w), min(by+block, h)
			var sb, sg, sr, n int
			for y := by; y < y1; y++ {
				row := src.Pix[(y*w+bx)*4 : (y*w+x1)*4]
				for i := 0; i < len(row); i += 4 {
					sb += int(row[i])
					sg += int(row[i+1])
					sr += int(row[i+2])
					n++
				}
			}
			cb, cg, cr := byte(sb/n), byte(sg/n), byte(sr/n)
			for y := by; y < y1; y++ {
				row := dst.Pix[(y*w+bx)*4 : (y*w+x1)*4]
				for i := 0; i < len(row); i += 4 {
					row[i], row[i+1], row[i+2], row[i+3] = cb, cg, cr, 255
				}
			}
		}
	}
	return dst, nil
}

// blurred 生成整屏模糊化的画面：横竖各做三遍盒式模糊，效果接近高斯模糊。
// 每一遍用滑动窗口求和，耗时与半径无关，4K 整屏也只要零点几秒。
func blurred(src *capture.Bitmap, radius int) (*capture.Bitmap, error) {
	dst, err := capture.NewBitmap(src.Width, src.Height)
	if err != nil {
		return nil, err
	}
	w, h := src.Width, src.Height
	a := make([]uint8, len(src.Pix))
	copy(a, src.Pix)
	b := make([]uint8, len(src.Pix))
	for pass := 0; pass < 3; pass++ {
		boxBlurLine(a, b, w, h, radius, 4, w*4) // 横向：沿每一行
		boxBlurLine(b, a, h, w, radius, w*4, 4) // 纵向：沿每一列
	}
	for i := 0; i+3 < len(a); i += 4 {
		dst.Pix[i], dst.Pix[i+1], dst.Pix[i+2], dst.Pix[i+3] = a[i], a[i+1], a[i+2], 255
	}
	return dst, nil
}

// boxBlurLine 对 lines 条长度为 n 的线做一维盒式模糊：step 是线内相邻像素的字节跨度，
// lineStep 是相邻两条线起点的字节跨度。边缘按最近的像素延伸。
func boxBlurLine(src, dst []uint8, n, lines, radius, step, lineStep int) {
	span := 2*radius + 1
	for l := 0; l < lines; l++ {
		base := l * lineStep
		for c := 0; c < 3; c++ {
			at := func(i int) int {
				i = max(0, min(i, n-1))
				return int(src[base+i*step+c])
			}
			sum := 0
			for i := -radius; i <= radius; i++ {
				sum += at(i)
			}
			for i := 0; i < n; i++ {
				dst[base+i*step+c] = uint8(sum / span)
				sum += at(i+radius+1) - at(i-radius)
			}
		}
	}
}

// dimmed 生成压暗的画面，作为遮罩上选区以外部分的底图。
func dimmed(src *capture.Bitmap) (*capture.Bitmap, error) {
	dst, err := capture.NewBitmap(src.Width, src.Height)
	if err != nil {
		return nil, err
	}
	// 乘 9/16 大约压暗到原来的 56%，与常见截图工具的遮罩深浅相当。
	for i := 0; i+3 < len(src.Pix); i += 4 {
		dst.Pix[i] = byte(int(src.Pix[i]) * 9 / 16)
		dst.Pix[i+1] = byte(int(src.Pix[i+1]) * 9 / 16)
		dst.Pix[i+2] = byte(int(src.Pix[i+2]) * 9 / 16)
		dst.Pix[i+3] = 255
	}
	return dst, nil
}
