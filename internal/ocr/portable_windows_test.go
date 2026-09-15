//go:build windows && amd64

package ocr

import (
	"bytes"
	"image"
	_ "image/png"
	"os"
	"testing"

	"wardogs-mortar/internal/coords"
	platform "wardogs-mortar/internal/win32"
)

func TestEncodeOCRImageUpscalesSmallCapture(t *testing.T) {
	encoded, err := encodeOCRImage(&platform.Image{Width: 2, Height: 1, Pixels: []byte{
		0, 0, 255, 255,
		0, 255, 0, 255,
	}})
	if err != nil {
		t.Fatal(err)
	}
	decoded, _, err := image.Decode(bytes.NewReader(encoded))
	if err != nil {
		t.Fatal(err)
	}
	if got := decoded.Bounds().Size(); got.X != 4 || got.Y != 2 {
		t.Fatalf("encoded size=%v, want 4x2", got)
	}
}

func TestParseRapidOCRResponse(t *testing.T) {
	text, parsed, err := parseRapidOCRResponse([]byte(`{"code":100,"data":[{"text":"x123.45"},{"text":"y678.90"}]}`))
	if err != nil || !parsed || text != "x123.45\ny678.90" {
		t.Fatalf("unexpected parse result: text=%q parsed=%v err=%v", text, parsed, err)
	}
}

func TestPortableOCRRecognizesCoordinate(t *testing.T) {
	file, err := os.Open("testdata/coordinate.png")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	decoded, _, err := image.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	bounds := decoded.Bounds()
	input := &platform.Image{Width: bounds.Dx(), Height: bounds.Dy(), Pixels: make([]byte, bounds.Dx()*bounds.Dy()*4)}
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			r, g, b, a := decoded.At(bounds.Min.X+x, bounds.Min.Y+y).RGBA()
			index := (y*bounds.Dx() + x) * 4
			input.Pixels[index] = byte(b >> 8)
			input.Pixels[index+1] = byte(g >> 8)
			input.Pixels[index+2] = byte(r >> 8)
			input.Pixels[index+3] = byte(a >> 8)
		}
	}

	provider, err := newPortableProvider()
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	text, err := provider.Recognize(input)
	if err != nil {
		t.Fatal(err)
	}
	point, ok := coords.ParseCoordinate(text)
	if !ok || point.X != 123.45 || point.Y != 678.90 {
		t.Fatalf("unexpected OCR output %q, parsed=%+v ok=%v", text, point, ok)
	}
}
