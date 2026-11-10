package svgicon

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/png"
	"strings"
	"testing"
)

func fidelityImage(t *testing.T, source string) image.Image {
	t.Helper()
	var out bytes.Buffer
	if err := RunWorker(strings.NewReader(source), &out); err != nil {
		t.Fatal(err)
	}
	im, err := png.Decode(&out)
	if err != nil {
		t.Fatal(err)
	}
	return im
}

func requireRed(t *testing.T, im image.Image, x, y int) {
	t.Helper()
	r, g, b, a := im.At(x, y).RGBA()
	if r != 65535 || g != 0 || b != 0 || a != 65535 {
		t.Fatalf("pixel (%d,%d): want opaque red, got %d,%d,%d,%d", x, y, r, g, b, a)
	}
}

func TestFidelityTwoPointPolyline(t *testing.T) {
	source := svg(`<polyline points="10,50 90,50" fill="none" stroke="red" stroke-width="4"/>`)
	im := fidelityImage(t, source)
	requireRed(t, im, 50, 50)
	if _, _, _, a := im.At(50, 40).RGBA(); a != 0 {
		t.Fatal("paint outside polyline")
	}
	requireCanonicalIdempotence(t, source)
}

func requireCanonicalIdempotence(t *testing.T, source string) {
	t.Helper()
	clean, _, _, err := validate([]byte(source))
	if err != nil {
		t.Fatal(err)
	}
	again, _, _, err := validate(clean)
	if err != nil || !bytes.Equal(clean, again) {
		t.Fatalf("canonical validation not idempotent: %s -> %s (%v)", clean, again, err)
	}
}

func TestFidelityRectRadii(t *testing.T) {
	for _, tc := range []struct{ name, attrs, want string }{
		{"rx only", `width="100" height="100" rx="40"`, `width="100" height="100" rx="40" ry="40"`},
		{"ry only", `width="100" height="100" ry="40"`, `width="100" height="100" rx="40" ry="40"`},
		{"oversized rx", `width="80" height="40" rx="100"`, `width="80" height="40" rx="40" ry="20"`},
		{"oversized ry", `width="80" height="40" ry="100"`, `width="80" height="40" rx="40" ry="20"`},
		{"oversized both px dimensions", `width="80px" height="40px" rx="90" ry="70"`, `width="80" height="40" rx="40" ry="20"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := svg(`<rect ` + tc.attrs + ` fill="red"/>`)
			im := fidelityImage(t, source)
			if _, _, _, a := im.At(1, 1).RGBA(); a != 0 {
				t.Error("rounded corner must be transparent")
			}
			requireRed(t, im, 40, 20)
			want := fidelityImage(t, svg(`<rect `+tc.want+` fill="red"/>`))
			for y := 0; y < 100; y++ {
				for x := 0; x < 100; x++ {
					if im.At(x, y) != want.At(x, y) {
						t.Fatalf("radius normalization differs at (%d,%d)", x, y)
					}
				}
			}
			requireCanonicalIdempotence(t, source)
		})
	}
}

func TestFidelityOpacitySubset(t *testing.T) {
	overlapping := `<rect width="70" height="100" fill="red"/><rect x="30" width="70" height="100" fill="red"/>`
	for name, source := range map[string]string{
		"group compositing":         svg(`<g opacity=".5">` + overlapping + `</g>`),
		"root compositing":          strings.Replace(svg(overlapping), `viewBox=`, `opacity=".5" viewBox=`, 1),
		"inherited fill override":   svg(`<g fill-opacity=".5"><g><rect width="100" height="100" fill="red" fill-opacity=".5"/></g></g>`),
		"inherited stroke override": svg(`<g stroke-opacity=".5"><line x2="100" y2="100" stroke="red" stroke-opacity=".5"/></g>`),
		"root fill opacity":         strings.Replace(svg(overlapping), `viewBox=`, `fill-opacity=".5" viewBox=`, 1),
		"leaf general opacity":      svg(`<rect width="100" height="100" fill="red" stroke="blue" stroke-width="10" opacity=".5"/>`),
		"zero general opacity":      svg(`<rect width="100" height="100" opacity="0"/>`),
		"container fill noop":       svg(`<g fill-opacity="1">` + overlapping + `</g>`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := validate([]byte(source)); !errors.Is(err, invalid) {
				t.Errorf("unsupported opacity accepted: %v", err)
			}
			var out bytes.Buffer
			if err := RunWorker(strings.NewReader(source), &out); !errors.Is(err, invalid) || out.Len() != 0 {
				t.Errorf("worker accepted unsupported opacity: %v", err)
			}
			if _, err := Normalize(context.Background(), []byte(source)); !errors.Is(err, invalid) {
				t.Errorf("must reject in caller before render, got %v", err)
			}
		})
	}
}

func TestFidelityLeafPaintOpacity(t *testing.T) {
	for name, source := range map[string]string{
		"fill":   svg(`<g opacity="1"><g><rect width="100" height="100" fill="red" fill-opacity=".5"/></g></g>`),
		"stroke": svg(`<line x1="10" y1="50" x2="90" y2="50" stroke="red" stroke-width="4" stroke-opacity=".5"/>`),
	} {
		t.Run(name, func(t *testing.T) {
			im := fidelityImage(t, source)
			r, g, b, a := im.At(50, 50).RGBA()
			if a < 32500 || a > 33000 || r != a || g != 0 || b != 0 {
				t.Fatalf("want half-alpha red, got %d,%d,%d,%d", r, g, b, a)
			}
			requireCanonicalIdempotence(t, source)
		})
	}
}

func TestFidelityNonzeroViewBoxDownscale(t *testing.T) {
	im := fidelityImage(t, `<svg xmlns="http://www.w3.org/2000/svg" viewBox="2000 0 1000 1000"><rect x="2000" width="1000" height="1000" fill="red"/></svg>`)
	if im.Bounds().Dx() != 512 || im.Bounds().Dy() != 512 {
		t.Fatal(im.Bounds())
	}
	requireRed(t, im, 256, 256)
}
