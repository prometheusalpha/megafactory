package app

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"
)

func TestAutoSaveWritesAfterTheDelay(t *testing.T) {
	a, tt := openSample(t)
	var due []func()
	a.host.After = func(_ time.Duration, fn func()) { due = append(due, fn) }
	prefs.AutoSave, prefs.AutoSaveDelay = true, 1
	t.Cleanup(func() { prefs = defaultSettings })
	click(t, tt, "pom.xml")
	tb := a.tabs[a.active]
	tb.ed.Buffer.Edit(editor.Edit{Text: "x"})
	tt.Frame()
	if len(due) != 1 {
		t.Fatalf("scheduled %d saves, want 1", len(due))
	}
	tt.Frame()
	if len(due) != 1 {
		t.Fatalf("a frame without an edit scheduled another: %d", len(due))
	}
	tb.ed.Buffer.Edit(editor.Edit{Text: "y"})
	tt.Frame()
	due[0]() // the first pause ends, but a later edit moved the text on
	if !tb.ed.Buffer.Modified() {
		t.Fatal("saved before the last edit's pause")
	}
	due[1]()
	if tb.ed.Buffer.Modified() {
		t.Fatal("still modified after its pause")
	}
	got, _ := os.ReadFile(filepath.Join(a.project, "pom.xml"))
	if string(got[:2]) != "xy" && string(got[:2]) != "yx" {
		t.Errorf("file starts %q, want the edits", got[:2])
	}
}

func TestAutoSaveOffSchedulesNothing(t *testing.T) {
	a, tt := openSample(t)
	n := 0
	a.host.After = func(time.Duration, func()) { n++ }
	prefs.AutoSave = false
	t.Cleanup(func() { prefs = defaultSettings })
	click(t, tt, "pom.xml")
	a.tabs[a.active].ed.Buffer.Edit(editor.Edit{Text: "x"})
	tt.Frame()
	if n != 0 {
		t.Errorf("scheduled %d saves with auto save off", n)
	}
}
