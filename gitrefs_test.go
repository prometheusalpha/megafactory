package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"megafactory/internal/git"

	"github.com/egoist/mygo/ui"
)

// refsRunGit runs a git command in dir with a fixed identity and no global
// config, so commits are deterministic on any machine.
func refsRunGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_AUTHOR_NAME=Test", "GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test", "GIT_COMMITTER_EMAIL=test@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func refsInitRepo(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	refsRunGit(t, dir, "init", "-b", "main")
	refsRunGit(t, dir, "config", "user.name", "Test")
	refsRunGit(t, dir, "config", "user.email", "test@example.com")
}

// refsGitRepo builds a repo with two local branches (main, feature), a tag
// (v1) and a remote-tracking branch (origin/main).
func refsGitRepo(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo")
	refsInitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refsRunGit(t, dir, "add", "f.txt")
	refsRunGit(t, dir, "commit", "-m", "first")
	refsRunGit(t, dir, "tag", "v1")
	refsRunGit(t, dir, "branch", "feature")
	refsRunGit(t, dir, "update-ref", "refs/remotes/origin/main", "HEAD")
	return dir
}

// refsTestApp opens dir as a project with blame off, so a.view renders no
// editor/blame code for the empty tab list.
func refsTestApp(t *testing.T, dir string) *app {
	t.Helper()
	old := prefs
	prefs.GutterBlame, prefs.InlineBlame = false, false
	t.Cleanup(func() { prefs = old })
	a := newApp(defaultState)
	a.openProject(dir)
	repo, ok := git.Detect(dir)
	if !ok {
		t.Fatalf("git.Detect found no repo in %s", dir)
	}
	a.git.repo, a.git.ok = repo, ok
	return a
}

func TestBranchPickerListsAndFilters(t *testing.T) {
	dir := refsGitRepo(t)
	a := refsTestApp(t, dir)
	a.openBranchPicker()
	tt := ui.NewTester(a.view, 1280, 800)
	if a.git.picker == nil || !a.git.picker.open {
		t.Fatal("branch picker did not open")
	}
	// The dialog title, the local branches and the tag are all listed.
	for _, want := range []string{"Branches", "main", "feature", "v1"} {
		if !tt.HasText(want) {
			t.Errorf("picker does not show %q", want)
		}
	}
	// A query matching nothing removes every ref; the raw-revision action
	// takes their place. Setting query directly keeps the bound-value frame
	// lag out of the assertion.
	tt.Type("zzz")
	if a.git.picker == nil {
		t.Fatal("picker closed while typing")
	}
	a.git.picker.query = "zzz"
	tt.Frame()
	// "main" is never checked here: gitRefresh put the real branch in the
	// status bar chip, so HasText("main") stays true regardless of filtering.
	for _, gone := range []string{"feature", "origin", "v1"} {
		if tt.HasText(gone) {
			t.Errorf("filter kept %q", gone)
		}
	}
	for _, gone := range []string{"Local Branches", "Tags"} {
		if tt.HasText(gone) {
			t.Errorf("group header %q still shown with no matching refs", gone)
		}
	}
	if !tt.HasText("Checkout revision") {
		t.Error("no checkout-revision action for a typed query")
	}
}

func TestBranchPickerCreatesBranch(t *testing.T) {
	dir := refsGitRepo(t)
	a := refsTestApp(t, dir)
	a.openBranchPicker()
	tt := ui.NewTester(a.view, 1280, 800)
	if !tt.HasText("Branches") {
		t.Fatal("branch picker not shown")
	}
	if a.git.picker == nil {
		t.Fatal("branch picker did not open")
	}
	a.createBranch("feature-x")
	if out := refsRunGit(t, dir, "branch", "--list", "feature-x"); !strings.Contains(out, "feature-x") {
		t.Errorf("branch not created: %q", out)
	}
	if a.git.picker != nil {
		t.Error("picker still open after creating a branch")
	}
}

func TestCheckoutStashesDirtyTree(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "repo")
	refsInitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refsRunGit(t, dir, "add", "f.txt")
	refsRunGit(t, dir, "commit", "-m", "first")
	// "other" changes f.txt in a second commit, so a local edit to f.txt is
	// exactly the conflict that makes git refuse the checkout.
	refsRunGit(t, dir, "checkout", "-b", "other")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	refsRunGit(t, dir, "commit", "-am", "second")
	secondHash := strings.TrimSpace(refsRunGit(t, dir, "rev-parse", "HEAD"))
	refsRunGit(t, dir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("local edit\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	a := refsTestApp(t, dir)
	refs, err := a.git.repo.Refs()
	if err != nil {
		t.Fatal(err)
	}
	var other git.Ref
	for _, r := range refs {
		if r.Kind == git.LocalBranch && r.Name == "other" {
			other = r
		}
	}
	if other.Name == "" {
		t.Fatal("no 'other' branch ref")
	}

	asked := false
	var q question
	a.host.ask = func(qq question, done func(int)) {
		asked, q = true, qq
		done(0) // Stash & Checkout
	}
	a.checkoutRef(other)

	if !asked {
		t.Fatal("no stash offer for a dirty checkout")
	}
	if q.message != "Checkout blocked by local changes" {
		t.Errorf("message %q", q.message)
	}
	if !strings.Contains(q.detail, "f.txt") || !strings.Contains(q.detail, "Stash your changes and check out anyway?") {
		t.Errorf("detail lost git's error or the stash offer: %q", q.detail)
	}
	if len(q.buttons) != 2 || q.buttons[0] != "Stash & Checkout" || q.buttons[1] != "Cancel" {
		t.Errorf("buttons %q", q.buttons)
	}
	if out := refsRunGit(t, dir, "stash", "list"); !strings.Contains(out, "megafactory: before checkout") {
		t.Errorf("no stash created: %q", out)
	}
	if head := strings.TrimSpace(refsRunGit(t, dir, "rev-parse", "HEAD")); head != secondHash {
		t.Errorf("retry checkout landed on %q, want %q", head, secondHash)
	}
	// gitRefresh applied the new state after the checkout succeeded.
	if a.branch == "dev" {
		t.Error("gitRefresh did not run after the checkout")
	}
}
