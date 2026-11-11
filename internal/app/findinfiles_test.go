package app

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func openFind(t *testing.T) (*App, *ui.Tester) {
	t.Helper()
	a, tt := openSample(t)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyF)
	tt.Frame()
	if a.findIn == nil {
		t.Fatal("Cmd+Shift+F did not open Find in Files")
	}
	return a, tt
}

func TestFindInFilesJumpsToTheMatch(t *testing.T) {
	a, tt := openFind(t)
	tt.Type("springapplication.run")
	tt.Frame()
	if !tt.HasText("1 match in 1 file") {
		t.Fatalf("status: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.findIn != nil || len(a.tabs) != 1 || a.tabs[0].name != "DemoApplication.java" {
		t.Fatalf("find %v, tabs %d", a.findIn, len(a.tabs))
	}
	if got := a.tabs[0].ed.SelectedText(); got != "SpringApplication.run" {
		t.Errorf("selected %q", got)
	}
	if line := a.tabs[0].ed.CursorPos().Line; line != 9 {
		t.Errorf("caret on line %d, want 9", line)
	}
}

func TestFindInFilesOptions(t *testing.T) {
	a, tt := openFind(t)
	tt.Type("springapplication")
	tt.Frame()
	click(t, tt, "Match case")
	if !a.findIn.query.MatchCase || !tt.HasText("No matches") {
		t.Errorf("Match case: %v, %v", a.findIn.query, tt.Texts())
	}
	click(t, tt, "Match case", "Regex")
	tt.Type("(")
	tt.Frame()
	if !tt.HasText("Not a valid regular expression") {
		t.Errorf("an invalid regex says: %v", tt.Texts())
	}
}

func TestFindInFilesTakesTheSelection(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "pom.xml")
	a.tabs[0].ed.Select(0, 5)
	want := a.tabs[0].ed.SelectedText()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyF)
	tt.Frame()
	if a.findIn == nil || a.findIn.query.Text != want {
		t.Errorf("query %v, want %q", a.findIn, want)
	}
}

func TestFindInFilesPreviewsTheSelectedMatch(t *testing.T) {
	a, tt := openFind(t)
	tt.Type("springapplication.run")
	tt.Frame()
	tt.Frame()
	pv := a.findIn.preview
	if pv.ed == nil || pv.path != "src/main/java/com/example/demo/DemoApplication.java" {
		t.Fatalf("preview of %q", pv.path)
	}
	if !pv.ed.ReadOnly {
		t.Error("the preview is editable")
	}
	if got := pv.ed.SelectedText(); got != "SpringApplication.run" {
		t.Errorf("preview selects %q", got)
	}
	if len(a.tabs) != 0 {
		t.Errorf("previewing opened a tab: %d tabs", len(a.tabs))
	}
}
