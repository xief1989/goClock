package ui

import (
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/lxn/win"

	"goclock/internal/capture"
)

// TestRenderShotPreview 把截图工具栏、调色板、放大镜之外的各类标注离屏渲染成 PNG，用于人工检查外观。
// 渲染走的是遮罩与导出共用的绘制函数，因此图片与实际效果一致。
//
// 默认跳过，需要时这样执行：
//
//	$env:GOCLOCK_PREVIEW_DIR="C:\temp"; go test ./internal/ui -run TestRenderShotPreview -v
func TestRenderShotPreview(t *testing.T) {
	dir := os.Getenv("GOCLOCK_PREVIEW_DIR")
	if dir == "" {
		t.Skip("未设置 GOCLOCK_PREVIEW_DIR，跳过截图标注预览渲染")
	}
	for _, scale := range []float64{1, 1.5} {
		for _, mode := range []paletteMode{paletteDraw, paletteMosaic} {
			path := filepath.Join(dir, fmt.Sprintf("shot-%.0f-palette%d.png", scale*100, mode))
			if err := renderShotPreview(scale, mode, path); err != nil {
				t.Fatalf("缩放 %.0f%% 渲染失败：%v", scale*100, err)
			}
			t.Logf("已输出 %s", path)
		}
	}
}

// renderShotPreview 按缩放比例 scale 渲染一张预览图，mode 决定选项条显示哪一组。
func renderShotPreview(scale float64, mode paletteMode, path string) error {
	px := func(v int) int { return dip(v, scale) }
	w, h := px(900), px(560)

	bg, err := capture.NewBitmap(w, h)
	if err != nil {
		return err
	}
	// 底图：浅色背景上画几行「文字」色块，马赛克有东西可糊。
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			i := (y*w + x) * 4
			v := byte(245)
			if (y/px(18))%2 == 0 && (x/px(9))%3 != 0 && y%px(18) > px(4) && y%px(18) < px(14) {
				v = byte(60 + (x*7+y*3)%80)
			}
			bg.Pix[i], bg.Pix[i+1], bg.Pix[i+2], bg.Pix[i+3] = v, v, v+5, 255
		}
	}
	mos, err := pixelate(bg, px(mosaicBlock))
	if err != nil {
		return err
	}
	mosCanvas, err := newMemCanvas(mos)
	if err != nil {
		return err
	}
	defer mosCanvas.Dispose()
	blr, err := blurred(bg, px(blurRadius))
	if err != nil {
		return err
	}
	blurCanvas, err := newMemCanvas(blr)
	if err != nil {
		return err
	}
	defer blurCanvas.Dispose()
	c, err := newMemCanvas(bg)
	if err != nil {
		return err
	}
	defer c.Dispose()

	pt := func(x, y int) image.Point { return image.Pt(px(x), px(y)) }
	var pen []image.Point
	for i := 0; i <= 40; i++ {
		pen = append(pen, pt(560+i*5, 120+(i%8)*6))
	}
	annots := []*annotation{
		{tool: toolRect, color: shotColors[0], level: 0, pts: []image.Point{pt(40, 40), pt(220, 140)}},
		{tool: toolEllipse, color: shotColors[3], level: 1, pts: []image.Point{pt(260, 40), pt(480, 150)}},
		{tool: toolArrow, color: shotColors[0], level: 0, pts: []image.Point{pt(60, 300), pt(220, 200)}},
		{tool: toolArrow, color: shotColors[2], level: 2, pts: []image.Point{pt(260, 320), pt(480, 200)}},
		{tool: toolPen, color: shotColors[1], level: 1, pts: pen},
		{tool: toolMosaic, color: shotColors[0], level: 2, pts: []image.Point{pt(560, 240), pt(700, 260), pt(840, 240)}},
		{tool: toolMosaic, level: 2, blur: true, pts: []image.Point{pt(560, 300), pt(700, 320), pt(840, 300)}},
		{tool: toolMosaic, area: true, blur: true, pts: []image.Point{pt(260, 340), pt(480, 400)}},
		{tool: toolMosaic, area: true, pts: []image.Point{pt(520, 350), pt(700, 400)}},
		{tool: toolText, color: shotColors[0], level: 1, pts: []image.Point{pt(40, 340)}, text: "文字标注 Text 123\n第二行"},
	}
	r := annotRenderer{hdc: c.dc, scale: scale, mosaic: mosCanvas.dc, blur: blurCanvas.dc}
	r.draw(annots)

	sel := image.Rect(px(20), px(20), px(880), px(420))
	frameRect(c.dc, sel, dip(1, scale), colorAccent)
	l := layoutToolbar(sel, image.Rect(0, 0, w, h), scale, mode)
	st := toolbarState{tool: toolArrow, color: 0, level: 1, hoverBtn: 10, hoverPal: -1, canUndo: true}
	if mode == paletteMosaic {
		st = toolbarState{tool: toolMosaic, level: 2, mosaicShape: 1, mosaicStyle: 1, hoverBtn: -1, hoverPal: 3, canUndo: true}
	}
	drawToolbar(c.dc, &l, st, scale)
	font := newFont(dip(12, scale), false)
	defer win.DeleteObject(win.HGDIOBJ(font))
	anchor, tip := image.Rectangle{}, ""
	if st.hoverBtn >= 0 {
		anchor, tip = l.buttons[st.hoverBtn], barItems[st.hoverBtn].tip
	} else {
		anchor, tip = l.items[st.hoverPal].rect, l.items[st.hoverPal].tip
	}
	drawTooltip(c.dc, l.tipRect(anchor, measureText(c.dc, tip, font), image.Rect(0, 0, w, h), scale), tip, font)
	win.GdiFlush()

	img := bg.RGBA(image.Rect(0, 0, w, h))
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return png.Encode(f, img)
}
