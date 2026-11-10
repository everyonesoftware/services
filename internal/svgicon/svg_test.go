package svgicon

import (
	"bytes"
	"image/png"
	"testing"
)

const logo = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 50"><defs><linearGradient id="paint"><stop offset="0" stop-color="#ff0000"/><stop offset="1" stop-color="#0000ff"/></linearGradient></defs><rect width="100" height="50" fill="url(#paint)"/></svg>`

func TestStaticGradientPNG(t *testing.T) {
	var out bytes.Buffer
	if err := RunWorker(bytes.NewBufferString(logo), &out); err != nil {
		t.Fatal(err)
	}
	im, err := png.Decode(&out)
	if err != nil {
		t.Fatal(err)
	}
	if im.Bounds().Dx() != 100 || im.Bounds().Dy() != 50 {
		t.Fatal(im.Bounds())
	}
	r, _, b, a := im.At(10, 25).RGBA()
	if a == 0 || r <= b {
		t.Fatalf("left gradient %d %d %d", r, b, a)
	}
	r, _, b, a = im.At(90, 25).RGBA()
	if a == 0 || b <= r {
		t.Fatalf("right gradient %d %d %d", r, b, a)
	}
}
