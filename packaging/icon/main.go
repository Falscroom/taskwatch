//go:build ignore

// Генератор иконки приложения: рисует icon.png 1024×1024 (make icon собирает
// из него packaging/taskwatch.icns). Бирюзовый скруглённый квадрат по сетке
// иконок macOS и список: два пункта с галочкой, третий ещё ждёт.
package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
)

const (
	size = 1024
	ss   = 4 // субпикселей на сторону: сглаживание без сторонних библиотек
)

type rgba struct{ r, g, b, a float64 }

var (
	top    = rgba{0x2c, 0xc3, 0x95, 1}
	bottom = rgba{0x10, 0x86, 0x63, 1}
	ink    = rgba{0xf2, 0xfb, 0xf7, 1}
	dim    = rgba{0xf2, 0xfb, 0xf7, 0.55}
)

// body — сквиркл 824×824 по центру, как у системных иконок.
func body(x, y float64) bool {
	const a = 412.0
	dx, dy := math.Abs(x-512)/a, math.Abs(y-512)/a
	return math.Pow(dx, 5)+math.Pow(dy, 5) <= 1
}

// segDist — расстояние от точки до отрезка.
func segDist(px, py, ax, ay, bx, by float64) float64 {
	vx, vy := bx-ax, by-ay
	t := math.Max(0, math.Min(1, ((px-ax)*vx+(py-ay)*vy)/(vx*vx+vy*vy)))
	return math.Hypot(px-(ax+t*vx), py-(ay+t*vy))
}

var rows = []float64{350, 512, 674}

// glyph — цвет списка в точке или ok=false, если там фон.
func glyph(x, y float64) (rgba, bool) {
	for i, cy := range rows {
		c := ink
		if i == len(rows)-1 {
			c = dim
		}
		// Строка списка — полоса со скруглёнными концами.
		if segDist(x, y, 452, cy, 724, cy) <= 30 {
			return c, true
		}
		if i < len(rows)-1 {
			// Галочка.
			if segDist(x, y, 282, cy+4, 318, cy+40) <= 24 || segDist(x, y, 318, cy+40, 384, cy-36) <= 24 {
				return c, true
			}
		} else if d := math.Hypot(x-330, y-cy); d <= 38 && d >= 22 {
			// Пустой кружок — пункт ещё не сделан.
			return c, true
		}
	}
	return rgba{}, false
}

func sample(x, y float64) rgba {
	if !body(x, y) {
		return rgba{}
	}
	t := (y - 100) / 824
	bg := rgba{top.r + (bottom.r-top.r)*t, top.g + (bottom.g-top.g)*t, top.b + (bottom.b-top.b)*t, 1}
	if c, ok := glyph(x, y); ok {
		return rgba{bg.r + (c.r-bg.r)*c.a, bg.g + (c.g-bg.g)*c.a, bg.b + (c.b-bg.b)*c.a, 1}
	}
	return bg
}

func main() {
	out := flag.String("o", "icon.png", "куда записать PNG")
	flag.Parse()

	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					c := sample(float64(px)+(float64(sx)+0.5)/ss, float64(py)+(float64(sy)+0.5)/ss)
					r, g, b, a = r+c.r*c.a, g+c.g*c.a, b+c.b*c.a, a+c.a
				}
			}
			if a == 0 {
				continue
			}
			img.SetNRGBA(px, py, color.NRGBA{uint8(r / a), uint8(g / a), uint8(b / a), uint8(255 * a / (ss * ss))})
		}
	}
	f, err := os.Create(*out)
	if err != nil {
		log.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}
}
