// Package svgicon converts a deliberately small static SVG subset into PNG.
// No original SVG is served. The renderer uses only ReadIconStream, never the
// filename API; accepted content has no resource-loading constructs. Reexec is
// a hard time boundary, NOT an operating-system filesystem/network sandbox.
package svgicon

import (
	"bytes"
	"fmt"
	"image"
	"image/png"
	"io"
	"math"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

const WorkerFlag = "--render-icon-svg"
const MaxBytes = 2 << 20
const MaxPNGBytes = 2 << 20

// RunWorker handles only bounded stdin bytes and writes a single static PNG.
// Applications must dispatch WorkerFlag before any application initialization.
func RunWorker(in io.Reader, out io.Writer) (err error) {
	defer func() {
		if recover() != nil {
			err = fmt.Errorf("SVG renderer failed")
		}
	}()
	data, err := io.ReadAll(io.LimitReader(in, MaxBytes+1))
	if err != nil {
		return err
	}
	clean, w, h, err := validate(data)
	if err != nil {
		return err
	}
	icon, err := oksvg.ReadIconStream(bytes.NewReader(clean), oksvg.StrictErrorMode)
	if err != nil {
		return err
	}
	scale := math.Min(1, 512/math.Max(w, h))
	width := max(1, int(math.Round(w*scale)))
	height := max(1, int(math.Round(h*scale)))
	im := image.NewRGBA(image.Rect(0, 0, width, height))
	// rasterx post-multiplies: scale the viewBox-origin translation too.
	// oksvg.SetTarget translates before scaling, leaving nonzero origins wrong.
	icon.Transform = rasterx.Identity.Scale(float64(width)/w, float64(height)/h).
		Translate(-icon.ViewBox.X, -icon.ViewBox.Y)
	scanner := rasterx.NewScannerGV(width, height, im, im.Bounds())
	icon.Draw(rasterx.NewDasher(width, height, scanner), 1)
	return png.Encode(out, im)
}
