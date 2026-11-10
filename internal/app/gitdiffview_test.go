package app

import (
	"os"
	"os/exec"
	"path/filepath"
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
