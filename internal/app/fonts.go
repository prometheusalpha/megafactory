package app

import (
	"bufio"
	"io"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// codeFamily is the family the bundled fonts register under. The Mono
// build of the Nerd Font keeps its icons one cell wide, so they don't
// break the editor's columns.
const codeFamily = "JetBrainsMono Nerd Font Mono"

func RegisterFonts() {
	for _, style := range []string{"Regular", "Bold", "Italic", "BoldItalic"} {
		data, err := assets.ReadFile("assets/fonts/JetBrainsMonoNerdFontMono-" + style + ".ttf")
		if err == nil {
			err = ui.RegisterFont(data, codeFamily)
		}
		if err != nil {
			// Code falls back to the families after codeFamily.
			log.Printf("font %s: %v", style, err)
		}
	}
}

// codeFont is the font of code, the editor's and the terminal's. It takes
// the user's Ghostty font settings when there are some, so that code
// reads the same here as in their terminal.
type codeFont struct {
	families []string // the user's, before the bundled fallbacks
	size     float32
	features []string // as Ghostty's font-feature: "ss01", "+ss01", "-calt", "cv05=2"
	thicken  bool
	// cellHeight and cellWidth are Ghostty's adjust-cell-height and
	// adjust-cell-width.
	cellHeight, cellWidth adjust

	rowH float32 // the height of a terminal row, after cellHeight
}

// adjust is a Ghostty adjust-* value: DIPs ("14") or a share ("20%").
type adjust struct {
	px, pct float32
}

func (a adjust) apply(v float32) float32 { return v*(1+a.pct) + a.px }

var defaultCodeFont = codeFont{size: 13, features: []string{"calt"}, cellHeight: adjust{pct: 0.14}}

// code is the code font of this run; loadCodeFont sets it.
var code = defaultCodeFont

// LoadGhostty reads the user's Ghostty font settings, once the bundled
// fonts are registered: what it can't read leaves the defaults. Its theme
// is not the app's, which the settings choose.
func LoadGhostty() {
	code = defaultCodeFont
	if f, err := os.Open(ghosttyConfig()); err == nil {
		code = parseGhostty(f, defaultCodeFont)
		f.Close()
	}
	code.resolve()
}

func ghosttyConfig() string {
	dir := os.Getenv("XDG_CONFIG_HOME")
	if dir == "" {
		home, _ := os.UserHomeDir()
		dir = filepath.Join(home, ".config")
	}
	return filepath.Join(dir, "ghostty", "config")
}

// parseGhostty reads the font settings of a Ghostty config: lines of
// key = value, where font-family and font-feature repeat, and an empty
// value resets them. Other keys are Ghostty's alone, or not yet ours.
func parseGhostty(r io.Reader, f codeFont) codeFont {
	featuresSet := false
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.Trim(strings.TrimSpace(v), `"`)
		switch k {
		case "font-family":
			if v == "" {
				f.families = nil
			} else {
				f.families = append(f.families, v)
			}
		case "font-size":
			if n, err := strconv.ParseFloat(v, 32); err == nil && n > 0 {
				f.size = float32(n)
			}
		case "font-feature":
			// The user's features replace the defaults rather than add to them.
			if !featuresSet {
				f.features, featuresSet = nil, true
			}
			for _, ft := range strings.Split(v, ",") {
				if ft = strings.TrimSpace(ft); ft != "" {
					f.features = append(f.features, ft)
				}
			}
		case "font-thicken":
			f.thicken = v == "true"
		case "adjust-cell-height":
			f.cellHeight = parseAdjust(v)
		case "adjust-cell-width":
			f.cellWidth = parseAdjust(v)
		}
	}
	return f
}

func parseAdjust(v string) adjust {
	if p, ok := strings.CutSuffix(v, "%"); ok {
		n, _ := strconv.ParseFloat(p, 32)
		return adjust{pct: float32(n) / 100}
	}
	n, _ := strconv.ParseFloat(v, 32)
	return adjust{px: float32(n)}
}

// resolve matches the user's families to installed ones, as Ghostty
// does: "JetBrains Mono Nerd Font" is the family "JetBrainsMono Nerd
// Font". It then measures the line height the cells adjust.
func (f *codeFont) resolve() {
	known := map[string]string{}
	for _, fam := range ui.FontFamilies() {
		known[squash(fam.Name)] = fam.Name
	}
	for i, fam := range f.families {
		if name, ok := known[squash(fam)]; ok {
			f.families[i] = name
		}
	}
	m := ui.Font{Family: f.family(), Size: f.size}.Metrics()
	f.rowH = f.cellHeight.apply(m.Ascent + m.Descent + m.LineGap)
}

func squash(s string) string { return strings.ToLower(strings.ReplaceAll(s, " ", "")) }

func (f codeFont) family() string {
	return strings.Join(append(append([]string{}, f.families...), codeFamily, "SF Mono", "Menlo", "monospace"), ", ")
}

// editorFont is the font of the code editor. Its line height is the
// editor's own setting (prefs.EditorLineHeight), not adjust-cell-height.
func (f codeFont) editorFont() ui.Font {
	// Element.FontFeatures takes "calt=0" where Ghostty says "-calt".
	features := make([]string, len(f.features))
	for i, ft := range f.features {
		if name, ok := strings.CutPrefix(ft, "-"); ok {
			ft = name + "=0"
		}
		features[i] = strings.TrimPrefix(ft, "+")
	}
	return ui.Font{Family: f.family(), Size: f.size, Features: strings.Join(features, ", ")}
}

// terminalFont is the font of the terminal, which takes Ghostty's
// features as they are, and its line height as a multiple of the font's
// own. adjust-cell-width in DIPs is left out: the terminal sizes columns
// as a multiple of the advance alone.
func (f codeFont) terminalFont() terminal.Font {
	m := ui.Font{Family: f.family(), Size: f.size}.Metrics()
	return terminal.Font{
		Family:     f.family(),
		Size:       f.size,
		Features:   f.features,
		Thicken:    f.thicken,
		LineHeight: f.rowH / (m.Ascent + m.Descent + m.LineGap),
		CellWidth:  1 + f.cellWidth.pct,
	}
}
