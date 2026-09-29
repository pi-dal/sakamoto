// portraitgen samples a verified, separately licensed photo into terminal
// assets. It is a build-time tool; runtime rendering never fetches images.
package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"flag"
	"fmt"
	"image"
	_ "image/jpeg"
	"os"
	"strings"
)

const sourceSHA256 = "c51cedfd74096a9503636eea88e2275568f9a8fdde899a7d305007962cf01471"

func main() {
	input := flag.String("input", "", "verified, locally stored CC BY 2.0 source JPEG")
	output := flag.String("output", "", "output portrait file")
	width := flag.Int("width", 46, "terminal cells wide")
	height := flag.Int("height", 16, "terminal rows high")
	format := flag.String("format", "grayhex", "grayhex (half-block pixels) or blocks (monochrome fallback)")
	flag.Parse()
	if *input == "" || *output == "" || *width < 1 || *height < 1 || (*format != "grayhex" && *format != "blocks") {
		fail(fmt.Errorf("usage: portraitgen -input source.jpg -output portrait.gray -width 46 -height 16 -format grayhex|blocks"))
	}
	source, err := os.ReadFile(*input)
	if err != nil {
		fail(err)
	}
	if fmt.Sprintf("%x", sha256.Sum256(source)) != sourceSHA256 {
		fail(fmt.Errorf("source photo digest differs from the verified CC BY 2.0 image"))
	}
	photo, _, err := image.Decode(bytes.NewReader(source))
	if err != nil {
		fail(err)
	}
	bounds := photo.Bounds()
	if bounds.Dx() != 484 || bounds.Dy() != 532 {
		fail(fmt.Errorf("expected 484x532 source, got %s", bounds))
	}
	// Face-focused crop: every format uses the same composition; the double
	// vertical sampling in grayhex matches the two pixels of a half-block cell.
	crop := image.Rect(100, 20, 390, 280)
	out, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		fail(err)
	}
	writer := bufio.NewWriter(out)
	virtualHeight := *height * 2
	pixels := make([][]uint8, virtualHeight)
	for y := 0; y < virtualHeight; y++ {
		pixels[y] = make([]uint8, *width)
		for x := 0; x < *width; x++ {
			total := 0
			for sy := 0; sy < 4; sy++ {
				for sx := 0; sx < 4; sx++ {
					px := crop.Min.X + (4*x+sx)*crop.Dx()/(4*(*width))
					py := crop.Min.Y + (4*y+sy)*crop.Dy()/(4*virtualHeight)
					r, g, b, _ := photo.At(px, py).RGBA()
					total += int((299*r + 587*g + 114*b) / (1000 * 257))
				}
			}
			pixels[y][x] = uint8(total / 16)
		}
	}
	for y := 0; y < *height; y++ {
		var row strings.Builder
		for x := 0; x < *width; x++ {
			top, bottom := pixels[y*2][x], pixels[y*2+1][x]
			if *format == "grayhex" {
				if _, err := fmt.Fprintf(writer, "%02x%02x", top, bottom); err != nil {
					fail(err)
				}
			} else {
				brightness := (int(top) + int(bottom)) / 2
				idx := brightness * 6 / 256
				if idx > 5 {
					idx = 5
				}
				row.WriteRune([]rune("█▓▒░· ")[idx])
			}
		}
		if *format == "blocks" {
			if _, err := fmt.Fprintln(writer, strings.TrimRight(row.String(), " ")); err != nil {
				fail(err)
			}
		} else if _, err := fmt.Fprintln(writer); err != nil {
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
