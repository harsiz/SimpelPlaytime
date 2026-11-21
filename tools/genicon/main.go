// Command genicon draws the Simpel Playtime logo and writes it as PNG files.
// Run it from the repo root: go run ./tools/genicon
package main

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"log"
	"math"
	"os"
	"path/filepath"
)

// sdf helpers (all in 256x256 logo space)

func roundedRect(x, y, cx, cy, hw, hh, r float64) float64 {
	qx := math.Abs(x-cx) - hw + r
	qy := math.Abs(y-cy) - hh + r
	ox, oy := math.Max(qx, 0), math.Max(qy, 0)
	return math.Hypot(ox, oy) + math.Min(math.Max(qx, qy), 0) - r
}

func inTriangle(px, py, ax, ay, bx, by, cx, cy float64) bool {
	sign := func(x1, y1, x2, y2, x3, y3 float64) float64 {
		return (x1-x3)*(y2-y3) - (x2-x3)*(y1-y3)
	}
	d1 := sign(px, py, ax, ay, bx, by)
	d2 := sign(px, py, bx, by, cx, cy)
	d3 := sign(px, py, cx, cy, ax, ay)
	neg := d1 < 0 || d2 < 0 || d3 < 0
	pos := d1 > 0 || d2 > 0 || d3 > 0
	return !(neg && pos)
}

var (
	top    = color.NRGBA{0x9b, 0x63, 0xff, 0xff}
	bottom = color.NRGBA{0x6f, 0x3c, 0xd4, 0xff}
	white  = color.NRGBA{0xff, 0xff, 0xff, 0xff}
)

func sample(x, y float64) color.NRGBA {
	if roundedRect(x, y, 128, 128, 120, 120, 56) > 0 {
		return color.NRGBA{}
	}
	// soft vertical blend between the two purples
	t := math.Min(math.Max((y-40)/180, 0), 1)
	c := color.NRGBA{
		R: uint8(float64(top.R)*(1-t) + float64(bottom.R)*t),
		G: uint8(float64(top.G)*(1-t) + float64(bottom.G)*t),
		B: uint8(float64(top.B)*(1-t) + float64(bottom.B)*t),
		A: 0xff,
	}
	if math.Abs(math.Hypot(x-128, y-138)-74) <= 9 {
		return white
	}
	if roundedRect(x, y, 128, 46, 9, 16, 8) <= 0 {
		return white
	}
	if inTriangle(x, y, 110, 100, 170, 138, 110, 176) {
		return white
	}
	return c
}

func draw(size int) *image.NRGBA {
	const ss = 4
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	scale := 256.0 / float64(size)
	for py := 0; py < size; py++ {
		for px := 0; px < size; px++ {
			var r, g, b, a float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					x := (float64(px) + (float64(sx)+0.5)/ss) * scale
					y := (float64(py) + (float64(sy)+0.5)/ss) * scale
					c := sample(x, y)
					fa := float64(c.A) / 255
					r += float64(c.R) * fa
					g += float64(c.G) * fa
					b += float64(c.B) * fa
					a += fa
				}
			}
			n := float64(ss * ss)
			if a > 0 {
				img.SetNRGBA(px, py, color.NRGBA{R: uint8(r / a), G: uint8(g / a), B: uint8(b / a), A: uint8(a / n * 255)})
			}
		}
	}
	return img
}

func write(path string, img image.Image) {
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		log.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		log.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
	log.Printf("wrote %s", path)
}

func main() {
	big := draw(256)
	write("internal/ui/logo.png", big)
	write("assets/icon.png", big)
}
