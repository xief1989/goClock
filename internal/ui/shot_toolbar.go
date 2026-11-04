package ui

import (
	"image"
	"math"

	"github.com/lxn/walk"
	"github.com/lxn/win"
)

// shotButton 是截图工具栏上的按钮。
type shotButton int

const (
	btnRect shotButton = iota
	btnEllipse
	btnArrow
	btnPen
	btnMosaic
	btnText
	btnUndo
	btnOCR
	btnTranslate
	btnLong
	btnRecord
	btnPin
	btnSave
	btnCancel
	btnDone
)

// barItem 描述工具栏上的一个按钮。
type barItem struct {
	btn shotButton
	tip string
	// sepBefore 表示按钮前面画一条分隔线。
	sepBefore bool
}

// barItems 是工具栏按钮的排列顺序，与微信截图的布局一致：绘图工具、撤销、文字处理、输出方式、退出与完成。
var barItems = []barItem{
	{btnRect, "矩形", false},
	{btnEllipse, "椭圆", false},
	{btnArrow, "箭头", false},
	{btnPen, "画笔", false},
	{btnMosaic, "马赛克 / 模糊", false},
	{btnText, "文字", false},
	{btnUndo, "撤销  Ctrl+Z", true},
	{btnOCR, "识别文字", true},
	{btnTranslate, "翻译", false},
	{btnLong, "长截图", true},
	{btnRecord, "录屏", false},
	{btnPin, "钉在桌面", false},
	{btnSave, "保存  Ctrl+S", false},
	{btnCancel, "退出  Esc", true},
	{btnDone, "完成  Enter / 双击", false},
}

// tool 返回按钮对应的绘图工具，不是绘图按钮时返回 toolNone。
func (b shotButton) tool() shotTool {
	switch b {
	case btnRect:
		return toolRect
	case btnEllipse:
		return toolEllipse
	case btnArrow:
		return toolArrow
	case btnPen:
		return toolPen
	case btnMosaic:
		return toolMosaic
	case btnText:
		return toolText
	}
	return toolNone
}

// 工具栏尺寸（96 DPI 下的像素）。
const (
	barButtonSize = 30
	barPadding    = 4
	barGap        = 2
	barSepWidth   = 9
	barOffset     = 6
	paletteHeight = 32
	paletteSize   = 26
	paletteSwatch = 22
)

// 工具栏配色。
var (
	colorBarBg      = walk.RGB(0xFF, 0xFF, 0xFF)
	colorBarBorder  = walk.RGB(0xD1, 0xD5, 0xDB)
	colorBarIcon    = walk.RGB(0x37, 0x41, 0x51)
	colorBarHover   = walk.RGB(0xF3, 0xF4, 0xF6)
	colorBarActive  = walk.RGB(0xE0, 0xEA, 0xFF)
	colorBarSep     = walk.RGB(0xE5, 0xE7, 0xEB)
	colorDone       = walk.RGB(0x16, 0xA3, 0x4A)
	colorCancel     = walk.RGB(0xDC, 0x26, 0x26)
	colorTipBg      = walk.RGB(0x1F, 0x29, 0x37)
	colorTipText    = walk.RGB(0xF9, 0xFA, 0xFB)
	colorDotDefault = walk.RGB(0x6B, 0x72, 0x80)
)

// paletteMode 表示选项条显示哪一组选项。
type paletteMode int

const (
	paletteNone paletteMode = iota
	// paletteDraw：粗细 + 颜色，用于矩形、椭圆、箭头、画笔、文字。
	paletteDraw
	// paletteMosaic：画笔/矩形 + 马赛克/模糊 + 粗细，马赛克与颜色无关。
	paletteMosaic
)

// paletteKind 是选项条上一项的类别。
type paletteKind int

const (
	palSize paletteKind = iota
	palColor
	palShape
	palStyle
)

// paletteItem 是选项条上的一项。
type paletteItem struct {
	kind  paletteKind
	index int
	rect  image.Rectangle
	tip   string
}

// 选项条各项的提示文字。
var (
	sizeTips  = [3]string{"细", "中", "粗"}
	shapeTips = [2]string{"画笔涂抹", "框选区域"}
	styleTips = [2]string{"马赛克", "模糊"}
)

// toolbarLayout 是工具栏与选项条在遮罩坐标中的位置。
type toolbarLayout struct {
	bar     image.Rectangle
	buttons []image.Rectangle
	// palette 为空表示不显示选项条（当前没有选中绘图工具）。
	palette image.Rectangle
	items   []paletteItem
	// seps 是选项条里分隔线的横坐标。
	seps []int
	// below 表示工具栏在选区下方，提示条与选项条据此决定往哪边放。
	below bool
}

// layoutToolbar 计算工具栏位置：默认贴在选区右下方，下面放不下就放到上方，再不行就放进选区里面。
// mon 是选区所在显示器的范围（遮罩坐标）。
func layoutToolbar(sel, mon image.Rectangle, scale float64, mode paletteMode) toolbarLayout {
	btn := dip(barButtonSize, scale)
	pad := dip(barPadding, scale)
	gap := dip(barGap, scale)
	sep := dip(barSepWidth, scale)
	off := dip(barOffset, scale)
	barH := btn + pad*2

	width := pad*2 - gap
	for _, it := range barItems {
		if it.sepBefore {
			width += sep
		}
		width += btn + gap
	}

	palH := 0
	if mode != paletteNone {
		palH = dip(paletteHeight, scale) + off
	}

	l := toolbarLayout{below: true}
	x := sel.Max.X - width
	y := sel.Max.Y + off
	switch {
	case y+barH+palH <= mon.Max.Y:
	case sel.Min.Y-off-barH-palH >= mon.Min.Y:
		y = sel.Min.Y - off - barH
		l.below = false
	default:
		y = sel.Max.Y - off - barH
		l.below = false
	}
	x = max(mon.Min.X, min(x, mon.Max.X-width))
	l.bar = image.Rect(x, y, x+width, y+barH)

	cx := x + pad
	for _, it := range barItems {
		if it.sepBefore {
			cx += sep
		}
		l.buttons = append(l.buttons, image.Rect(cx, y+pad, cx+btn, y+pad+btn))
		cx += btn + gap
	}

	if mode != paletteNone {
		l.layoutPalette(mode, x, scale)
	}
	return l
}

// layoutPalette 排布选项条：从工具栏左端开始，放在工具栏远离选区的一侧。
func (l *toolbarLayout) layoutPalette(mode paletteMode, x int, scale float64) {
	pad := dip(barPadding, scale)
	gap := dip(barGap, scale)
	sep := dip(barSepWidth, scale)
	off := dip(barOffset, scale)
	cell := dip(paletteSize, scale)
	sw := dip(paletteSwatch, scale)
	ph := dip(paletteHeight, scale)

	py := l.bar.Max.Y + off
	if !l.below {
		py = l.bar.Min.Y - off - ph
	}
	cy := py + (ph-cell)/2
	px := x + pad
	addCells := func(kind paletteKind, tips []string) {
		for i, tip := range tips {
			l.items = append(l.items, paletteItem{kind: kind, index: i, tip: tip,
				rect: image.Rect(px, cy, px+cell, cy+cell)})
			px += cell
		}
	}
	addSep := func() {
		l.seps = append(l.seps, px+sep/2)
		px += sep
	}

	switch mode {
	case paletteDraw:
		addCells(palSize, sizeTips[:])
		addSep()
		sy := py + (ph-sw)/2
		for i := range shotColors {
			l.items = append(l.items, paletteItem{kind: palColor, index: i,
				rect: image.Rect(px, sy, px+sw, sy+sw)})
			px += sw + gap
		}
		px -= gap
	case paletteMosaic:
		addCells(palShape, shapeTips[:])
		addSep()
		addCells(palStyle, styleTips[:])
		addSep()
		addCells(palSize, sizeTips[:])
	}
	l.palette = image.Rect(x, py, px+pad, py+ph)
}

// bounds 返回工具栏与选项条合起来占据的范围。
func (l *toolbarLayout) bounds() image.Rectangle {
	return l.bar.Union(l.palette)
}

// buttonAt 返回点 p 下的按钮下标，没有命中时返回 -1。
func (l *toolbarLayout) buttonAt(p image.Point) int {
	for i, r := range l.buttons {
		if p.In(r) {
			return i
		}
	}
	return -1
}

// paletteAt 返回点 p 下的选项下标，没有命中时返回 -1。
func (l *toolbarLayout) paletteAt(p image.Point) int {
	for i, it := range l.items {
		if p.In(it.rect) {
			return i
		}
	}
	return -1
}

// tipRect 返回锚点 anchor（某个按钮或选项）的提示条位置，放在工具栏远离选区的一侧。
func (l *toolbarLayout) tipRect(anchor image.Rectangle, textSize image.Point, mon image.Rectangle, scale float64) image.Rectangle {
	padX, padY := dip(8, scale), dip(4, scale)
	w, h := textSize.X+padX*2, textSize.Y+padY*2
	x := anchor.Min.X + anchor.Dx()/2 - w/2
	x = max(mon.Min.X, min(x, mon.Max.X-w))
	off := dip(4, scale)
	var y int
	if l.below {
		y = l.bounds().Max.Y + off
		if y+h > mon.Max.Y {
			y = l.bar.Min.Y - off - h
		}
	} else {
		y = l.bounds().Min.Y - off - h
		if y < mon.Min.Y {
			y = l.bar.Max.Y + off
		}
	}
	return image.Rect(x, y, x+w, y+h)
}

// toolbarState 是绘制工具栏所需的状态。
type toolbarState struct {
	tool        shotTool
	color       int
	level       int
	mosaicShape int
	mosaicStyle int
	hoverBtn    int
	hoverPal    int
	canUndo     bool
}

// selected 判断选项条上的某一项当前是否被选中。
func (st toolbarState) selected(it paletteItem) bool {
	switch it.kind {
	case palSize:
		return it.index == st.level
	case palColor:
		return it.index == st.color
	case palShape:
		return it.index == st.mosaicShape
	case palStyle:
		return it.index == st.mosaicStyle
	}
	return false
}

// drawToolbar 绘制工具栏与选项条。
func drawToolbar(hdc win.HDC, l *toolbarLayout, st toolbarState, scale float64) {
	border := dip(1, scale)
	fillRect(hdc, l.bar, colorBarBorder)
	fillRect(hdc, l.bar.Inset(border), colorBarBg)

	colors := make([]walk.Color, len(barItems))
	g := newGraphics(hdc)
	for i, it := range barItems {
		r := l.buttons[i]
		if it.sepBefore {
			sx := r.Min.X - dip(barSepWidth, scale)/2 - dip(barGap, scale)/2
			fillRect(hdc, image.Rect(sx, r.Min.Y+dip(6, scale), sx+border, r.Max.Y-dip(6, scale)), colorBarSep)
		}
		active := it.btn.tool() != toolNone && it.btn.tool() == st.tool
		switch {
		case active:
			fillRect(hdc, r, colorBarActive)
		case i == st.hoverBtn:
			fillRect(hdc, r, colorBarHover)
		}
		c := colorBarIcon
		switch {
		case active:
			c = colorAccent
		case it.btn == btnDone:
			c = colorDone
		case it.btn == btnCancel, it.btn == btnRecord:
			c = colorCancel
		case it.btn == btnUndo && !st.canUndo:
			c = colorMuted
		}
		colors[i] = c
		if g != nil {
			drawIcon(g, it.btn, iconBox(r, scale), c, scale)
		}
	}
	g.Close()

	// 「识别文字」「翻译」的图标里有汉字，用 GDI 文字补画；必须在 GDI+ 画完、关掉之后再画。
	glyphFont := newFont(dip(12, scale), true)
	for i, it := range barItems {
		if glyph := iconGlyph(it.btn); glyph != "" {
			drawText(hdc, glyph, iconBox(l.buttons[i], scale), glyphFont, colors[i], dtCenter|dtVCenter|dtSingleLine)
		}
	}
	win.DeleteObject(win.HGDIOBJ(glyphFont))

	if !l.palette.Empty() {
		drawPalette(hdc, l, st, scale)
	}
}

// iconGlyph 返回需要用文字补画的图标字符。
func iconGlyph(b shotButton) string {
	switch b {
	case btnOCR:
		return "文"
	case btnTranslate:
		return "译"
	}
	return ""
}

// iconBox 返回按钮中央用于画图标的正方形区域。
func iconBox(btn image.Rectangle, scale float64) image.Rectangle {
	s := dip(18, scale)
	x := btn.Min.X + (btn.Dx()-s)/2
	y := btn.Min.Y + (btn.Dy()-s)/2
	return image.Rect(x, y, x+s, y+s)
}

// drawPalette 绘制选项条。
func drawPalette(hdc win.HDC, l *toolbarLayout, st toolbarState, scale float64) {
	border := dip(1, scale)
	fillRect(hdc, l.palette, colorBarBorder)
	fillRect(hdc, l.palette.Inset(border), colorBarBg)
	for _, sx := range l.seps {
		fillRect(hdc, image.Rect(sx, l.palette.Min.Y+dip(6, scale), sx+border, l.palette.Max.Y-dip(6, scale)), colorBarSep)
	}

	g := newGraphics(hdc)
	if g == nil {
		return
	}
	defer g.Close()

	dots := [3]int{4, 7, 10}
	for i, it := range l.items {
		r := it.rect
		on := st.selected(it)
		if it.kind == palColor {
			if on {
				fillRect(hdc, r, colorAccent)
				fillRect(hdc, r.Inset(dip(2, scale)), colorBarBg)
				fillRect(hdc, r.Inset(dip(4, scale)), shotColors[it.index])
				continue
			}
			fillRect(hdc, r.Inset(dip(2, scale)), colorBarBorder)
			fillRect(hdc, r.Inset(dip(2, scale)+border), shotColors[it.index])
			continue
		}
		switch {
		case on:
			fillRect(hdc, r.Inset(dip(2, scale)), colorBarActive)
		case i == st.hoverPal:
			fillRect(hdc, r.Inset(dip(2, scale)), colorBarHover)
		}
		c := colorDotDefault
		if on {
			c = colorAccent
		}
		switch it.kind {
		case palSize:
			d := dip(dots[it.index], scale)
			mid := r.Min.Add(image.Pt(r.Dx()/2, r.Dy()/2))
			g.fillEllipse(c, image.Rect(mid.X-d/2, mid.Y-d/2, mid.X-d/2+d, mid.Y-d/2+d))
		case palShape, palStyle:
			drawPaletteIcon(g, it, iconBox(r, scale), c, scale)
		}
	}
}

// drawPaletteIcon 绘制马赛克选项的小图标：画笔、矩形、马赛克格子、模糊光斑。
func drawPaletteIcon(g *gpGraphics, it paletteItem, box image.Rectangle, c walk.Color, scale float64) {
	u := float64(box.Dx()) / 18
	pt := func(x, y float64) image.Point {
		return image.Pt(box.Min.X+int(x*u+0.5), box.Min.Y+int(y*u+0.5))
	}
	w := float32(1.6 * scale)
	switch {
	case it.kind == palShape && it.index == 0:
		drawIcon(g, btnPen, box, c, scale)
	case it.kind == palShape:
		g.drawRect(c, w, image.Rectangle{Min: pt(2.5, 3.5), Max: pt(15.5, 14.5)})
	case it.kind == palStyle && it.index == 0:
		drawIcon(g, btnMosaic, box, c, scale)
	default:
		// 模糊：由外到内越来越实的几圈，像一团晕开的光斑。
		for i, a := range []uint8{50, 90, 160, 255} {
			inset := 1.5 + float64(i)*2
			g.fillEllipseAlpha(c, a, image.Rectangle{Min: pt(inset, inset), Max: pt(18-inset, 18-inset)})
		}
	}
}

// drawTooltip 在 r 内画一条深色提示。
func drawTooltip(hdc win.HDC, r image.Rectangle, text string, font win.HFONT) {
	fillRect(hdc, r, colorTipBg)
	drawText(hdc, text, r, font, colorTipText, dtCenter|dtVCenter|dtSingleLine)
}

// drawIcon 在 18×18（按缩放放大）的方框里画按钮图标，全部用抗锯齿线条绘制，不依赖图片素材。
// 「识别文字」「翻译」只在这里画外框，里面的汉字由 drawToolbar 用 GDI 补上。
func drawIcon(g *gpGraphics, b shotButton, box image.Rectangle, c walk.Color, scale float64) {
	u := float64(box.Dx()) / 18
	pt := func(x, y float64) image.Point {
		return image.Pt(box.Min.X+int(x*u+0.5), box.Min.Y+int(y*u+0.5))
	}
	w := float32(1.6 * scale)
	switch b {
	case btnRect:
		g.drawRect(c, w, image.Rectangle{Min: pt(2, 4), Max: pt(16, 14)})
	case btnEllipse:
		g.drawEllipse(c, w, image.Rectangle{Min: pt(1.5, 3.5), Max: pt(16.5, 14.5)})
	case btnArrow:
		g.fillPolygon(c, arrowPolygon(pt(3, 15), pt(15, 3), 1.6*scale))
	case btnPen:
		var pts []image.Point
		for i := 0; i <= 16; i++ {
			x := 1 + float64(i)
			pts = append(pts, pt(x, 9+4*math.Sin(x/16*2*math.Pi)))
		}
		g.drawPolyline(c, 255, w, pts)
	case btnMosaic:
		for row := 0; row < 3; row++ {
			for col := 0; col < 3; col++ {
				r := image.Rectangle{Min: pt(2+float64(col)*5, 2+float64(row)*5), Max: pt(6+float64(col)*5, 6+float64(row)*5)}
				alpha := uint8(90)
				if (row+col)%2 == 0 {
					alpha = 255
				}
				g.fillRectAlpha(c, alpha, r)
			}
		}
	case btnText:
		g.drawPolyline(c, 255, w*1.2, []image.Point{pt(3, 3.5), pt(15, 3.5)})
		g.drawPolyline(c, 255, w*1.2, []image.Point{pt(9, 3.5), pt(9, 15.5)})
	case btnUndo:
		// 逆时针的回退箭头：一段从左侧绕过顶部到右下的弧线，左端带箭头。
		var pts []image.Point
		for a := 180.0; a <= 400; a += 10 {
			rad := a * math.Pi / 180
			pts = append(pts, pt(10+6*math.Cos(rad), 10+6*math.Sin(rad)))
		}
		g.drawPolyline(c, 255, w, pts)
		g.drawPolyline(c, 255, w, []image.Point{pt(1, 7), pt(4, 11), pt(7.5, 8)})
	case btnOCR:
		// 四个角的取景框，中间的「文」字由 GDI 补画。
		for _, corner := range [][3][2]float64{
			{{0.5, 5}, {0.5, 0.5}, {5, 0.5}}, {{13, 0.5}, {17.5, 0.5}, {17.5, 5}},
			{{17.5, 13}, {17.5, 17.5}, {13, 17.5}}, {{5, 17.5}, {0.5, 17.5}, {0.5, 13}},
		} {
			g.drawPolyline(c, 255, w*0.8, []image.Point{
				pt(corner[0][0], corner[0][1]), pt(corner[1][0], corner[1][1]), pt(corner[2][0], corner[2][1]),
			})
		}
	case btnLong:
		g.drawRect(c, w, image.Rectangle{Min: pt(4, 1.5), Max: pt(14, 16.5)})
		g.drawPolyline(c, 255, w, []image.Point{pt(9, 5), pt(9, 12)})
		g.drawPolyline(c, 255, w, []image.Point{pt(6.5, 9.5), pt(9, 12), pt(11.5, 9.5)})
	case btnRecord:
		// 摄像机：机身加镜头三角，中间一个实心红点表示「录制」。
		g.drawRect(c, w, image.Rectangle{Min: pt(1.5, 4.5), Max: pt(12, 13.5)})
		g.fillPolygon(c, []pointF{toPointF(pt(12.5, 9)), toPointF(pt(17, 5.5)), toPointF(pt(17, 12.5))})
		g.fillEllipse(c, image.Rectangle{Min: pt(4.5, 6.8), Max: pt(9, 11.2)})
	case btnPin:
		g.drawEllipse(c, w, image.Rectangle{Min: pt(5, 1.5), Max: pt(13, 9.5)})
		g.drawPolyline(c, 255, w, []image.Point{pt(3.5, 10), pt(14.5, 10)})
		g.drawPolyline(c, 255, w, []image.Point{pt(9, 10), pt(9, 17)})
	case btnSave:
		g.drawPolyline(c, 255, w, []image.Point{pt(9, 2), pt(9, 12)})
		g.drawPolyline(c, 255, w, []image.Point{pt(5, 8), pt(9, 12), pt(13, 8)})
		g.drawPolyline(c, 255, w, []image.Point{pt(2.5, 12), pt(2.5, 16), pt(15.5, 16), pt(15.5, 12)})
	case btnCancel:
		g.drawPolyline(c, 255, w*1.25, []image.Point{pt(4, 4), pt(14, 14)})
		g.drawPolyline(c, 255, w*1.25, []image.Point{pt(14, 4), pt(4, 14)})
	case btnDone:
		g.drawPolyline(c, 255, w*1.4, []image.Point{pt(2.5, 9.5), pt(7, 14), pt(15.5, 4.5)})
	}
}

// toPointF 把整数点转成 GDI+ 的浮点点。
func toPointF(p image.Point) pointF {
	return pointF{X: float32(p.X), Y: float32(p.Y)}
}
