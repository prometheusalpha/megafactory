package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestThemeColorsEditor(t *testing.T) {
	th, err := loadThemes("Catppuccin Mocha")
	if err != nil {
		t.Fatal(err)
	}
	term, et, k := th.pick(false)
	if term != th.dark || !isDark(et.Background) {
		t.Error("a single dark theme should serve light windows too")
	}
	if et.Background != ui.Hex("#1e1e2e") || et.Text != ui.Hex("#cdd6f4") {
		t.Errorf("background %v, text %v", et.Background, et.Text)
	}
	if got := et.Tokens[editor.TokenKeyword].Color; got != term.Palette[ansiMagenta] {
		t.Errorf("keyword %v", got)
	}
	if k.editor != et.Background {
		t.Errorf("editor card %v, editor %v", k.editor, et.Background)
	}
}

func TestThemePairs(t *testing.T) {
	th, err := loadThemes("light:Catppuccin Latte,dark:Catppuccin Mocha")
	if err != nil {
		t.Fatal(err)
	}
	if isDark(th.editorLight.Background) || !isDark(th.editorDark.Background) {
		t.Error("light and dark themes swapped")
	}
	if _, err := loadThemes("light:Catppuccin Latte,Catppuccin Mocha"); err == nil {
		t.Error("a pair without dark: loaded")
	}
}

func TestEveryThemeChoiceLoads(t *testing.T) {
	for _, ch := range themeChoices {
		th := themeNamed(ch.name)
		if (th.light == nil) != (ch.spec == "") {
			t.Errorf("%s: loaded %v", ch.name, th.light != nil)
		}
	}
	if th := themeNamed("Solarized"); th.light != nil {
		t.Error("an unknown theme should be the app's own colors")
	}
}
