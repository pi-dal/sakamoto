// appicongen resizes the owner-provided assets/app-icon.png into native
// launcher resources. Run via mise run icons; no external renderer is needed.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
)

const source = "assets/app-icon.png"

func main() {
	if err := generate(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func generate() error {
	data, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	master, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	square := func(size int) image.Image {
		im := image.NewRGBA(image.Rect(0, 0, size, size))
		resizeInto(im, master, im.Bounds())
		return im
	}
	assets := "ios/App/Assets.xcassets"
	if err := writeJSON(filepath.Join(assets, "Contents.json"), map[string]any{
		"info": map[string]any{"author": "xcode", "version": 1},
	}); err != nil {
		return err
	}
	type iconSlot struct {
		Idiom    string `json:"idiom"`
		Size     string `json:"size"`
		Scale    string `json:"scale"`
		Filename string `json:"filename"`
	}
	var slots []iconSlot
	for _, spec := range []struct {
		idiom  string
		points []float64
		scales []int
	}{
		{"iphone", []float64{20, 29, 40, 60}, []int{2, 3}},
		{"ipad", []float64{20, 29, 40, 76}, []int{1, 2}},
		{"ipad", []float64{83.5}, []int{2}},
		{"ios-marketing", []float64{1024}, []int{1}},
	} {
		for _, points := range spec.points {
			for _, scale := range spec.scales {
				pixels := int(points * float64(scale))
				name := fmt.Sprintf("icon-%d.png", pixels)
				if err := writePNG(filepath.Join(assets, "AppIcon.appiconset", name), square(pixels)); err != nil {
					return err
				}
				slots = append(slots, iconSlot{spec.idiom, fmt.Sprintf("%gx%g", points, points), fmt.Sprintf("%dx", scale), name})
			}
		}
	}
	if err := writeJSON(filepath.Join(assets, "AppIcon.appiconset/Contents.json"), map[string]any{
		"images": slots, "info": map[string]any{"author": "xcode", "version": 1},
	}); err != nil {
		return err
	}
	// Preserve the supplied composition inside the adaptive mask's safe area.
	// A luminance mask of the same artwork supplies Android themed icons.
	mono := image.NewNRGBA(master.Bounds())
	bounds := master.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, _ := master.At(x, y).RGBA()
			luma := (299*r + 587*g + 114*b) / 1000 / 257
			// Exclude the outer tile rim from the monochrome glyph.
			if x > bounds.Dx()/6 && x < bounds.Dx()*5/6 && y > bounds.Dy()/8 && y < bounds.Dy()*7/8 && luma > 140 {
				mono.SetNRGBA(x, y, color.NRGBA{A: 255})
			}
		}
	}
	res := "android/app/src/main/res"
	region := image.Rect(60, 60, 372, 372)
	for name, src := range map[string]image.Image{"ic_launcher_foreground": master, "ic_launcher_monochrome": mono} {
		im := image.NewNRGBA(image.Rect(0, 0, 432, 432))
		resizeInto(im, src, region)
		if err := writePNG(filepath.Join(res, "drawable-nodpi", name+".png"), im); err != nil {
			return err
		}
	}
	for _, density := range []struct {
		name string
		size int
	}{{"mdpi", 48}, {"hdpi", 72}, {"xhdpi", 96}, {"xxhdpi", 144}, {"xxxhdpi", 192}} {
		im := square(density.size)
		if err := writePNG(filepath.Join(res, "mipmap-"+density.name, "ic_launcher.png"), im); err != nil {
			return err
		}
		round := image.NewNRGBA(im.Bounds())
		radius := float64(density.size) / 2
		for y := 0; y < density.size; y++ {
			for x := 0; x < density.size; x++ {
				dx, dy := float64(x)+0.5-radius, float64(y)+0.5-radius
				if dx*dx+dy*dy <= radius*radius {
					round.Set(x, y, im.At(x, y))
				}
			}
		}
		if err := writePNG(filepath.Join(res, "mipmap-"+density.name, "ic_launcher_round.png"), round); err != nil {
			return err
		}
	}
	fmt.Println("Generated iOS AppIcon and Android adaptive, themed, and legacy launcher PNGs.")
	return nil
}

func resizeInto(dst interface{ Set(int, int, color.Color) }, src image.Image, region image.Rectangle) {
	// Average source pixels for smooth, antialiased small launcher exports.
	bounds := src.Bounds()
	for y := region.Min.Y; y < region.Max.Y; y++ {
		for x := region.Min.X; x < region.Max.X; x++ {
			x0 := (x - region.Min.X) * bounds.Dx() / region.Dx()
			x1 := (x - region.Min.X + 1) * bounds.Dx() / region.Dx()
			y0 := (y - region.Min.Y) * bounds.Dy() / region.Dy()
			y1 := (y - region.Min.Y + 1) * bounds.Dy() / region.Dy()
			if x1 <= x0 {
				x1 = x0 + 1
			}
			if y1 <= y0 {
				y1 = y0 + 1
			}
			var r, g, b, a, n uint64
			for py := y0; py < y1; py++ {
				for px := x0; px < x1; px++ {
					rr, gg, bb, aa := src.At(px, py).RGBA()
					r += uint64(rr)
					g += uint64(gg)
					b += uint64(bb)
					a += uint64(aa)
					n++
				}
			}
			dst.Set(x, y, color.RGBA64{R: uint16(r / n), G: uint16(g / n), B: uint16(b / n), A: uint16(a / n)})
		}
	}
}

func writePNG(path string, im image.Image) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, im); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func writeJSON(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o644)
}
