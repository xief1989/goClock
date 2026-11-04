package ui

import (
	"image"
	"math"
	"sync"
	"unsafe"

	"github.com/lxn/walk"
	"github.com/lxn/win"
	"golang.org/x/sys/windows"
)

// GDI+ 的最小绑定，只覆盖截图标注需要的抗锯齿线条、多边形与椭圆。
//
// GDI 画斜线、椭圆有锯齿，箭头和圆圈会很难看，所以标注改用 GDI+ 绘制。
// GDI+ 的坐标、线宽是 float32：amd64 上 Go 的 syscall 会把前四个参数同时放进
// 整数寄存器和 XMM 寄存器，第五个起放在栈上，因此把 float32 的位模式当作 uintptr 传入即可。

var (
	libGdiplus = windows.NewLazySystemDLL("gdiplus.dll")

	procGdiplusStartup        = libGdiplus.NewProc("GdiplusStartup")
	procGdipCreateFromHDC     = libGdiplus.NewProc("GdipCreateFromHDC")
	procGdipDeleteGraphics    = libGdiplus.NewProc("GdipDeleteGraphics")
	procGdipSetSmoothingMode  = libGdiplus.NewProc("GdipSetSmoothingMode")
	procGdipCreatePen1        = libGdiplus.NewProc("GdipCreatePen1")
	procGdipDeletePen         = libGdiplus.NewProc("GdipDeletePen")
	procGdipSetPenStartCap    = libGdiplus.NewProc("GdipSetPenStartCap")
	procGdipSetPenEndCap      = libGdiplus.NewProc("GdipSetPenEndCap")
	procGdipSetPenLineJoin    = libGdiplus.NewProc("GdipSetPenLineJoin")
	procGdipCreateSolidFill   = libGdiplus.NewProc("GdipCreateSolidFill")
	procGdipDeleteBrush       = libGdiplus.NewProc("GdipDeleteBrush")
	procGdipDrawRectangleI    = libGdiplus.NewProc("GdipDrawRectangleI")
	procGdipDrawEllipseI      = libGdiplus.NewProc("GdipDrawEllipseI")
	procGdipDrawLinesI        = libGdiplus.NewProc("GdipDrawLinesI")
	procGdipDrawArcI          = libGdiplus.NewProc("GdipDrawArcI")
	procGdipFillPolygon       = libGdiplus.NewProc("GdipFillPolygon")
	procGdipFillEllipseI      = libGdiplus.NewProc("GdipFillEllipseI")
	procGdipFillRectangleI    = libGdiplus.NewProc("GdipFillRectangleI")
	procGdipSetPixelOffsetMod = libGdiplus.NewProc("GdipSetPixelOffsetMode")
)

// GDI+ 枚举值。
const (
	gdipSmoothingAntiAlias = 4
	gdipPixelOffsetHalf    = 4
	gdipUnitPixel          = 2
	gdipLineCapRound       = 2
	gdipLineJoinRound      = 2
	gdipFillModeAlternate  = 0
)

var (
	gdipOnce sync.Once
	gdipOK   bool
	// gdipToken 是本程序自己的 GDI+ 令牌。walk 读图片时也会启停 GDI+，
	// 它用的是 lxn/win 里的全局令牌，这里单独保存一份，互不干扰。
	gdipToken uintptr
)

// gdipStartupInput 对应 GdiplusStartupInput。
type gdipStartupInput struct {
	version                  uint32
	debugEventCallback       uintptr
	suppressBackgroundThread int32
	suppressExternalCodecs   int32
}

// gdipReady 确保 GDI+ 已初始化，初始化失败时返回 false（此时标注退化为不绘制）。
func gdipReady() bool {
	gdipOnce.Do(func() {
		if procGdiplusStartup.Find() != nil {
			return
		}
		input := gdipStartupInput{version: 1}
		ret, _, _ := procGdiplusStartup.Call(uintptr(unsafe.Pointer(&gdipToken)),
			uintptr(unsafe.Pointer(&input)), 0)
		gdipOK = ret == 0
	})
	return gdipOK
}

// pointF 对应 GDI+ 的 PointF。
type pointF struct {
	X, Y float32
}

// gpGraphics 包装一个 GDI+ 绘图对象，所有绘制都开启抗锯齿。
type gpGraphics struct {
	h uintptr
}

// newGraphics 在 hdc 上创建 GDI+ 绘图对象，GDI+ 不可用时返回 nil。
// hdc 上已有的裁剪区域对 GDI+ 同样生效。
func newGraphics(hdc win.HDC) *gpGraphics {
	if !gdipReady() {
		return nil
	}
	var g uintptr
	if ret, _, _ := procGdipCreateFromHDC.Call(uintptr(hdc), uintptr(unsafe.Pointer(&g))); ret != 0 || g == 0 {
		return nil
	}
	procGdipSetSmoothingMode.Call(g, gdipSmoothingAntiAlias)
	// 像素中心对齐到 .5，整数坐标的多边形边缘才不会整体偏半个像素。
	procGdipSetPixelOffsetMod.Call(g, gdipPixelOffsetHalf)
	return &gpGraphics{h: g}
}

// Close 释放绘图对象。
func (g *gpGraphics) Close() {
	if g != nil && g.h != 0 {
		procGdipDeleteGraphics.Call(g.h)
		g.h = 0
	}
}

// argb 把 walk.Color 加上不透明度转换成 GDI+ 的 ARGB。
func argb(c walk.Color, alpha uint8) uintptr {
	return uintptr(alpha)<<24 | uintptr(c.R())<<16 | uintptr(c.G())<<8 | uintptr(c.B())
}

// f32 把 float32 的位模式装进 uintptr，用于传给 GDI+。
func f32(v float32) uintptr {
	return uintptr(math.Float32bits(v))
}

// withPen 创建一支圆头圆角的画笔执行 fn，结束后释放。
func (g *gpGraphics) withPen(c walk.Color, alpha uint8, width float32, fn func(pen uintptr)) {
	var pen uintptr
	if ret, _, _ := procGdipCreatePen1.Call(argb(c, alpha), f32(width), gdipUnitPixel,
		uintptr(unsafe.Pointer(&pen))); ret != 0 || pen == 0 {
		return
	}
	defer procGdipDeletePen.Call(pen)
	procGdipSetPenStartCap.Call(pen, gdipLineCapRound)
	procGdipSetPenEndCap.Call(pen, gdipLineCapRound)
	procGdipSetPenLineJoin.Call(pen, gdipLineJoinRound)
	fn(pen)
}

// withBrush 创建纯色画刷执行 fn，结束后释放。
func (g *gpGraphics) withBrush(c walk.Color, alpha uint8, fn func(brush uintptr)) {
	var brush uintptr
	if ret, _, _ := procGdipCreateSolidFill.Call(argb(c, alpha), uintptr(unsafe.Pointer(&brush))); ret != 0 || brush == 0 {
		return
	}
	defer procGdipDeleteBrush.Call(brush)
	fn(brush)
}

// drawRect 画矩形边框。
func (g *gpGraphics) drawRect(c walk.Color, width float32, r image.Rectangle) {
	g.withPen(c, 255, width, func(pen uintptr) {
		procGdipDrawRectangleI.Call(g.h, pen, uintptr(r.Min.X), uintptr(r.Min.Y), uintptr(r.Dx()), uintptr(r.Dy()))
	})
}

// drawEllipse 画内切于矩形的椭圆边框。
func (g *gpGraphics) drawEllipse(c walk.Color, width float32, r image.Rectangle) {
	g.withPen(c, 255, width, func(pen uintptr) {
		procGdipDrawEllipseI.Call(g.h, pen, uintptr(r.Min.X), uintptr(r.Min.Y), uintptr(r.Dx()), uintptr(r.Dy()))
	})
}

// drawPolyline 画折线，alpha 为不透明度。
func (g *gpGraphics) drawPolyline(c walk.Color, alpha uint8, width float32, pts []image.Point) {
	if len(pts) < 2 {
		return
	}
	buf := make([]int32, 0, len(pts)*2)
	for _, p := range pts {
		buf = append(buf, int32(p.X), int32(p.Y))
	}
	g.withPen(c, alpha, width, func(pen uintptr) {
		procGdipDrawLinesI.Call(g.h, pen, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(pts)))
	})
}

// drawArc 画椭圆弧，角度单位为度，0 度指向右方、顺时针为正。
func (g *gpGraphics) drawArc(c walk.Color, width float32, r image.Rectangle, start, sweep float32) {
	g.withPen(c, 255, width, func(pen uintptr) {
		procGdipDrawArcI.Call(g.h, pen, uintptr(r.Min.X), uintptr(r.Min.Y), uintptr(r.Dx()), uintptr(r.Dy()),
			f32(start), f32(sweep))
	})
}

// fillPolygon 填充多边形。
func (g *gpGraphics) fillPolygon(c walk.Color, pts []pointF) {
	if len(pts) < 3 {
		return
	}
	g.withBrush(c, 255, func(brush uintptr) {
		procGdipFillPolygon.Call(g.h, brush, uintptr(unsafe.Pointer(&pts[0])), uintptr(len(pts)), gdipFillModeAlternate)
	})
}

// fillEllipse 填充内切于矩形的椭圆。
func (g *gpGraphics) fillEllipse(c walk.Color, r image.Rectangle) {
	g.fillEllipseAlpha(c, 255, r)
}

// fillEllipseAlpha 用半透明颜色填充内切于矩形的椭圆。
func (g *gpGraphics) fillEllipseAlpha(c walk.Color, alpha uint8, r image.Rectangle) {
	g.withBrush(c, alpha, func(brush uintptr) {
		procGdipFillEllipseI.Call(g.h, brush, uintptr(r.Min.X), uintptr(r.Min.Y), uintptr(r.Dx()), uintptr(r.Dy()))
	})
}

// fillRectAlpha 用半透明颜色填充矩形。
func (g *gpGraphics) fillRectAlpha(c walk.Color, alpha uint8, r image.Rectangle) {
	g.withBrush(c, alpha, func(brush uintptr) {
		procGdipFillRectangleI.Call(g.h, brush, uintptr(r.Min.X), uintptr(r.Min.Y), uintptr(r.Dx()), uintptr(r.Dy()))
	})
}
