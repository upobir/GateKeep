package icon

import (
	"bytes"
	_ "embed"
	"image"
	"image/png"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

//go:embed icon.svg
var SVG []byte

func PNG(size int) ([]byte, error) {
	svg, err := oksvg.ReadIconStream(bytes.NewReader(SVG))
	if err != nil {
		return nil, err
	}
	svg.SetTarget(0, 0, float64(size), float64(size))

	img := image.NewRGBA(image.Rect(0, 0, size, size))
	scanner := rasterx.NewScannerGV(size, size, img, img.Bounds())
	svg.Draw(rasterx.NewDasher(size, size, scanner), 1)

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
