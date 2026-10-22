// Command desktop-assets resizes the owner-provided app icon for portable bundles.
package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"

	"golang.org/x/image/draw"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) != 3 {
		return fmt.Errorf("usage: desktop-assets source.png output-directory")
	}
	input, err := os.Open(os.Args[1])
	if err != nil {
		return err
	}
	source, err := png.Decode(input)
	_ = input.Close()
	if err != nil {
		return err
	}
	target := image.NewNRGBA(image.Rect(0, 0, 256, 256))
	draw.CatmullRom.Scale(target, target.Bounds(), source, source.Bounds(), draw.Src, nil)
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, target); err != nil {
		return err
	}
	if err := os.MkdirAll(os.Args[2], 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(os.Args[2], "sakamoto.png"), buffer.Bytes(), 0o644); err != nil {
		return err
	}
	// ICO can carry a PNG frame on supported Windows versions.
	var icon bytes.Buffer
	for _, v := range []uint16{0, 1, 1} {
		if err := binary.Write(&icon, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	icon.Write([]byte{0, 0, 0, 0})
	for _, v := range []uint16{1, 32} {
		if err := binary.Write(&icon, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	for _, v := range []uint32{uint32(buffer.Len()), 22} {
		if err := binary.Write(&icon, binary.LittleEndian, v); err != nil {
			return err
		}
	}
	icon.Write(buffer.Bytes())
	return os.WriteFile(filepath.Join(os.Args[2], "sakamoto.ico"), icon.Bytes(), 0o644)
}
