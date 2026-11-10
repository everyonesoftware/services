package svgicon

import (
	"bytes"
	"image/png"
	"strings"
	"testing"
)

func svg(body string) string {
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100">` + body + `</svg>`
}
func TestRejectUnsafeXMLAndSVG(t *testing.T) {
	cases := map[string]string{
		"doctype":                        `<!DOCTYPE svg [<!ENTITY x SYSTEM "file:///etc/passwd">]>` + svg(`<rect width="10" height="10"/>`),
		"entity":                         svg(`<title>&amp;</title><rect/>`),
		"mismatched declaration quotes":  `<?xml version="1.0'?>` + svg(`<rect/>`),
		"declaration after comment":      `<!-- before --><?xml version="1.0"?>` + svg(`<rect/>`),
		"evenodd unsupported by scanner": svg(`<path d="M0 0 H100 V100 H0 Z M20 20 H80 V80 H20 Z" fill-rule="evenodd"/>`), "pi": `<?target data?>` + svg(`<rect/>`),
		"malformed": svg(`<g><rect/></svg>`), "two roots": svg(`<rect/>`) + svg(`<rect/>`), "text outside": "no" + svg(`<rect/>`),
		"namespace prefix":  `<s:svg xmlns:s="http://www.w3.org/2000/svg" viewBox="0 0 10 10"><s:rect/></s:svg>`,
		"namespace rebind":  svg(`<g xmlns="http://www.w3.org/1999/xhtml"><rect/></g>`),
		"unknown namespace": svg(`<rect xmlns:x="urn:test"/>`), "qualified attr": svg(`<rect x:fill="red"/>`),
		"duplicate attr": svg(`<rect width="1" width="2"/>`), "nested svg": svg(`<svg><rect/></svg>`),
		"unknown attr": svg(`<rect magic="1"/>`), "event": svg(`<rect onclick="alert(1)"/>`), "style attr": svg(`<rect style="fill:red"/>`),
		"external fill": svg(`<rect fill="url(https://example.com/x)"/>`), "file fill": svg(`<rect fill="url(file:///secret)"/>`),
		"missing gradient": svg(`<rect fill="url(#a)"/>`), "href": svg(`<rect href="#a"/>`),
		"gradient cycle":        svg(`<defs><linearGradient id="a" href="#b"/><linearGradient id="b" href="#a"/></defs><rect fill="url(#a)"/>`),
		"gradient wrong target": svg(`<g id="a"/><rect fill="url(#a)"/>`), "duplicate id": svg(`<rect id="x"/><rect id="x"/>`),
		"empty color": svg(`<rect fill=""/>`), "hsl color": svg(`<rect fill="hsl(0,1,1)"/>`), "unsafe color": svg(`<rect fill="rgb()"/>`),
		"nan": svg(`<circle r="NaN"/>`), "inf": svg(`<circle r="1e999"/>`), "magnitude": svg(`<circle cx="99999"/>`),
		"huge transform":     svg(`<g transform="scale(16)"><g transform="scale(16)"><rect/></g></g>`),
		"singular transform": svg(`<rect transform="scale(0)"/>`), "too many transforms": svg(`<rect transform="` + strings.Repeat("scale(1) ", 9) + `"/>`),
		"too deep":        svg(strings.Repeat(`<g>`, 16) + `<rect/>` + strings.Repeat(`</g>`, 16)),
		"too many nodes":  svg(strings.Repeat(`<rect/>`, 513)),
		"path length":     svg(`<path d="M0 0 ` + strings.Repeat("L1 1 ", 14000) + `"/>`),
		"path numbers":    svg(`<path d="M0 0 ` + strings.Repeat("L0 0 ", 3000) + `"/>`),
		"large bytes":     strings.Repeat(" ", MaxBytes+1),
		"too wide":        strings.Replace(svg(`<rect/>`), "100 100", "4097 1", 1),
		"too many pixels": strings.Replace(svg(`<rect/>`), "100 100", "2001 2000", 1),
		"physical pixels": `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" width="4096" height="4096"><rect/></svg>`,
	}
	for _, name := range []string{"script", "foreignObject", "html", "image", "text", "use", "animate", "animateTransform", "set", "filter", "style", "unknown"} {
		cases[name] = svg(`<` + name + `/>`)
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, _, err := validate([]byte(input)); err == nil {
				t.Fatal("unsafe SVG accepted")
			}
		})
	}
}
func TestActualShapesScaleAndOutputCap(t *testing.T) {
	inputs := []struct {
		name, source string
		w, h         int
		x, y         int
	}{
		{"scale", svg(`<g transform="scale(2)"><rect x="10" y="10" width="20" height="20" fill="#00ff00"/></g>`), 100, 100, 50, 50},
		{"path", svg(`<path d="M10 10 L90 10 L50 90 Z" fill="red"/>`), 100, 100, 50, 40},
		{"circle", svg(`<circle cx="50" cy="50" r="20" fill="blue"/>`), 100, 100, 50, 50},
		{"ellipse", svg(`<ellipse cx="50" cy="50" rx="30" ry="15" fill="blue"/>`), 100, 100, 50, 50},
		{"line", svg(`<line x1="10" y1="50" x2="90" y2="50" stroke="red" stroke-width="4"/>`), 100, 100, 50, 50},
		{"polygon", svg(`<polygon points="10,10 90,10 50,90" fill="green"/>`), 100, 100, 50, 40},
		{"polyline", svg(`<polyline points="10,50 50,50 90,50" fill="none" stroke="blue" stroke-width="4"/>`), 100, 100, 50, 50},
		{"translate-matrix", svg(`<g transform="translate(20,10)"><rect width="20" height="20" transform="matrix(1,0,0,1,10,20)" fill="red"/></g>`), 100, 100, 40, 40},
		{"radial", svg(`<defs><radialGradient id="r"><stop offset="0" stop-color="red"/><stop offset="100%" stop-color="blue"/></radialGradient></defs><circle cx="50" cy="50" r="40" fill="url(#r)"/>`), 100, 100, 50, 50},
		{"bounded", `<?xml version="1.0" encoding="UTF-8"?>` + strings.Replace(svg(`<rect width="2000" height="1000" fill="red"/>`), "100 100", "2000 1000", 1), 512, 256, 250, 120},
	}
	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := RunWorker(strings.NewReader(tc.source), &out); err != nil {
				t.Fatal(err)
			}
			im, err := png.Decode(&out)
			if err != nil {
				t.Fatal(err)
			}
			if im.Bounds().Dx() != tc.w || im.Bounds().Dy() != tc.h {
				t.Fatal(im.Bounds())
			}
			_, _, _, a := im.At(tc.x, tc.y).RGBA()
			if a == 0 {
				t.Fatal("expected painted pixel")
			}
			if tc.name == "radial" {
				r, _, b, _ := im.At(50, 50).RGBA()
				rr, _, bb, _ := im.At(85, 50).RGBA()
				if r <= b || rr >= bb {
					t.Fatal("radial gradient not rendered")
				}
			}
		})
	}
}
