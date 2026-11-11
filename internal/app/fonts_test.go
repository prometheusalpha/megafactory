package app

import (
	"os"
	"slices"
	"strings"
	"testing"
)

func TestParseGhosttyFont(t *testing.T) {
	cfg := `theme = Ayu Mirage
font-size = 14
adjust-cell-height = 14
# font-family = "Iosevka Nerd Font Mono"
font-family = "JetBrains Mono Nerd Font Mono"
font-feature = ss01, ss02, calt, -liga
font-feature = +dlig
adjust-cell-width = 4%
`
	f := parseGhostty(strings.NewReader(cfg), defaultCodeFont)
	if f.size != 14 || f.cellWidth != (adjust{pct: 0.04}) {
		t.Errorf("size %v, cell width %+v", f.size, f.cellWidth)
	}
	if want := []string{"ss01", "ss02", "calt", "-liga", "+dlig"}; !slices.Equal(f.features, want) {
		t.Errorf("features %q, want %q", f.features, want)
	}
	// Ghostty's spelling of the family finds the installed one.
	f.resolve()
	if f.families[0] != codeFamily {
		t.Errorf("family %q, want %q", f.families[0], codeFamily)
	}
	if font := f.editorFont(); font.Features != "ss01, ss02, calt, liga=0, dlig" {
		t.Errorf("editor features %q", font.Features)
	}
	if tf := f.terminalFont(); tf.LineHeight != prefs.TerminalLineHeight || tf.CellWidth != 1.04 || len(tf.Features) != 5 {
		t.Errorf("terminal font %+v", tf)
	}
}

func TestDefaultCodeFontWithoutConfig(t *testing.T) {
	f := parseGhostty(strings.NewReader(""), defaultCodeFont)
	f.resolve()
	if font := f.editorFont(); font.Features != "calt" || !strings.HasPrefix(font.Family, codeFamily) {
		t.Errorf("default font %+v", font)
	}
}

func TestMain(m *testing.M) {
	RegisterFonts()
	// Tests keep their settings and state away from the user's.
	dir, err := os.MkdirTemp("", "megafactory-test")
	if err != nil {
		panic(err)
	}
	dataDir = dir
	// The tests click the sample's folders one at a time.
	defaultSettings.CompactFolders = false
	prefs = defaultSettings
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}
