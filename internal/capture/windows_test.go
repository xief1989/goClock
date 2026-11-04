package capture

import (
	"image"
	"testing"
)

func TestPickPrefersInnermostChildOfTopWindow(t *testing.T) {
	targets := []Target{
		{
			// 最上层的窗口，内含一个工具栏和一个编辑区，编辑区里还有一个更小的控件。
			Rect: image.Rect(100, 100, 600, 500),
			Children: []image.Rectangle{
				image.Rect(100, 100, 600, 140),
				image.Rect(100, 140, 600, 500),
				image.Rect(300, 300, 400, 350),
			},
		},
		{
			// 被压在下面、范围更大的窗口。
			Rect: image.Rect(0, 0, 1000, 800),
		},
	}

	cases := []struct {
		p    image.Point
		want image.Rectangle
		ok   bool
	}{
		{image.Pt(120, 120), image.Rect(100, 100, 600, 140), true},
		{image.Pt(320, 320), image.Rect(300, 300, 400, 350), true},
		{image.Pt(200, 400), image.Rect(100, 140, 600, 500), true},
		{image.Pt(50, 50), image.Rect(0, 0, 1000, 800), true},
		{image.Pt(2000, 2000), image.Rectangle{}, false},
	}
	for _, c := range cases {
		got, ok := Pick(targets, c.p)
		if ok != c.ok || got != c.want {
			t.Errorf("Pick(%v)：got=%v,%v want=%v,%v", c.p, got, ok, c.want, c.ok)
		}
	}
}
