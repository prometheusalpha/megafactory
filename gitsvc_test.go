package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// gitTestRepo builds a real repository with two commits, a modified file
// and an untracked one, and returns its root and the branch's name.
func gitTestRepo(t *testing.T) (dir, branch string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir = t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	write := func(rel, text string) {
		t.Helper()
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@megafactory.local")
	run("config", "user.name", "Test")
	write("tracked.txt", "one\n")
	write("sub/nested.txt", "nested\n")
	run("add", "-A")
	run("commit", "-q", "-m", "first")
	write("second.txt", "two\n")
	run("add", "-A")
	run("commit", "-q", "-m", "second")
	// A branch of our own, so the name does not depend on git's default.
	run("checkout", "-q", "-b", "mf-work")
	// The working tree: tracked.txt is modified, untracked.txt is new.
	write("tracked.txt", "one\nchanged\n")
	write("untracked.txt", "new\n")
	out, err := exec.Command("git", "-C", dir, "symbolic-ref", "--short", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

func TestGitServiceDetectsRepo(t *testing.T) {
	dir, branch := gitTestRepo(t)
	a := newApp(defaultState)
	a.branch = "dev" // gitRefresh must replace it with the real branch
	a.openProject(dir)
	a.sidePane = 1 // the sidebar's Git pane
	tt := ui.NewTester(a.view, 1280, 800)

	// newApp's host runs each job's I/O and apply in place, so the read is
	// done by the time gitProjectOpened returns; a Frame shows the result.
	a.gitProjectOpened()
	tt.Frame()

	if !a.git.ok {
		t.Fatalf("no repository detected in %s", dir)
	}
	wantDir, err := filepath.EvalSymlinks(dir)
	if err != nil {
		wantDir = dir
	}
	gotDir, err := filepath.EvalSymlinks(a.git.repo.Dir)
	if err != nil {
		gotDir = a.git.repo.Dir
	}
	if gotDir != wantDir {
		t.Errorf("repo dir = %q, want %q", a.git.repo.Dir, dir)
	}
	if a.branch != branch {
		t.Errorf("branch = %q, want %q", a.branch, branch)
	}
	got := map[string]bool{}
	for _, s := range a.git.changes {
		got[s.Path] = true
	}
	if !got["tracked.txt"] || !got["untracked.txt"] {
		t.Errorf("changes = %v, want the modified and untracked files", got)
	}
	if !tt.HasText("Changes") || !tt.HasText("tracked.txt") || !tt.HasText("untracked.txt") {
		t.Errorf("Git pane does not list the changes: %v", tt.Texts())
	}
	// Project-relative and repo-root-relative paths round-trip; a path
	// outside the project has no project-relative name.
	for _, rel := range []string{"tracked.txt", "sub/nested.txt"} {
		if g := a.gitRel(rel); g != rel {
			t.Errorf("gitRel(%q) = %q", rel, g)
		}
		if p := a.projectRel(rel); p != rel {
			t.Errorf("projectRel(%q) = %q", rel, p)
		}
	}
	if p := a.projectRel("../outside.txt"); p != "" {
		t.Errorf("projectRel of an outside path = %q, want empty", p)
	}
}

func TestGitServiceNoRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir := t.TempDir() // a folder with no .git above it
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := newApp(defaultState)
	a.branch = "dev"
	a.project = dir
	a.gitProjectOpened()
	if a.git.ok {
		t.Error("detected a repository where there is none")
	}
	if a.branch != "dev" {
		t.Errorf("branch changed without a repository: %q", a.branch)
	}
	if a.git.changes != nil || a.git.head != "" || a.git.err != nil {
		t.Errorf("stale git state: %+v", a.git)
	}
}

func TestGitBranchChip(t *testing.T) {
	dir, branch := gitTestRepo(t)
	a := newApp(defaultState)
	a.project = dir
	a.gitProjectOpened()
	if !a.git.ok {
		t.Fatal("no repository detected")
	}
	// A view of just the chip keeps the test on the chip itself.
	view := func(c *ui.Context) { a.gitBranchChip(c, colorsOf(c)) }
	tt := ui.NewTester(view, 320, 40)
	if !tt.HasText(branch) {
		t.Errorf("chip shows no branch %q: %v", branch, tt.Texts())
	}
	if err := tt.Click("Current branch"); err != nil {
		t.Fatalf("chip is not a button: %v", err)
	}
	tt.Frame()
	if a.git.picker == nil {
		t.Error("the chip did not open the branch picker")
	}

	// Without a repository the chip reports the error instead of choosing.
	b := newApp(defaultState)
	b.branch = "dev"
	var title string
	b.host.showError = func(t, _ string) { title = t }
	norepo := func(c *ui.Context) { b.gitBranchChip(c, colorsOf(c)) }
	nt := ui.NewTester(norepo, 320, 40)
	if err := nt.Click("Current branch"); err != nil {
		t.Fatalf("chip is not a button without a repository: %v", err)
	}
	if title != "Not a git repository" {
		t.Errorf("showError title = %q, want %q", title, "Not a git repository")
	}
}
