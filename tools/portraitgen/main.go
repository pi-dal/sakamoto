// portraitgen makes a deterministic, terminal-safe block portrait from a
// separately licensed local photo. It is a build-time tool, not a runtime dep.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
)

func main() {
	input := flag.String("input", "", "local CC-licensed source JPEG")
	output := flag.String("output", "", "output UTF-8 portrait")
	width := flag.Int("width", 38, "terminal cells wide")
	height := flag.Int("height", 21, "terminal rows high")
	flag.Parse()
	if *input == "" || *output == "" || *width < 1 || *height < 1 {
		fmt.Fprintln(os.Stderr, "usage: portraitgen -input portrait.jpg -output portrait.txt -width 38 -height 21")
		os.Exit(2)
	}
	in, err := os.Open(*input)
	if err != nil {
		fail(err)
	}
	defer func() { _ = in.Close() }()
	photo, _, err := image.Decode(in)
	if err != nil {
		fail(err)
	}
	// Fixed close portrait crop from the 484x532 Joi Ito / Solid State Survivor
	// image (source and modification details in docs/portrait-license.md).
	bounds := photo.Bounds()
	if bounds.Dx() != 484 || bounds.Dy() != 532 {
		fail(fmt.Errorf("expected verified 484x532 source, got %s", bounds))
	}
	crop := image.Rect(85, 0, 399, 350)
	out, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		fail(err)
	}
	writer := bufio.NewWriter(out)
	shades := []rune("█▓▒░· ") // darkest to lightest; readable without colour or graphics protocols
	for y := 0; y < *height; y++ {
		for x := 0; x < *width; x++ {
			// Supersampling avoids a single scanline deciding a whole terminal cell.
			total := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					px := crop.Min.X + (4*x+sx)*crop.Dx()/(4*(*width))
					py := crop.Min.Y + (4*y+sy)*crop.Dy()/(4*(*height))
					r, g, b, _ := photo.At(px, py).RGBA()
					total += int((299*r + 587*g + 114*b) / (1000 * 257))
				}
			}
			brightness := (total/16 - 20) * 6 / 5
			if brightness < 0 {
				brightness = 0
			}
			if brightness > 255 {
				brightness = 255
			}
			idx := brightness * len(shades) / 256
			if idx >= len(shades) {
				idx = len(shades) - 1
			}
			if _, err := fmt.Fprint(writer, string(shades[idx])); err != nil {
				fail(err)
			}
		}
		if _, err := fmt.Fprintln(writer); err != nil {
			fail(err)
		}
	}
	if err := writer.Flush(); err != nil {
		fail(err)
	}
	if err := out.Close(); err != nil {
		fail(err)
	}
}
func fail(err error) { fmt.Fprintln(os.Stderr, err); os.Exit(1) }
