package app

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
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

// refsTestApp opens dir as a project with blame off, so a.View renders no
// editor/blame code for the empty tab list.
func refsTestApp(t *testing.T, dir string) *App {
	t.Helper()
	old := prefs
	prefs.GutterBlame, prefs.InlineBlame = false, false
	t.Cleanup(func() { prefs = old })
	a := newApp(defaultState)
	a.OpenProject(dir)
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
	tt := ui.NewTester(a.View, 1280, 800)
	if a.git.picker == nil || !a.git.picker.open {
		t.Fatal("branch picker did not open")
	}
	// Sections start collapsed, so only their headers are listed.
	for _, want := range []string{"Branches", "Local Branches", "Remote Branches · origin", "Tags"} {
		if !tt.HasText(want) {
			t.Errorf("picker does not show %q", want)
		}
	}
	if tt.HasText("v1") {
		t.Error("tags listed under a collapsed section")
	}
	// Opening the sections lists the local branches and the tag.
	a.git.picker.toggled = map[string]bool{"sec:local": true, "sec:tags": true}
	tt.Frame()
	for _, want := range []string{"main", "feature", "v1"} {
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
	tt := ui.NewTester(a.View, 1280, 800)
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
	var q Question
	a.host.Ask = func(qq Question, done func(int)) {
		asked, q = true, qq
		done(0) // Stash & Checkout
	}
	a.checkoutRef(other)

	if !asked {
		t.Fatal("no stash offer for a dirty checkout")
	}
	if q.Message != "Checkout blocked by local changes" {
		t.Errorf("message %q", q.Message)
	}
	if !strings.Contains(q.Detail, "f.txt") || !strings.Contains(q.Detail, "Stash your changes and check out anyway?") {
		t.Errorf("detail lost git's error or the stash offer: %q", q.Detail)
	}
	if len(q.Buttons) != 2 || q.Buttons[0] != "Stash & Checkout" || q.Buttons[1] != "Cancel" {
		t.Errorf("buttons %q", q.Buttons)
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

// labels flattens items to "depth:label" strings, section and folder rows
// marked with a trailing slash, to compare whole trees at once.
func labels(items []pickerItem) []string {
	var out []string
	for _, it := range items {
		l := strings.Repeat(" ", it.depth) + it.label
		if it.kind == itemSection || it.kind == itemFolder {
			l += "/"
		}
		out = append(out, l)
	}
	return out
}

func treeApp(refs ...git.Ref) (*App, *branchPicker) {
	a := newApp(defaultState)
	a.git.refs = refs
	return a, &branchPicker{open: true}
}

func local(name string) git.Ref {
	return git.Ref{Name: name, Full: "refs/heads/" + name, Kind: git.LocalBranch}
}

func remote(name string) git.Ref {
	return git.Ref{Name: name, Full: "refs/remotes/" + name, Kind: git.RemoteBranch}
}

func TestPickerTreeGroupsBySlash(t *testing.T) {
	a, p := treeApp(local("main"), local("feat/a/b"), local("feat/a/c"), local("feat/d"),
		remote("origin/feat/a/b"), remote("upstream/x"))
	// Everything starts collapsed: the sections only.
	got := labels(a.pickerItems(p))
	want := []string{"Local Branches/", "Remote Branches · origin/", "Remote Branches · upstream/", "Create new branch from HEAD…"}
	if !slices.Equal(got, want) {
		t.Errorf("collapsed = %q, want %q", got, want)
	}
	// Opening Local lists folders before branches, each folder collapsed.
	p.toggled = map[string]bool{"sec:local": true}
	got = labels(a.pickerItems(p))
	want = []string{"Local Branches/", " feat/", " main", "Remote Branches · origin/"}
	if len(got) < 4 || !slices.Equal(got[:4], want) {
		t.Errorf("local opened = %q, want %q first", got, want)
	}
	// a/b/c is a tree: feat -> a -> b.
	p.toggled["dir:sec:local\x00feat"] = true
	p.toggled["dir:sec:local\x00feat/a"] = true
	got = labels(a.pickerItems(p))
	want = []string{"Local Branches/", " feat/", "  a/", "   b", "   c", "  d", " main"}
	if len(got) < len(want) || !slices.Equal(got[:len(want)], want) {
		t.Errorf("feat opened = %q, want %q first", got, want)
	}
	// A remote's section drops the remote's name from its branches.
	p.toggled["sec:remote:upstream"] = true
	for _, l := range labels(a.pickerItems(p)) {
		if l == " upstream/" || l == " x" {
			return
		}
	}
	t.Errorf("upstream section lists no x: %q", labels(a.pickerItems(p)))
}

func TestPickerSearchOpensEverythingMatching(t *testing.T) {
	a, p := treeApp(local("main"), local("feat/a/b"), remote("origin/feat/a/zed"))
	p.query = "FEAT/A"
	got := labels(a.pickerItems(p))
	want := []string{
		"Local Branches/", " feat/", "  a/", "   b",
		"Remote Branches · origin/", " feat/", "  a/", "   zed",
		// "Create new branch" does not match the query: only the revision
		// action follows the refs.
		"Checkout revision FEAT/A…",
	}
	if !slices.Equal(got, want) {
		t.Errorf("search = %q, want %q", got, want)
	}
}

// Foo and foo are different branches to git, so the tree keeps them apart.
func TestPickerTreeKeepsCaseVariantsApart(t *testing.T) {
	a, p := treeApp(local("Foo/x"), local("foo/y"))
	p.toggled = map[string]bool{"sec:local": true}
	got := labels(a.pickerItems(p))
	if got[1] != " Foo/" || got[2] != " foo/" {
		t.Errorf("case variants merged or lost: %q", got)
	}
	// Searching in lower case still finds both.
	p.query = "foo"
	if got := labels(a.pickerItems(p)); !slices.Equal(got[:6], []string{"Local Branches/", " Foo/", "  x", " foo/", "  y", "Checkout revision foo…"}) {
		t.Errorf("search = %q", got)
	}
}

func TestPickerRecentIsOpenAndSkipsCurrent(t *testing.T) {
	a, p := treeApp(local("main"), local("feature"), local("old"))
	a.branch = "main"
	p.recent = []string{"main", "feature", "gone", "old"}
	got := labels(a.pickerItems(p))
	if !slices.Equal(got[:3], []string{"Recent/", " feature", " old"}) {
		t.Errorf("recent = %q", got)
	}
	// While searching, Recent stays out: Local lists those branches.
	p.query = "feature"
	if got := labels(a.pickerItems(p)); got[0] != "Local Branches/" {
		t.Errorf("search listed Recent: %q", got)
	}
}

func TestCaseClash(t *testing.T) {
	refs := []git.Ref{local("feature/x"), local("Main"), remote("origin/Remote")}
	for name, want := range map[string]string{
		"main":         "Main",
		"Feature/y":    "feature/x",
		"feature/X":    "feature/x",
		"feature/x":    "", // same name: git itself refuses it
		"feature":      "",
		"other/Main":   "",
		"remote":       "", // only local branches are files under refs/heads
		"Main/sub":     "", // Main is a branch: git refuses the folder itself
		"feature/x/yz": "",
	} {
		if got := caseClash(name, refs); got != want {
			t.Errorf("caseClash(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestRequestCreateBranchAsksOnCaseClash(t *testing.T) {
	dir := refsGitRepo(t)
	a := refsTestApp(t, dir)
	refs, err := a.git.repo.Refs()
	if err != nil {
		t.Fatal(err)
	}
	a.git.refs = refs
	var q Question
	asked := 0
	choice := 1 // Cancel
	a.host.Ask = func(qq Question, done func(int)) {
		asked, q = asked+1, qq
		done(choice)
	}
	a.requestCreateBranch("Feature")
	if asked != 1 || !strings.Contains(q.Detail, "“feature”") {
		t.Fatalf("asked %d times, detail %q", asked, q.Detail)
	}
	if out := refsRunGit(t, dir, "branch", "--list", "Feature"); strings.TrimSpace(out) != "" {
		t.Errorf("branch created despite Cancel: %q", out)
	}
	// An unrelated name needs no question.
	a.requestCreateBranch("unrelated")
	if asked != 1 {
		t.Errorf("asked about a name with no clash")
	}
}
