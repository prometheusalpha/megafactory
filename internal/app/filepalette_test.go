package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestFilePaletteOpensFile(t *testing.T) {
	a := newApp(defaultState)
	a.branch = "dev"
	a.OpenProject(sampleProject(t))
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Key(ui.Cmd, ui.KeyP)
	tt.Frame()
	// Files come in off the UI thread; wait for the list.
	for range 50 {
		if a.files != nil && a.files.files != nil {
			break
		}
		tt.Frame()
	}
	tt.Type("helloctl")
	tt.Frame()
	if !tt.HasText("src/main/java/com/example/demo") {
		t.Fatal("palette shows no folder of the hit")
	}
	tt.Key(0, ui.KeyEnter)
	for range 50 {
		tt.Frame()
		if len(a.tabs) > 0 {
			break
		}
	}
	if a.files != nil || len(a.tabs) == 0 || a.tabs[a.active].name != "HelloController.java" {
		t.Fatalf("palette %v, tabs %d", a.files, len(a.tabs))
	}
}
