package menubar

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
)

// readableSnapshot gives transparent content an explicitly documented neutral
// ground. It does not simulate the system's material, arrow or shadow.
func readableSnapshot(raw []byte, dark bool) ([]byte, error) {
	content, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	theme := radixTheme(dark)
	c := theme.Background
	output := image.NewRGBA(content.Bounds())
	draw.Draw(output, output.Bounds(), image.NewUniform(color.NRGBA{R: c.R, G: c.G, B: c.B, A: 255}), image.Point{}, draw.Src)
	draw.Draw(output, output.Bounds(), content, content.Bounds().Min, draw.Over)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, output); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}
