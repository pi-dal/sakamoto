package menubar

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
	"runtime"
)

// trayIcon is an authored power-switch glyph, rasterized at 2x into a
// monochrome template PNG. Its source and provenance are this function;
// no generated or third-party raster asset is embedded in the application.
func trayIcon() []byte {
	const size = 36
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	ink := color.NRGBA{A: 255}
	if runtime.GOOS != "darwin" {
		ink = color.NRGBA{R: 37, G: 99, B: 235, A: 255}
	}
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			px, py := float64(x)-17.5, float64(y)-18
			r := math.Hypot(px, py)
			ring := r >= 10 && r <= 13 && !(py < -6 && math.Abs(px) < 6)
			stem := math.Abs(px) <= 1.5 && py >= -15 && py <= -1
			if ring || stem {
				img.SetNRGBA(x, y, ink)
			}
		}
	}
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		panic(err)
	}
	return out.Bytes()
}
