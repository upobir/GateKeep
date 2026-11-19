package icon

import (
	"bytes"
	"image/png"
	"testing"
)

func TestPNGUsesSharedIcon(t *testing.T) {
	data, err := PNG(32)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() != 32 || img.Bounds().Dy() != 32 {
		t.Fatalf("size = %v", img.Bounds())
	}

	var black, white int
	bounds := img.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a == 0 {
				continue
			}
			if r < 0x2000 && g < 0x2000 && b < 0x2000 {
				black++
			}
			if r > 0xe000 && g > 0xe000 && b > 0xe000 {
				white++
			}
		}
	}
	if black == 0 || white == 0 {
		t.Fatalf("black=%d white=%d", black, white)
	}
}
