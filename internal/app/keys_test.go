package app

import (
	"testing"

	"megafactory/internal/keymap"

	"github.com/egoist/mygo/ui"
)

// openThree opens three tabs and leaves the editor, which takes Tab
// itself, focused on the last.
func openThree(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a, tt := openSample(t)
	click(t, tt, "pom.xml", "src", "main", "java", "com", "example", "demo", "DemoApplication.java", "resources", "config.json")
	if len(a.tabs) != 3 {
		t.Fatalf("%d tabs, want 3", len(a.tabs))
	}
	return a, tt
}

func TestCtrlTabCyclesTabs(t *testing.T) {
	a, tt := openThree(t)
	if a.active != 2 {
		t.Fatalf("active %d, want 2", a.active)
	}
	tt.Key(ui.Ctrl, ui.KeyTab)
	if a.active != 0 {
		t.Errorf("after Ctrl+Tab at the last tab: active %d, want 0", a.active)
	}
	tt.Key(ui.Ctrl, ui.KeyTab)
	if a.active != 1 {
		t.Errorf("after Ctrl+Tab: active %d, want 1", a.active)
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	if a.active != 2 {
		t.Errorf("after Ctrl+Shift+Tab twice: active %d, want 2", a.active)
	}
}

func TestCloseOtherTabsKey(t *testing.T) {
	a, tt := openThree(t)
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyTab)
	keep := a.tabs[a.active]
	tt.Key(ui.Cmd|ui.Shift, ui.KeyW)
	if len(a.tabs) != 1 || a.tabs[0] != keep {
		t.Errorf("%d tabs left, want only %s", len(a.tabs), keep.name)
	}
}

func TestRebindFromSettings(t *testing.T) {
	defer func() { prefs = defaultSettings }()
	a, tt := openThree(t)
	menus := 0
	a.OnKeymapChange(func() { menus++ })
	tt.SetSize(1280, 3000) // the whole settings page shows
	click(t, tt, "Settings  ⌘,", "Next Tab shortcut")
	if !a.Recording() {
		t.Fatal("clicking the key did not wait for one")
	}
	// ⌘W is Close Tab's, so it is refused, and no tab closes with it.
	tt.Key(ui.Cmd, ui.KeyW)
	if !a.Recording() || a.keys.Binding("nextTab").Key != ui.KeyTab || len(a.tabs) != 4 {
		t.Errorf("a key in use was taken: recording %v, tabs %d", a.Recording(), len(a.tabs))
	}
	// A letter on its own would type into the editor.
	tt.Key(0, ui.KeyJ)
	if !a.Recording() {
		t.Error("a key with no modifier was taken")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyRight)
	if a.Recording() || a.keys.Binding("nextTab") != (keymap.Binding{Mods: ui.Cmd | ui.Alt, Key: ui.KeyRight}) {
		t.Fatalf("not rebound: recording %v, %v", a.Recording(), a.keys.Binding("nextTab"))
	}
	if menus < 2 {
		t.Errorf("the menu bar was told %d times, want one per recording start and stop", menus)
	}
	if got := LoadSettings().Keymap["nextTab"]; got != a.keys.Binding("nextTab").String() {
		t.Errorf("kept %q", got)
	}
	// The old key does nothing now; the new one does.
	before := a.active
	tt.Key(ui.Ctrl, ui.KeyTab)
	if a.active != before {
		t.Error("the old key still steps a tab")
	}
	tt.Key(ui.Cmd|ui.Alt, ui.KeyRight)
	if a.active == before {
		t.Error("the new key does not step a tab")
	}
}

func TestEscapeStopsRecording(t *testing.T) {
	defer func() { prefs = defaultSettings }()
	a, tt := openThree(t)
	tt.SetSize(1280, 3000)
	click(t, tt, "Settings  ⌘,", "Go to File shortcut")
	tt.Key(0, ui.KeyEscape)
	if a.Recording() || a.keys.Binding("findFile").Key != ui.KeyP {
		t.Errorf("recording %v, findFile %v", a.Recording(), a.keys.Binding("findFile"))
	}
}
