package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func caretLine(a *App) int { return a.tabs[a.active].ed.CursorPos().Line }

func TestBackAndForwardBetweenFiles(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "pom.xml", "src", "main", "java", "com", "example", "demo", "DemoApplication.java")
	pom, demo := a.tabs[0].ed.Buffer.String(), a.tabs[1].ed.Buffer.String()
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	if got := a.tabs[a.active].name; got != "pom.xml" {
		t.Fatalf("Back went to %s, want pom.xml", got)
	}
	tt.Key(ui.Cmd, ui.KeyBracketRight)
	if got := a.tabs[a.active].name; got != "DemoApplication.java" {
		t.Errorf("Forward went to %s, want DemoApplication.java", got)
	}
	// The keys are no longer the editor's Outdent and Indent.
	tt.Key(ui.Cmd, ui.KeyBracketRight)
	if a.tabs[0].ed.Buffer.String() != pom || a.tabs[1].ed.Buffer.String() != demo {
		t.Error("⌘[ or ⌘] changed the text")
	}
}

func TestBackAfterAFarJumpInAFile(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "pom.xml")
	ed := a.tabs[a.active].ed
	ed.Buffer.Set(strings.Repeat("line\n", 40))
	ed.GoTo(ed.Buffer.Pos(0))
	tt.Frame()
	// One line at a time is not a jump, however far it goes.
	for range 15 {
		tt.Key(0, ui.KeyDown)
	}
	tt.Key(ui.Cmd, ui.KeyDown)
	if caretLine(a) != 40 {
		t.Fatalf("caret on line %d, want the end", caretLine(a))
	}
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	if caretLine(a) != 15 {
		t.Errorf("Back to line %d, want 15", caretLine(a))
	}
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	if caretLine(a) != 15 {
		t.Errorf("a second Back went to line %d: the arrows' steps were kept", caretLine(a))
	}
	tt.Key(ui.Cmd, ui.KeyBracketRight)
	if caretLine(a) != 40 {
		t.Errorf("Forward to line %d, want 40", caretLine(a))
	}
}

func TestBackOpensAClosedFile(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "pom.xml", "src", "main", "java", "com", "example", "demo", "DemoApplication.java")
	a.requestClose(a.tabs[0])
	tt.Frame()
	if len(a.tabs) != 1 {
		t.Fatalf("%d tabs, want pom.xml's closed", len(a.tabs))
	}
	tt.Key(ui.Cmd, ui.KeyBracketLeft)
	if got := a.tabs[a.active].name; got != "pom.xml" {
		t.Errorf("Back went to %s, want pom.xml opened again", got)
	}
}
