package ocr

import (
	"image"
	"math"
)

// planTiles 把图片整理成可以送去识别的若干块。
//
// 实测（14 像素的微软雅黑正文）：原样送进识别器几乎认不对，放大到 3 倍最稳；
// 同样的内容整张 10000 像素高送进去，又比切成小块识别差得多。所以先尽量放大到 3 倍，
// 再按「每块放大后不超过 tilePixels 像素」切块，切口尽量落在空白行上，免得把一行字劈成两半。
// 识别器对边长还有硬上限 maxDim（通常 10000），太宽的图只能缩小。
func planTiles(img *image.RGBA, maxDim int) []tile {
	w, h := img.Rect.Dx(), img.Rect.Dy()
	scale := 1.0
	switch {
	case w > maxDim:
		scale = float64(maxDim) / float64(w)
	case w*3 <= maxDim:
		scale = 3
	case w*2 <= maxDim:
		scale = 2
	}
	if short := float64(min(w, h)) * scale; short < minSide {
		scale *= minSide / short
	}
	if scale*float64(w) > float64(maxDim) {
		scale = float64(maxDim) / float64(w)
	}

	// 原图上每块的最大高度：既不超过识别器的边长上限，也不超过像素预算。
	maxRows := int(float64(maxDim) / scale)
	maxRows = min(maxRows, int(float64(tilePixels)/(float64(w)*scale*scale)))
	maxRows = max(maxRows, minTileRows)
	var tiles []tile
	for y := 0; y < h; {
		end := min(h, y+maxRows)
		if end < h {
			end = blankRowNear(img, end, max(y+maxRows/2, end-400))
		}
		sub := subImage(img, image.Rect(0, y, w, end))
		if scale != 1 {
			sub = resize(sub, scale)
		}
		tiles = append(tiles, tile{img: sub, offsetY: y, scale: scale})
		y = end
	}
	return tiles
}

// blankRowNear 从第 end 行往上找一行纯色（空白）的行作为切口，找到 floor 还没有就直接在 end 切。
func blankRowNear(img *image.RGBA, end, floor int) int {
	for y := end; y > floor; y-- {
		if rowIsPlain(img, y-1) {
			return y
		}
	}
	return end
}

// rowIsPlain 判断某一行是否所有像素颜色相近。
func rowIsPlain(img *image.RGBA, y int) bool {
	w := img.Rect.Dx()
	row := img.Pix[img.PixOffset(img.Rect.Min.X, img.Rect.Min.Y+y):]
	r0, g0, b0 := int(row[0]), int(row[1]), int(row[2])
	for x := 1; x < w; x++ {
		i := x * 4
		if absDiff(int(row[i]), r0) > 8 || absDiff(int(row[i+1]), g0) > 8 || absDiff(int(row[i+2]), b0) > 8 {
			return false
		}
	}
	return true
}

// absDiff 返回两数之差的绝对值。
func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}

// subImage 复制出图片的一部分，坐标相对原图左上角。
func subImage(img *image.RGBA, r image.Rectangle) *image.RGBA {
	out := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		src := img.Pix[img.PixOffset(img.Rect.Min.X+r.Min.X, img.Rect.Min.Y+r.Min.Y+y):]
		copy(out.Pix[y*out.Stride:y*out.Stride+r.Dx()*4], src)
	}
	return out
}

// resize 按比例缩放图片（双线性插值）。
func resize(img *image.RGBA, scale float64) *image.RGBA {
	sw, sh := img.Rect.Dx(), img.Rect.Dy()
	dw := max(1, int(math.Round(float64(sw)*scale)))
	dh := max(1, int(math.Round(float64(sh)*scale)))
	out := image.NewRGBA(image.Rect(0, 0, dw, dh))
	for y := 0; y < dh; y++ {
		fy := (float64(y)+0.5)/scale - 0.5
		y0 := clampInt(int(math.Floor(fy)), 0, sh-1)
		y1 := clampInt(y0+1, 0, sh-1)
		ty := fy - math.Floor(fy)
		for x := 0; x < dw; x++ {
			fx := (float64(x)+0.5)/scale - 0.5
			x0 := clampInt(int(math.Floor(fx)), 0, sw-1)
			x1 := clampInt(x0+1, 0, sw-1)
			tx := fx - math.Floor(fx)
			for c := 0; c < 4; c++ {
				p00 := float64(img.Pix[y0*img.Stride+x0*4+c])
				p01 := float64(img.Pix[y0*img.Stride+x1*4+c])
				p10 := float64(img.Pix[y1*img.Stride+x0*4+c])
				p11 := float64(img.Pix[y1*img.Stride+x1*4+c])
				v := (p00*(1-tx)+p01*tx)*(1-ty) + (p10*(1-tx)+p11*tx)*ty
				out.Pix[y*out.Stride+x*4+c] = uint8(math.Round(v))
			}
		}
	}
	return out
}

// clampInt 把 v 限制在 [lo, hi]。
func clampInt(v, lo, hi int) int {
	return max(lo, min(v, hi))
}
