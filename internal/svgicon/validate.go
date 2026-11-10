package svgicon

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/srwiley/rasterx"
	"golang.org/x/image/colornames"
)

// These are intentionally tighter than SVG itself. Rejected SVGs are not repaired.
const (
	MaxNodes      = 512
	MaxDepth      = 16
	MaxAttributes = 24
	MaxPathBytes  = 65536
	MaxNumbers    = 8192
	MaxMagnitude  = 8192
)
const svgNS = "http://www.w3.org/2000/svg"

var invalid = errors.New("unsupported or unsafe SVG")
var numberRE = regexp.MustCompile(`^[+-]?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?`)
var idRE = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)
var hexRE = regexp.MustCompile(`^#(?:[0-9a-fA-F]{3}|[0-9a-fA-F]{6})$`)
var declarationRE = regexp.MustCompile(`^version\s*=\s*(?:"1\.0"|'1\.0')(?:\s+encoding\s*=\s*(?:"UTF-8"|'UTF-8'))?(?:\s+standalone\s*=\s*(?:"(?:yes|no)"|'(?:yes|no)'))?\s*$`)
var elementAttrs = map[string]string{
	"svg": "width height viewBox", "g": "", "defs": "", "title": "", "desc": "",
	"path": "d", "rect": "x y width height rx ry", "circle": "cx cy r", "ellipse": "cx cy rx ry",
	"line": "x1 y1 x2 y2", "polyline": "points", "polygon": "points",
	"linearGradient": "x1 y1 x2 y2 gradientUnits", "radialGradient": "cx cy r fx fy gradientUnits", "stop": "offset stop-color stop-opacity",
}

const presentation = "fill stroke stroke-width fill-opacity stroke-opacity opacity fill-rule stroke-linecap stroke-linejoin stroke-miterlimit transform"

func has(list, key string) bool { return strings.Contains(" "+list+" ", " "+key+" ") }
func numbers(s string) ([]float64, error) {
	var result []float64
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimLeft(s, " ,\t\r\n") {
		m := numberRE.FindString(s)
		if m == "" {
			return nil, invalid
		}
		n, err := strconv.ParseFloat(m, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || math.Abs(n) > MaxMagnitude {
			return nil, invalid
		}
		result = append(result, n)
		if len(result) > MaxNumbers {
			return nil, invalid
		}
		s = s[len(m):]
	}
	if len(result) == 0 {
		return nil, invalid
	}
	return result, nil
}
func scalar(s string) (float64, error) {
	n, e := numbers(s)
	if e != nil || len(n) != 1 {
		return 0, invalid
	}
	return n[0], nil
}
func colorOK(s string) bool {
	if s == "none" || hexRE.MatchString(s) {
		return true
	}
	_, ok := colornames.Map[s]
	return ok
}
func dimension(n float64) bool { return n >= 1 && n <= 4096 }
func matrixOK(m rasterx.Matrix2D) bool {
	for _, v := range []float64{m.A, m.B, m.C, m.D} {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > 16 {
			return false
		}
	}
	for _, v := range []float64{m.E, m.F} {
		if math.IsNaN(v) || math.IsInf(v, 0) || math.Abs(v) > MaxMagnitude {
			return false
		}
	}
	return math.Abs(m.A*m.D-m.B*m.C) >= 1e-6
}

// Restrict transforms to translate, scale and matrix; rotations/skews are outside
// this initial subset. Canonicalize scale(s), avoiding upstream's scale(s,0).
func transform(s string, parent rasterx.Matrix2D) (string, rasterx.Matrix2D, error) {
	m := parent
	var out strings.Builder
	count := 0
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimSpace(s) {
		i := strings.IndexByte(s, '(')
		j := strings.IndexByte(s, ')')
		if i < 1 || j < i || count >= 8 {
			return "", m, invalid
		}
		count++
		name := strings.TrimSpace(s[:i])
		n, e := numbers(s[i+1 : j])
		if e != nil {
			return "", m, e
		}
		switch name {
		case "scale":
			if len(n) == 1 {
				n = append(n, n[0])
			}
			if len(n) != 2 {
				return "", m, invalid
			}
			m = m.Scale(n[0], n[1])
		case "translate":
			if len(n) == 1 {
				n = append(n, 0)
			}
			if len(n) != 2 {
				return "", m, invalid
			}
			m = m.Translate(n[0], n[1])
		case "matrix":
			if len(n) != 6 {
				return "", m, invalid
			}
			m = m.Mult(rasterx.Matrix2D{A: n[0], B: n[1], C: n[2], D: n[3], E: n[4], F: n[5]})
		default:
			return "", m, invalid
		}
		if !matrixOK(m) {
			return "", m, invalid
		}
		out.WriteString(name + "(")
		for k, v := range n {
			if k > 0 {
				out.WriteByte(',')
			}
			out.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
		}
		out.WriteString(") ")
		s = s[j+1:]
	}
	if count == 0 {
		return "", m, invalid
	}
	return strings.TrimSpace(out.String()), m, nil
}
func pathOK(s string) (int, error) {
	if len(s) == 0 || len(s) > MaxPathBytes {
		return 0, invalid
	}
	count := 0
	sum := 0.0
	for s = strings.TrimSpace(s); s != ""; s = strings.TrimLeft(s, " ,\t\r\n") {
		if strings.ContainsRune("MmLlHhVvCcSsQqTtAaZz", rune(s[0])) {
			s = s[1:]
			count++
			continue
		}
		v := numberRE.FindString(s)
		if v == "" {
			return 0, invalid
		}
		n, e := scalar(v)
		if e != nil {
			return 0, e
		}
		sum += math.Abs(n)
		if sum > 65536 {
			return 0, invalid
		}
		count++
		if count > MaxNumbers {
			return 0, invalid
		}
		s = s[len(v):]
	}
	return count, nil
}

type frame struct {
	name       string
	outputName string
	matrix     rasterx.Matrix2D
}

func validate(data []byte) ([]byte, float64, float64, error) {
	if len(data) == 0 || len(data) > MaxBytes || bytes.ContainsRune(data, '&') {
		return nil, 0, 0, invalid
	}
	// RawToken retains prefixes, so aliasing/rebinding cannot hide qualified names.
	d := xml.NewDecoder(bytes.NewReader(data))
	var out bytes.Buffer
	enc := xml.NewEncoder(&out)
	var stack []frame
	nodes, roots, shapes, pathBytes, numCount := 0, 0, 0, 0, 0
	var w, h float64
	ids := map[string]string{}
	gradReady := map[string]bool{}
	var refs []string
	decl := false
	fail := func() ([]byte, float64, float64, error) { return nil, 0, 0, invalid }
	for {
		token, err := d.RawToken()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fail()
		}
		switch t := token.(type) {
		case xml.Directive:
			return fail()
		case xml.ProcInst:
			if t.Target != "xml" || !bytes.HasPrefix(data, []byte("<?xml")) || decl || roots != 0 || len(stack) != 0 || !declarationRE.Match(t.Inst) {
				return fail()
			}
			decl = true
		case xml.Comment: // discarded, never passed to the renderer
		case xml.CharData:
			if strings.TrimSpace(string(t)) != "" && (len(stack) == 0 || (stack[len(stack)-1].name != "title" && stack[len(stack)-1].name != "desc")) {
				return fail()
			}
		case xml.StartElement:
			name := t.Name.Local
			attrs, known := elementAttrs[name]
			if !known || t.Name.Space != "" {
				return fail()
			}
			nodes++
			if nodes > MaxNodes || len(stack) >= MaxDepth || len(t.Attr) > MaxAttributes {
				return fail()
			}
			parent := ""
			m := rasterx.Identity
			if len(stack) > 0 {
				parent = stack[len(stack)-1].name
				m = stack[len(stack)-1].matrix
			}
			if len(stack) == 0 {
				roots++
				if roots != 1 || name != "svg" {
					return fail()
				}
			} else {
				if name == "svg" || parent == "title" || parent == "desc" || parent == "stop" {
					return fail()
				}
				if parent == "linearGradient" || parent == "radialGradient" {
					if name != "stop" {
						return fail()
					}
				} else if name == "stop" {
					return fail()
				}
				if (name == "linearGradient" || name == "radialGradient") && parent != "defs" {
					return fail()
				}
				if name == "defs" && parent != "svg" {
					return fail()
				}
				if parent != "svg" && parent != "g" && parent != "defs" && parent != "linearGradient" && parent != "radialGradient" {
					return fail()
				}
				if parent == "defs" && name != "linearGradient" && name != "radialGradient" {
					return fail()
				}
			}
			values := map[string]string{}
			clean := xml.StartElement{Name: xml.Name{Local: name}}
			ns := false
			for _, a := range t.Attr {
				// Canonical XML must not introduce escaped whitespace entities:
				// the worker deliberately rejects every entity reference again.
				k, v := a.Name.Local, strings.Join(strings.Fields(a.Value), " ")
				if a.Name.Space != "" || values[k] != "" || v == "" || len(v) > MaxPathBytes {
					return fail()
				}
				values[k] = v
				if k == "xmlns" {
					if name != "svg" || len(stack) != 0 || a.Value != svgNS {
						return fail()
					}
					ns = true
					clean.Attr = append(clean.Attr, a)
					continue
				}
				if k != "id" && !has(attrs, k) && !(has(presentation, k) && name != "defs" && name != "title" && name != "desc" && name != "stop" && name != "linearGradient" && name != "radialGradient") {
					return fail()
				}
				switch k {
				case "id":
					if !idRE.MatchString(v) || ids[v] != "" {
						return fail()
					}
					ids[v] = name
				case "d":
					n, e := pathOK(v)
					if e != nil {
						return fail()
					}
					numCount += n
					pathBytes += len(v)
				case "points":
					n, e := numbers(v)
					if e != nil || len(n) < 4 || len(n)%2 != 0 {
						return fail()
					}
					numCount += len(n)
				case "viewBox":
					n, e := numbers(v)
					if e != nil || len(n) != 4 || !dimension(n[2]) || !dimension(n[3]) {
						return fail()
					}
					w, h = n[2], n[3]
				case "transform":
					var e error
					v, m, e = transform(v, m)
					if e != nil {
						return fail()
					}
				case "fill", "stroke", "stop-color":
					if strings.HasPrefix(v, "url(#") && strings.HasSuffix(v, ")") && k == "fill" {
						id := v[5 : len(v)-1]
						if !idRE.MatchString(id) || !gradReady[id] {
							return fail()
						}
						refs = append(refs, id)
					} else if !colorOK(v) {
						return fail()
					}
				case "fill-rule":
					// ScannerGV supports nonzero winding only; do not silently
					// render evenodd logos with different holes.
					if v != "nonzero" {
						return fail()
					}
				case "stroke-linecap":
					if v != "butt" && v != "round" && v != "square" {
						return fail()
					}
				case "stroke-linejoin":
					if v != "miter" && v != "round" && v != "bevel" {
						return fail()
					}
				case "gradientUnits":
					if v != "userSpaceOnUse" && v != "objectBoundingBox" {
						return fail()
					}
				default:
					raw := v
					if k == "width" || k == "height" {
						raw = strings.TrimSuffix(raw, "px")
					}
					percent := strings.HasSuffix(raw, "%")
					if percent {
						if !has("offset x1 y1 x2 y2 cx cy r fx fy", k) || (name != "stop" && name != "linearGradient" && name != "radialGradient") {
							return fail()
						}
						raw = strings.TrimSuffix(raw, "%")
					}
					n, e := scalar(raw)
					if e != nil {
						return fail()
					}
					if has("opacity fill-opacity stroke-opacity stop-opacity offset", k) {
						limit := 1.0
						if percent {
							limit = 100
						}
						if n < 0 || n > limit {
							return fail()
						}
					}
					// The renderer multiplies inherited paint alpha and cannot
					// composite group/shape opacity. Keep parent paint alpha at
					// its default 1; only leaf paint opacity is faithful.
					if k == "opacity" && n != 1 {
						return fail()
					}
					if (k == "fill-opacity" || k == "stroke-opacity") && !has("path rect circle ellipse line polyline polygon", name) {
						return fail()
					}
					if has("width height r rx ry stroke-width stroke-miterlimit", k) && n < 0 {
						return fail()
					}
					if k == "stroke-width" && n > 128 {
						return fail()
					}
					if k == "stroke-miterlimit" && (n < 1 || n > 10) {
						return fail()
					}
				}
				clean.Attr = append(clean.Attr, xml.Attr{Name: xml.Name{Local: k}, Value: v})
			}
			if name == "svg" {
				if !ns {
					return fail()
				}
				if values["width"] != "" || values["height"] != "" {
					ww, e := scalar(strings.TrimSuffix(values["width"], "px"))
					hh, e2 := scalar(strings.TrimSuffix(values["height"], "px"))
					if e != nil || e2 != nil || !dimension(ww) || !dimension(hh) || ww*hh > 4_000_000 {
						return fail()
					}
					if w != 0 && math.Abs(ww/hh-w/h) > 1e-6 {
						return fail()
					}
					if w == 0 {
						w, h = ww, hh
					}
				}
				if !dimension(w) || !dimension(h) || w*h > 4_000_000 {
					return fail()
				}
			}
			if has("path rect circle ellipse line polyline polygon", name) {
				shapes++
				if name == "path" && values["d"] == "" {
					return fail()
				}
			}
			if pathBytes > MaxPathBytes || numCount > MaxNumbers {
				return fail()
			}
			// The pinned renderer drops polylines with exactly two points.
			if name == "polyline" && values["points"] != "" {
				points, _ := numbers(values["points"])
				if len(points) == 4 {
					clean.Name.Local = "line"
					for i, a := range clean.Attr {
						if a.Name.Local == "points" {
							clean.Attr = append(clean.Attr[:i], clean.Attr[i+1:]...)
							break
						}
					}
					for i, k := range []string{"x1", "y1", "x2", "y2"} {
						clean.Attr = append(clean.Attr, xml.Attr{Name: xml.Name{Local: k}, Value: strconv.FormatFloat(points[i], 'g', -1, 64)})
					}
				}
			}
			if name == "rect" && (values["rx"] != "" || values["ry"] != "") {
				// SVG copies an omitted radius before clamping each axis.
				rx, _ := scalar(values["rx"])
				ry, _ := scalar(values["ry"])
				if values["rx"] == "" {
					rx = ry
				}
				if values["ry"] == "" {
					ry = rx
				}
				ww, _ := scalar(strings.TrimSuffix(values["width"], "px"))
				hh, _ := scalar(strings.TrimSuffix(values["height"], "px"))
				rx, ry = math.Min(rx, ww/2), math.Min(ry, hh/2)
				kept := clean.Attr[:0]
				for _, a := range clean.Attr {
					if a.Name.Local != "rx" && a.Name.Local != "ry" {
						kept = append(kept, a)
					}
				}
				clean.Attr = append(kept,
					xml.Attr{Name: xml.Name{Local: "rx"}, Value: strconv.FormatFloat(rx, 'g', -1, 64)},
					xml.Attr{Name: xml.Name{Local: "ry"}, Value: strconv.FormatFloat(ry, 'g', -1, 64)})
			}
			if len(clean.Attr) > MaxAttributes {
				return fail()
			}
			stack = append(stack, frame{name, clean.Name.Local, m})
			if err := enc.EncodeToken(clean); err != nil {
				return fail()
			}
		case xml.EndElement:
			if t.Name.Space != "" || len(stack) == 0 || stack[len(stack)-1].name != t.Name.Local {
				return fail()
			}
			if t.Name.Local == "linearGradient" || t.Name.Local == "radialGradient" { // only closed gradients can be referenced: no cycles or forward refs
				for id, kind := range ids {
					if kind == t.Name.Local {
						gradReady[id] = true
					}
				}
			}
			outputName := stack[len(stack)-1].outputName
			stack = stack[:len(stack)-1]
			if err := enc.EncodeToken(xml.EndElement{Name: xml.Name{Local: outputName}}); err != nil {
				return fail()
			}
		}
	}
	if len(stack) != 0 || roots != 1 || shapes == 0 {
		return fail()
	}
	for _, id := range refs {
		if ids[id] != "linearGradient" && ids[id] != "radialGradient" {
			return fail()
		}
	}
	if err := enc.Flush(); err != nil {
		return nil, 0, 0, fmt.Errorf("SVG encoding: %w", err)
	}
	return out.Bytes(), w, h, nil
}
