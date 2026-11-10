package svgicon

import (
	"context"
	"testing"
)

func TestMultilinePathSurvivesWorkerRevalidation(t *testing.T) {
	source := svg("<path d=\"M10 10\n L90 10\t L50 90 Z\" fill=\"red\"/>")
	if _, err := Normalize(context.Background(), []byte(source)); err != nil {
		t.Fatal(err)
	}
}
func FuzzValidate(f *testing.F) {
	for _, s := range []string{logo, svg(`<rect width="10" height="10"/>`), svg(`<path d="M0 0 L20 20 Z"/>`), svg(`<title>logo</title><circle r="20"/>`), `<svg/>`, `<?xml version="1.0"?>` + logo} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, input []byte) {
		clean, w, h, err := validate(input)
		if err != nil {
			return
		}
		_, ww, hh, err := validate(clean)
		if err != nil || w != ww || h != hh {
			t.Fatalf("canonical validation changed: %v %q", err, clean)
		}
	})
}
