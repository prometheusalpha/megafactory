package app

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/egoist/mygo/ui"
)

// twoProjects opens a project after another, which both go to Recent.
func twoProjects(t *testing.T) (a *App, tt *ui.Tester, first, second string) {
	t.Helper()
	a = newApp(defaultState)
	a.branch = "dev"
	a.host.ShowError = func(title, msg string) { t.Errorf("%s: %s", title, msg) }
	first = sampleProject(t)
	second = filepath.Join(t.TempDir(), "notes")
	os.MkdirAll(second, 0o755)
	os.WriteFile(filepath.Join(second, "todo.md"), []byte("- [ ] x\n"), 0o644)
	a.OpenProject(first)
	a.OpenProject(second)
	return a, ui.NewTester(a.View, 1280, 800), first, second
}

func TestRecentProjectsKept(t *testing.T) {
	a, _, first, second := twoProjects(t)
	if !slices.Equal(a.recent, []string{second, first}) || !slices.Equal(loadRecent(), a.recent) {
		t.Errorf("recent %q, kept %q", a.recent, loadRecent())
	}
}

func TestPaletteSwitchesProject(t *testing.T) {
	a, tt, first, _ := twoProjects(t)
	tt.Key(ui.Cmd|ui.Shift, ui.KeyO)
	tt.Frame()
	if !tt.HasText("current") || !tt.HasText("spring-demo") {
		t.Fatal("palette shows no projects")
	}
	// Typing filters; Enter opens the first left.
	tt.Type("sprdem")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.project != first || a.palette != nil || !tt.HasText("pom.xml") {
		t.Fatalf("project %q, palette %v", a.project, a.palette)
	}
	// Down past the projects is Open Folder.
	picked := false
	a.host.PickFolder = func(func(string)) { picked = true }
	tt.Key(ui.Cmd|ui.Shift, ui.KeyO)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	if !picked {
		t.Error("Open Folder not chosen")
	}
}

func TestPaletteAsksToSave(t *testing.T) {
	a, tt, first, second := twoProjects(t)
	click(t, tt, "todo.md")
	tt.Type("x")
	var asked string
	a.host.Ask = func(q Question, done func(int)) { asked = q.Message; done(2) }
	tt.Key(ui.Cmd|ui.Shift, ui.KeyO)
	tt.Frame()
	click(t, tt, "spring-demo")
	if asked != "Save the changes to “todo.md”?" || a.project != second {
		t.Fatalf("asked %q, project %q", asked, a.project)
	}
	a.host.Ask = func(_ Question, done func(int)) { done(0) }
	tt.Key(ui.Cmd|ui.Shift, ui.KeyO)
	tt.Frame()
	click(t, tt, "spring-demo")
	if a.project != first || readSample(t, second, "todo.md") != "x- [ ] x\n" {
		t.Errorf("project %q, todo.md %q", a.project, readSample(t, second, "todo.md"))
	}
}

func TestPaletteMissingAndRemove(t *testing.T) {
	a, tt, first, second := twoProjects(t)
	os.RemoveAll(first)
	var shown string
	a.host.ShowError = func(title, _ string) { shown = title }
	tt.Key(ui.Cmd|ui.Shift, ui.KeyO)
	tt.Frame()
	if !tt.HasText("not found") {
		t.Error("a gone project isn't marked")
	}
	click(t, tt, "spring-demo")
	if shown != "Project not found" || a.project != second || slices.Contains(a.recent, first) {
		t.Errorf("shown %q, project %q, recent %q", shown, a.project, a.recent)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyO)
	tt.Frame()
	click(t, tt, "Remove notes from Recent Projects")
	if len(a.recent) != 0 || a.project != second || a.palette == nil {
		t.Errorf("recent %q, project %q, palette open %v", a.recent, a.project, a.palette != nil)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.palette != nil {
		t.Error("Escape left the palette open")
	}
}
