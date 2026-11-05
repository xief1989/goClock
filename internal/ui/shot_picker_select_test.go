package ui

import (
	"image"
	"testing"

	"goclock/internal/ocr"
)

// frag 是一行里的一段文字：从横坐标 x 开始，每个字宽 10、高 16。
type frag struct {
	x    int
	text string
}

// fragLine 用若干段文字拼出一行识别结果，段与段之间补一个空格，空格占两段之间的空隙（与 ocr 拼行一致）。
func fragLine(y int, frags ...frag) ocr.Line {
	var runes []rune
	var glyphs []image.Rectangle
	for i, f := range frags {
		if i > 0 {
			prev := glyphs[len(glyphs)-1]
			runes = append(runes, ' ')
			glyphs = append(glyphs, image.Rect(prev.Max.X, y, f.x, y+16))
		}
		for j, r := range []rune(f.text) {
			runes = append(runes, r)
			glyphs = append(glyphs, image.Rect(f.x+j*10, y, f.x+j*10+10, y+16))
		}
	}
	rect := glyphs[0]
	for _, g := range glyphs {
		rect = rect.Union(g)
	}
	return ocr.Line{Text: string(runes), Rect: rect, Glyphs: glyphs}
}

// drag 模拟从 a 拖到 b，返回选中的文字。
func drag(tp *textPicker, a, b image.Point) string {
	tp.begin(tp.hit(a))
	tp.extend(tp.hit(b))
	return tp.selectedText()
}

func TestPickerFlowSelection(t *testing.T) {
	tp := newTextPicker([]ocr.Line{
		fragLine(0, frag{0, "第一行文字"}),
		fragLine(20, frag{0, "第二行文字"}),
		fragLine(40, frag{0, "第三行"}),
	})
	// 从第一行「行」字的左半边拖到第三行「三」字的左半边。
	if got := drag(tp, image.Pt(22, 8), image.Pt(12, 48)); got != "行文字\n第二行文字\n第" {
		t.Errorf("跨行选择不对：%q", got)
	}
	// 反着拖结果一样。
	if got := drag(tp, image.Pt(12, 48), image.Pt(22, 8)); got != "行文字\n第二行文字\n第" {
		t.Errorf("反向选择不对：%q", got)
	}
	// 单击不拖动没有选区。
	tp.begin(tp.hit(image.Pt(22, 8)))
	if tp.hasSelection() || tp.selectedText() != "" {
		t.Errorf("单击不应产生选区")
	}
}

// threePanes 模拟三栏界面：识别器把同一高度的三栏文字拼成了一行。
func threePanes() *textPicker {
	return newTextPicker([]ocr.Line{
		fragLine(0, frag{0, "左一"}, frag{200, "中一"}, frag{400, "右一"}),
		fragLine(20, frag{0, "左二"}, frag{200, "中二"}, frag{400, "右二"}),
		fragLine(40, frag{0, "左三"}, frag{200, "中三"}, frag{400, "右三"}),
	})
}

func TestPickerStaysInColumn(t *testing.T) {
	tp := threePanes()
	if len(tp.spans) != 9 {
		t.Fatalf("每行应按大间距切成三段，实际 %d 段", len(tp.spans))
	}
	// 在左栏从第一行拖到第三行：只选左栏。
	if got := drag(tp, image.Pt(1, 8), image.Pt(19, 48)); got != "左一\n左二\n左三" {
		t.Errorf("左栏里拖选不应夹进别的栏：%q", got)
	}
	hl := tp.highlights()
	if len(hl) != 3 {
		t.Fatalf("应有三块高亮：%v", hl)
	}
	for _, r := range hl {
		if r.Max.X > 30 {
			t.Errorf("高亮不应伸进中栏：%v", r)
		}
	}
	// 从左栏拖到右栏：跨栏时按行序把经过的都选上。
	if got := drag(tp, image.Pt(1, 8), image.Pt(419, 28)); got != "左一 中一 右一\n左二 中二 右二" {
		t.Errorf("跨栏选择不对：%q", got)
	}
}

func TestPickerSameRowCells(t *testing.T) {
	tp := newTextPicker([]ocr.Line{fragLine(0, frag{0, "A1"}, frag{100, "B2"}, frag{200, "C3"})})
	if got := drag(tp, image.Pt(11, 8), image.Pt(205, 8)); got != "1 B2 C" {
		t.Errorf("同一行跨格选择不对：%q", got)
	}
}

func TestPickerHit(t *testing.T) {
	tp := newTextPicker([]ocr.Line{
		fragLine(0, frag{0, "上面一行"}),
		fragLine(30, frag{0, "下面"}),
	})
	// 行尾右边的空白处仍算这一行的行尾，不跳到下一行。
	if h := tp.hit(image.Pt(300, 20)); h.caret != 4 || h.span != 0 {
		t.Errorf("行尾右侧应命中第一行末尾：%+v", h)
	}
	// 两行之间偏下的空白处命中下一行。
	if h := tp.hit(image.Pt(2, 28)); h.span != 1 || h.caret != 4 {
		t.Errorf("应命中下一行开头：%+v", h)
	}
	if !tp.onText(image.Pt(5, 5)) || tp.onText(image.Pt(5, 20)) {
		t.Errorf("文字上应判为在文字上，行间空白不算")
	}
}

func TestPickerHitStaysInColumnBelowEnd(t *testing.T) {
	// 左栏只有两行，中栏更长；拖到左栏最后一行下面的空白处，旁边同高的是中栏的字，终点仍应落在左栏。
	tp := newTextPicker([]ocr.Line{
		fragLine(0, frag{0, "左一"}, frag{200, "中一"}),
		fragLine(20, frag{0, "左二"}, frag{200, "中二"}),
		fragLine(40, frag{200, "中三"}),
		fragLine(60, frag{200, "中四"}),
	})
	if got := drag(tp, image.Pt(1, 8), image.Pt(5, 65)); got != "左一\n左二" {
		t.Errorf("拖到左栏末尾下方不应选进中栏：%q", got)
	}
}

func TestPickerHitRowEndAndIndent(t *testing.T) {
	tp := newTextPicker([]ocr.Line{
		fragLine(0, frag{0, "短"}),
		fragLine(20, frag{0, "很长很长的一行文字"}),
		fragLine(40, frag{20, "缩进的一行"}),
	})
	// 短行右边很远的空白处仍是这一行的行尾，而不是下面那行长句的中间。
	if h := tp.hit(image.Pt(80, 8)); h.span != 0 || h.caret != 1 {
		t.Errorf("短行右侧应命中它的行尾：%+v", h)
	}
	// 缩进行左边的空白处算这一行的开头。
	if h := tp.hit(image.Pt(5, 48)); h.span != 2 || h.caret != 10 {
		t.Errorf("缩进行左侧应命中它的开头：%+v", h)
	}
}

func TestPickerSelectSpanAndAll(t *testing.T) {
	tp := threePanes()
	tp.selectSpan(tp.hit(image.Pt(205, 28)))
	if got := tp.selectedText(); got != "中二" {
		t.Errorf("双击应选中整段：%q", got)
	}
	tp.selectAll()
	if got := tp.selectedText(); got != "左一 中一 右一\n左二 中二 右二\n左三 中三 右三" {
		t.Errorf("全选不对：%q", got)
	}
	tp.clear()
	if tp.hasSelection() || tp.selectedText() != "" {
		t.Errorf("取消后不应有选区")
	}
}

func TestPickerEmpty(t *testing.T) {
	tp := newTextPicker(nil)
	tp.begin(tp.hit(image.Pt(1, 1)))
	tp.selectAll()
	if !tp.empty() || tp.hasSelection() || tp.selectedText() != "" || tp.highlights() != nil {
		t.Errorf("没有文字时不应有选区")
	}
}
