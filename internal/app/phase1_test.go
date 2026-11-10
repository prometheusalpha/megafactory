package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// asyncHost holds the jobs the app runs off the UI thread until flush, as
// a window's would until their I/O ends.
type asyncHost struct{ jobs []func() func() }

func (h *asyncHost) run(job func() func()) { h.jobs = append(h.jobs, job) }

// flush runs the jobs held, and those they start, as the window would
// deliver them.
func (h *asyncHost) flush(tt *ui.Tester) {
	for len(h.jobs) > 0 {
		jobs := h.jobs
		h.jobs = nil
		for _, job := range jobs {
			job()()
		}
		tt.Frame()
	}
}

func TestTreeKeysOpenFolders(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "src")
	click(t, tt, "src") // closed again, with the focus on it
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	src := a.root.at("src")
	if !src.open || !src.loaded || !tt.HasText("main") {
		t.Errorf("→ on src: open %v, loaded %v", src.open, src.loaded)
	}
	// The selection follows the keys.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if a.selected != "src/main" {
		t.Errorf("selected %q after ↓", a.selected)
	}
}

func TestRenameOnBlurAndBadNames(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	var errs []string
	a.host.ShowError = func(title, _ string) { errs = append(errs, title) }
	menu(t, tt, "java", "Rename…")
	tt.Type("a/b")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	// Enter on a wrong name says so, and keeps the input to fix it.
	if len(errs) != 1 || a.edit == nil {
		t.Fatalf("errors %q, editing %v", errs, a.edit != nil)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("kotlin")
	// Leaving the input takes the name.
	click(t, tt, "resources")
	tt.Frame()
	if a.edit != nil || !onDisk(dir, "src/main/kotlin") {
		t.Errorf("blur: editing %v, renamed %v", a.edit != nil, onDisk(dir, "src/main/kotlin"))
	}
	// Leaving a wrong name drops it, without a word.
	menu(t, tt, "kotlin", "Rename…")
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("..")
	click(t, tt, "resources")
	tt.Frame()
	if a.edit != nil || len(errs) != 1 || !onDisk(dir, "src/main/kotlin") {
		t.Errorf("blur on ..: editing %v, errors %q", a.edit != nil, errs)
	}
	// A new file named nothing makes nothing.
	menu(t, tt, "kotlin", "New File…")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if a.edit != nil || len(errs) != 1 {
		t.Errorf("empty name: editing %v, errors %q", a.edit != nil, errs)
	}
}

func TestDividerResizesAndResets(t *testing.T) {
	a, tt := openSample(t)
	r, ok := tt.Find("Divider") // the sidebar's, first
	if !ok {
		t.Fatal("no divider")
	}
	x, y := r.X+r.W/2, r.Y+r.H/2
	tt.Press(x, y)
	tt.Move(x+60, y)
	tt.Release(x+60, y)
	if a.sidebarW != sidebarDefault+60 {
		t.Errorf("dragged to %v", a.sidebarW)
	}
	tt.Press(x+60, y)
	tt.Move(x+2000, y)
	tt.Release(x+2000, y)
	if a.sidebarW != 600 {
		t.Errorf("past the limit: %v", a.sidebarW)
	}
	r, _ = tt.Find("Divider")
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	if a.sidebarW != sidebarDefault {
		t.Errorf("double-click left %v", a.sidebarW)
	}
}

func TestWindowStateRoundTrip(t *testing.T) {
	a, tt := openSample(t)
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Key(ui.Cmd, ui.KeyJ)
	a.bottomH = 300
	SaveState(a.State())
	st := LoadState()
	if st.Project != a.project || st.ShowSidebar || st.ShowBottom || st.BottomH != 300 {
		t.Fatalf("kept %+v", st)
	}
	b := newApp(st)
	tt = ui.NewTester(b.View, 1280, 800)
	if b.showSidebar || b.showBottom || tt.HasText("Terminal") {
		t.Error("the panels came back shown")
	}
}

// The sidebar hidden and shown again keeps what was open in the tree.
func TestSidebarComesBackAsLeft(t *testing.T) {
	a, tt := openSample(t)
	click(t, tt, "src", "main")
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if tt.HasText("main") {
		t.Fatal("tree shown with the sidebar hidden")
	}
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if !tt.HasText("resources") || a.selected != "src/main" {
		t.Errorf("tree after showing again: selected %q", a.selected)
	}
}

func TestRevealReadsFoldersAsTheyCome(t *testing.T) {
	a := newApp(defaultState)
	a.branch = "dev"
	h := &asyncHost{}
	a.host.Run = h.run
	a.OpenProject(sampleProject(t))
	tt := ui.NewTester(a.View, 1280, 800)
	h.flush(tt)
	a.tabs = append(a.tabs, &tab{path: "src/main/java/com/example/demo/HelloController.java", name: "HelloController.java", ed: nil})
	a.revealInTree(a.tabs[0].path)
	tt.Frame()
	// Each folder is read in turn, as the one above it comes.
	h.flush(tt)
	if a.revealing != "" || !tt.HasText("HelloController.java") || a.selected != a.tabs[0].path {
		t.Errorf("revealing %q, selected %q", a.revealing, a.selected)
	}
	// A file gone meanwhile stops the reveal.
	a.revealInTree("src/gone/X.java")
	tt.Frame()
	h.flush(tt)
	if a.revealing != "" {
		t.Errorf("still revealing %q", a.revealing)
	}
}

func TestLateReadsOfAnotherProjectDropped(t *testing.T) {
	a := newApp(defaultState)
	h := &asyncHost{}
	a.host.Run = h.run
	a.OpenProject(sampleProject(t))
	tt := ui.NewTester(a.View, 1280, 800)
	other := filepath.Join(t.TempDir(), "other")
	os.MkdirAll(other, 0o755)
	os.WriteFile(filepath.Join(other, "only.txt"), nil, 0o644)
	// The first project's read comes after the second opened.
	a.OpenProject(other)
	h.flush(tt)
	if len(a.root.children) != 1 || a.root.children[0].name != "only.txt" {
		t.Errorf("tree of other: %d children", len(a.root.children))
	}
}

func TestUnreadableFileStaysClosed(t *testing.T) {
	a, tt, dir := sampleWithMenu(t)
	a.host.ShowError = func(string, string) {}
	f := filepath.Join(dir, "pom.xml")
	os.Chmod(f, 0)
	defer os.Chmod(f, 0o644)
	if _, err := os.ReadFile(f); err == nil {
		t.Skip("the file reads without permission, as root")
	}
	click(t, tt, "pom.xml")
	if len(a.tabs) != 0 {
		t.Errorf("opened %d tabs", len(a.tabs))
	}
}
