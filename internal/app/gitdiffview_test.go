package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"megafactory/internal/git"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// runGit issues a git command in dir, failing the test when it does.
func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// diffRepo makes a temp repo with a committed file that is then edited:
// committed is its HEAD text, working the text now on disk.
func diffRepo(t *testing.T) (dir, rel, committed, working string) {
	t.Helper()
	dir = t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	rel = "hello.txt"
	committed = "one\ntwo\nthree\n"
	working = "one\nTWO\nthree\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "add", rel)
	runGit(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial")
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(working), 0o644); err != nil {
		t.Fatal(err)
	}
	return
}

// diffApp opens the repo with a file tab of the working text.
func diffApp(t *testing.T, dir, rel, working string) (*App, *tab) {
	t.Helper()
	a := newApp(defaultState)
	a.OpenProject(dir)
	repo, ok := git.Detect(dir)
	if !ok {
		t.Fatalf("git.Detect(%q) found no repo", dir)
	}
	// Keep the project and the repo at one root, so a.gitRel of the file
	// is exactly rel even where Detect resolves a symlinked temp path.
	a.project = repo.Dir
	a.git.repo, a.git.ok = repo, true
	tb := &tab{path: rel, name: rel, ed: &editor.State{Buffer: editor.NewBuffer(working)}}
	a.tabs = append(a.tabs, tb)
	a.active = 0
	return a, tb
}

func TestGitDiffBaseFromHEAD(t *testing.T) {
	dir, rel, committed, working := diffRepo(t)
	a, tb := diffApp(t, dir, rel, working)
	a.gitDiffBase(tb)
	if tb.ed.DiffBase == nil {
		t.Fatal("DiffBase is nil")
	}
	if got := tb.ed.DiffBase.String(); got != committed {
		t.Errorf("DiffBase = %q, want %q", got, committed)
	}
}

func TestOpenDiffShowsHeadAndWorking(t *testing.T) {
	dir, rel, committed, working := diffRepo(t)
	a, _ := diffApp(t, dir, rel, working)
	a.openDiff(rel)
	var dt *tab
	di := -1
	for i, tb := range a.tabs {
		if tb.diff != nil {
			dt, di = tb, i
			break
		}
	}
	if dt == nil {
		t.Fatal("openDiff opened no diff tab")
	}
	if got := dt.diff.Old.String(); got != committed {
		t.Errorf("Old = %q, want %q", got, committed)
	}
	if got := dt.diff.New.String(); got != working {
		t.Errorf("New = %q, want %q", got, working)
	}
	if dt.diffRel != rel || dt.name != filepath.Base(rel)+" (HEAD)" {
		t.Errorf("diff tab rel %q name %q", dt.diffRel, dt.name)
	}
	// The header of the active diff tab offers both layouts.
	a.active = di
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Frame()
	if !tt.HasText("Split") {
		t.Error("no Split button")
	}
	if !tt.HasText("Unified") {
		t.Error("no Unified button")
	}
}

// Stepping through the changes goes past the last one only on a second
// press, into the next changed file in the Changes page's order, and back.
func TestDiffStepAcrossFiles(t *testing.T) {
	dir := t.TempDir()
	runGit(t, dir, "init", "-q")
	runGit(t, dir, "config", "user.email", "test@example.com")
	runGit(t, dir, "config", "user.name", "Test")
	base := "1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n11\n12\n"
	edited := "1\nTWO\n3\n4\n5\n6\n7\n8\n9\n10\nELEVEN\n12\n"
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(base), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	runGit(t, dir, "add", ".")
	runGit(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-m", "initial")
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(edited), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	a, _ := diffApp(t, dir, "a.txt", edited)
	a.git.changes = []git.Status{{Path: "b.txt", Code: " M"}, {Path: "a.txt", Code: " M"}}
	a.openDiff("a.txt")
	dt := a.tabs[a.active]
	tabs := len(a.tabs)
	hunk := func() int { i, _ := dt.diff.CurrentHunk(); return i }

	if diffStepHunk(dt.diff, -1) >= 0 || a.diffNeighbor("a.txt", -1) != "" {
		t.Error("the first file's top can step back")
	}
	if hunk() != 0 {
		t.Fatalf("opened at hunk %d, want the first", hunk())
	}
	a.diffStep(dt, 1)
	if hunk() != 1 {
		t.Fatalf("after a step at hunk %d, want 1", hunk())
	}
	a.diffStep(dt, 1)
	if dt.diffRel != "a.txt" || !diffArmed(dt, 1) {
		t.Fatalf("a first press past the end left %q armed %v", dt.diffRel, diffArmed(dt, 1))
	}
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Frame()
	if !tt.HasText("Click again for next file") {
		t.Error("no hint for the second press")
	}
	a.diffStep(dt, 1)
	if dt.diffRel != "b.txt" || dt.path != diffPath("b.txt") || hunk() != 0 || len(a.tabs) != tabs {
		t.Fatalf("second press: rel %q hunk %d tabs %d", dt.diffRel, hunk(), len(a.tabs))
	}
	if diffStepHunk(dt.diff, 1) < 0 || a.diffNeighbor("b.txt", 1) != "" {
		t.Error("b.txt should step on to its second change and have no next file")
	}
	a.diffStep(dt, -1)
	a.diffStep(dt, -1)
	if dt.diffRel != "a.txt" || hunk() != 1 {
		t.Fatalf("back: rel %q hunk %d, want a.txt at its last change", dt.diffRel, hunk())
	}
}

// Jump to Source opens the working file at the caret's line, mapping a line
// of the old side past the lines the changes above it added.
func TestDiffJumpToSource(t *testing.T) {
	dir, rel, _, _ := diffRepo(t)
	working := "one\nNEW\nMORE\nTWO\nthree\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(working), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := diffApp(t, dir, rel, working)
	a.tabs = nil // the file opens from the jump
	a.openDiff(rel)
	dt := a.tabs[a.active]
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Frame()

	// The caret on the old side's "three", its line 2, is on the working
	// file's line 4: the change above it turned one line into three.
	var old ui.Rect
	for _, n := range tt.AccessAll() {
		if n.Role == ui.RoleTextField && n.Bounds.X < 400 && n.Bounds.W > 0 {
			old = n.Bounds
		}
	}
	if old.W == 0 {
		t.Fatal("no old side")
	}
	top := float32(dt.diff.LineTop(editor.DiffOld, 2) - dt.diff.ScrollY())
	tt.ClickAt(old.X+old.W-20, old.Y+top+4)
	tt.Frame()
	if dt.diff.ActiveSide() != editor.DiffOld || dt.diff.CursorPos().Line != 2 {
		t.Fatalf("the click left side %v line %d", dt.diff.ActiveSide(), dt.diff.CursorPos().Line)
	}
	if got := diffSourceLine(dt.diff); got != 4 {
		t.Errorf("old side line 2 maps to %d, want 4", got)
	}
	src, ok := tt.AccessByRole(ui.RoleButton, "Jump to Source")
	if !ok {
		t.Fatal("no Jump to Source button")
	}
	tt.ClickAt(src.Bounds.X+src.Bounds.W/2, src.Bounds.Y+src.Bounds.H/2)
	tt.Frame()
	tt.Frame()
	ft := a.tabs[a.active]
	if ft.path != rel || ft.ed == nil {
		t.Fatalf("the jump showed %q", ft.path)
	}
	if line := ft.ed.CursorPos().Line; line != 4 {
		t.Errorf("the file's caret is on line %d, want 4", line)
	}
}

// A diff opens with the caret on its first change, rather than at the top.
func TestOpenDiffGoesToFirstChange(t *testing.T) {
	dir, rel, _, _ := diffRepo(t)
	var lines, edited []string
	for i := range 300 {
		lines = append(lines, strconv.Itoa(i))
	}
	edited = append(edited, lines...)
	edited[200] = "changed"
	committed := strings.Join(lines, "\n") + "\n"
	working := strings.Join(edited, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, dir, "-c", "commit.gpgsign=false", "commit", "-q", "-am", "long")
	if err := os.WriteFile(filepath.Join(dir, rel), []byte(working), 0o644); err != nil {
		t.Fatal(err)
	}
	a, _ := diffApp(t, dir, rel, working)
	a.openDiff(rel)
	dt := a.tabs[a.active]
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Frame()
	tt.Frame()
	if i, inside := dt.diff.CurrentHunk(); i != 0 || !inside {
		t.Errorf("the caret is in hunk %d (inside %v), want the first", i, inside)
	}
	if line := dt.diff.CursorPos().Line; line != 200 {
		t.Errorf("the caret is on line %d, want 200", line)
	}
}
