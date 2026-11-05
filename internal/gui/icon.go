package gui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// trayIcon рисует template-иконку 36×36 (18pt @2x): глиф merge request — два узла и ветка.
// macOS сам перекрашивает её под светлое/тёмное меню, важен только альфа-канал.
func trayIcon() []byte {
	const size = 36
	img := image.NewNRGBA(image.Rect(0, 0, size, size))

	// Расстояние от точки до отрезка — для линий; кольца — по расстоянию до центра.
	segment := func(px, py, ax, ay, bx, by float64) float64 {
		dx, dy := bx-ax, by-ay
		t := math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/(dx*dx+dy*dy)))
		return math.Hypot(px-(ax+t*dx), py-(ay+t*dy))
	}
	ring := func(px, py, cx, cy, r float64) float64 {
		return math.Abs(math.Hypot(px-cx, py-cy) - r)
	}

	const stroke = 1.6
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)+0.5, float64(y)+0.5
			d := math.Min(
				math.Min(ring(px, py, 10, 8, 4), ring(px, py, 10, 28, 4)),
				math.Min(ring(px, py, 26, 28, 4), segment(px, py, 10, 12, 10, 24)),
			)
			// ветка справа: вверх от нижнего правого узла и влево к верхнему
			d = math.Min(d, segment(px, py, 26, 24, 26, 14))
			d = math.Min(d, segment(px, py, 26, 14, 20, 8))
			d = math.Min(d, segment(px, py, 20, 8, 14, 8))

			if a := stroke + 0.5 - d; a > 0 {
				img.SetNRGBA(x, y, color.NRGBA{A: uint8(255 * math.Min(1, a))})
			}
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
