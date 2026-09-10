package model

import (
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"strings"

	"github.com/chai-rs/handdraw-server/pkg/resourceid"
)

// Validate checks native geometry and every supported Handdraw custom-data carrier.
func (s Scene) Validate(scope Validation) error {
	if s.AppState == nil || s.Files == nil || s.ElementOrder == nil || s.Elements == nil || len(s.Elements) > 20000 || len(s.ElementOrder) != len(s.Elements) {
		return ErrInvalidDocument
	}

	ids := map[string]bool{}
	for _, id := range s.ElementOrder {
		if ids[id] {
			return ErrInvalidDocument
		}

		ids[id] = true
		if _, ok := s.Elements[id]; !ok {
			return ErrInvalidDocument
		}
	}

	for key, e := range s.Elements {
		id, _ := e["id"].(string)
		kind, _ := e["type"].(string)

		if id == "" || len(id) > 256 || id != key {
			return ErrInvalidDocument
		}

		if !oneOf(kind, "rectangle", "ellipse", "diamond", "text", "line", "arrow", "freedraw", "image", "frame", "magicframe", "embeddable") {
			return ErrInvalidDocument
		}

		for _, key := range []string{"x", "y", "width", "height", "angle", "version", "versionNonce"} {
			if !finite(e[key]) {
				return ErrInvalidDocument
			}
		}

		if number(e["width"]) < 0 || number(e["height"]) < 0 || number(e["version"]) < 1 {
			return ErrInvalidDocument
		}

		if _, ok := e["isDeleted"].(bool); !ok {
			return ErrInvalidDocument
		}

		if oneOf(kind, "line", "arrow") && !points(e["points"], 2) {
			return ErrInvalidDocument
		}

		if kind == "freedraw" && !points(e["points"], 1) {
			return ErrInvalidDocument
		}

		if kind == "text" {
			if _, ok := e["text"].(string); !ok {
				return ErrInvalidDocument
			}

			if !finite(e["fontSize"]) || number(e["fontSize"]) <= 0 {
				return ErrInvalidDocument
			}
		}

		if kind == "image" {
			file, ok := e["fileId"].(string)

			deleted, _ := e["isDeleted"].(bool)
			if !deleted {
				if !ok {
					return ErrInvalidDocument
				}

				if _, ok := s.Files[file]; !ok {
					return ErrInvalidDocument
				}
			}
		}

		if link, ok := e["link"].(string); ok && link != "" && !safeLink(link) {
			return ErrInvalidDocument
		}

		if raw, ok := e["customData"]; ok && raw != nil {
			custom, ok := raw.(map[string]any)
			if !ok {
				return ErrInvalidDocument
			}

			if err := validateCustom(kind, custom); err != nil {
				return err
			}
		}
	}

	for file, asset := range s.Files {
		if file == "" || resourceid.Validate(asset.AssetID, AssetIDPrefix) != nil || !scope.AllowedAssets[asset.AssetID] || !oneOf(asset.MIMEType, "image/png", "image/jpeg", "image/webp", "image/gif", "image/svg+xml") {
			return ErrInvalidDocument
		}
	}

	for key, value := range s.AppState {
		switch key {
		case "viewBackgroundColor":
			if _, ok := value.(string); !ok {
				return ErrInvalidDocument
			}
		case "canvasBackground":
			if !oneOf(value, "empty", "dotted", "grid") {
				return ErrInvalidDocument
			}
		case "gridModeEnabled":
			if _, ok := value.(bool); !ok {
				return ErrInvalidDocument
			}
		case "gridSize", "gridStep":
			if value != nil && (!finite(value) || number(value) < 0) {
				return ErrInvalidDocument
			}
		default:
			return ErrInvalidDocument
		}
	}

	return nil
}

func validateCustom(kind string, c map[string]any) error {
	for key, v := range c {
		switch key {
		case "handdrawName":
			s, ok := v.(string)
			if !ok || len(s) > 1024 {
				return ErrInvalidDocument
			}
		case "handdrawTable":
			if kind != "rectangle" || !validTable(v) {
				return ErrInvalidDocument
			}
		case "handdrawDiagramBlock":
			m, ok := v.(map[string]any)
			if kind != "image" || !ok || number(m["v"]) != 1 || !oneOf(m["kind"], "code", "latex", "mermaid") {
				return ErrInvalidDocument
			}

			source, ok := m["source"].(string)
			if !ok || textUnits(source) > 50000 {
				return ErrInvalidDocument
			}

			if _, ok := m["wrap"].(bool); !ok {
				return ErrInvalidDocument
			}

			if m["language"] != nil {
				language, ok := m["language"].(string)
				if !ok || len(language) > 80 {
					return ErrInvalidDocument
				}
			}

			if filename, exists := m["filename"]; exists {
				value, ok := filename.(string)
				if !ok || textUnits(value) > 255 {
					return ErrInvalidDocument
				}
			}
		case "handdrawGeneralShape":
			if kind != "rectangle" || !validGeneralShape(v) {
				return ErrInvalidDocument
			}
		case "handdrawShape":
			m, ok := v.(map[string]any)
			if kind != "image" || !ok {
				return ErrInvalidDocument
			}

			for _, name := range []string{"id", "category", "source"} {
				if value, ok := m[name].(string); !ok || value == "" {
					return ErrInvalidDocument
				}
			}
		case "handdrawLabel":
			m, ok := v.(map[string]any)
			if kind != "text" || !ok {
				return ErrInvalidDocument
			}

			if owner, ok := m["ownerId"].(string); !ok || owner == "" {
				return ErrInvalidDocument
			}
		default:
			return ErrInvalidDocument
		}
	}

	return nil
}

func validTable(v any) bool {
	m, ok := v.(map[string]any)
	if !ok || number(m["v"]) != 1 {
		return false
	}

	cols, ok := m["cols"].([]any)
	if !ok || len(cols) < 1 || len(cols) > 32 {
		return false
	}

	rows, ok := m["rows"].([]any)
	if !ok || len(rows) < 1 || len(rows) > 200 {
		return false
	}

	for _, axis := range [][]any{cols, rows} {
		sum := 0.0

		for _, n := range axis {
			if !finite(n) || number(n) <= 0 || number(n) > 1 {
				return false
			}

			sum += number(n)
		}

		if math.Abs(sum-1) >= 1e-6 {
			return false
		}
	}

	cells, ok := m["cells"].([]any)
	if !ok || len(cells) != len(rows) {
		return false
	}

	for _, r := range cells {
		row, ok := r.([]any)
		if !ok || len(row) != len(cols) {
			return false
		}

		for _, cell := range row {
			c, ok := cell.(map[string]any)
			if !ok {
				return false
			}

			text, ok := c["text"].(string)
			if !ok || textUnits(text) > 4000 {
				return false
			}

			for key, val := range c {
				switch key {
				case "text":
				case "align":
					if !oneOf(val, "left", "center", "right") {
						return false
					}
				case "verticalAlign":
					if !oneOf(val, "top", "middle", "bottom") {
						return false
					}
				case "textColor", "backgroundColor":
					s, ok := val.(string)
					if !ok || len(s) < 1 || len(s) > 128 {
						return false
					}
				default:
					return false
				}
			}
		}
	}

	align, ok := m["align"].([]any)
	if !ok || len(align) != len(cols) {
		return false
	}

	for _, a := range align {
		if !oneOf(a, "left", "center", "right") {
			return false
		}
	}

	for _, key := range []string{"headerRow", "headerCol"} {
		if _, ok := m[key].(bool); !ok {
			return false
		}
	}

	return finite(m["fontFamily"]) && number(m["fontFamily"]) >= 1 && number(m["fontFamily"]) <= 10 && math.Trunc(number(m["fontFamily"])) == number(m["fontFamily"]) && finite(m["fontSize"]) && number(m["fontSize"]) >= 8 && number(m["fontSize"]) <= 96 && finite(m["pad"]) && number(m["pad"]) >= 0 && number(m["pad"]) <= 64
}

func finite(v any) bool {
	switch v.(type) {
	case float64, float32, int, int64, json.Number:
		n := number(v)
		return !math.IsNaN(n) && !math.IsInf(n, 0)
	default:
		return false
	}
}

func number(v any) float64 {
	switch n := v.(type) {
	case float64:
		return n
	case float32:
		return float64(n)
	case int:
		return float64(n)
	case int64:
		return float64(n)
	case json.Number:
		value, err := n.Float64()
		if err == nil {
			return value
		}
	}

	return math.NaN()
}

func oneOf(v any, allowed ...string) bool {
	s, ok := v.(string)
	if !ok {
		return false
	}

	for _, a := range allowed {
		if s == a {
			return true
		}
	}

	return false
}

func points(v any, min int) bool {
	ps, ok := v.([]any)
	if !ok || len(ps) < min || len(ps) > 100000 {
		return false
	}

	for _, v := range ps {
		p, ok := v.([]any)
		if !ok || len(p) != 2 || !finite(p[0]) || !finite(p[1]) {
			return false
		}
	}

	return true
}

func safeLink(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http" || u.Scheme == "mailto" || u.Scheme == "handdraw" || strings.HasPrefix(s, "#") || strings.HasPrefix(s, "/"))
}

var (
	shapePage = regexp.MustCompile(`<Shape\b[^>]*\bpage="([^"]+)"`)
	mdxTag    = regexp.MustCompile(`<([A-Z][A-Za-z0-9]*)\b`)
)

func validateNote(body string) error {
	var visible strings.Builder

	fence := ""

	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") || strings.HasPrefix(trimmed, "~~~") {
			marker := trimmed[:3]
			if fence == "" {
				fence = marker
			} else if fence == marker {
				fence = ""
			}

			continue
		}

		if fence == "" {
			visible.WriteString(line)
			visible.WriteByte('\n')
		}
	}

	prose := inlineCode.ReplaceAllString(visible.String(), "")
	for _, tag := range mdxTag.FindAllStringSubmatch(prose, -1) {
		if tag[1] != "Shape" && tag[1] != "Embed" {
			return ErrInvalidDocument
		}
	}

	for _, ref := range shapePage.FindAllStringSubmatch(prose, -1) {
		if resourceid.Validate(ref[1], PageIDPrefix) != nil {
			return ErrInvalidDocument
		}
	}

	return nil
}

var inlineCode = regexp.MustCompile("`[^`]*`")

func validGeneralShape(v any) bool {
	m, ok := v.(map[string]any)
	if !ok || number(m["v"]) != 1 || !points(m["points"], 4) {
		return false
	}

	id, ok := m["id"].(string)
	if !ok || id == "" || len(id) > 100 || !finite(m["width"]) || !finite(m["height"]) {
		return false
	}

	w, h := number(m["width"]), number(m["height"])
	if w <= 0 || h <= 0 {
		return false
	}

	ps := m["points"].([]any)
	if len(ps) > 2048 {
		return false
	}

	tolerance := math.Max(w, h) * 1e-10

	for _, p := range ps {
		pair := p.([]any)

		x, y := number(pair[0]), number(pair[1])
		if x < -tolerance || y < -tolerance || x > w+tolerance || y > h+tolerance {
			return false
		}
	}

	first, last := ps[0].([]any), ps[len(ps)-1].([]any)

	return math.Abs(number(first[0])-number(last[0])) <= tolerance && math.Abs(number(first[1])-number(last[1])) <= tolerance
}

// Editor schema limits use JavaScript UTF-16 string length.
func textUnits(s string) int {
	n := 0
	for _, r := range s {
		n++
		if r > 0xffff {
			n++
		}
	}

	return n
}
