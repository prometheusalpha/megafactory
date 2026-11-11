package git

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func gitCmd(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func writeFile(t *testing.T, dir, rel, text string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// realPath normalizes symlinked temp dirs (macOS /var -> /private/var) so a
// path printed by git can be compared with t.TempDir().

func realPath(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// newTestRepo builds a small repository: two commits, a tag, a second branch,
// a couple of remote-tracking refs, a modified tracked file and an untracked
// file. Each test gets its own copy so checkouts don't interfere.
func newTestRepo(t *testing.T) (string, Repo) {
	t.Helper()
	dir := t.TempDir()
	if out, err := exec.Command("git", "-C", dir, "init", "-q").CombinedOutput(); err != nil {
		t.Skipf("git: %v %s", err, out)
	}
	gitCmd(t, dir, "config", "user.name", "Mega Tester")
	gitCmd(t, dir, "config", "user.email", "mega@example.com")
	gitCmd(t, dir, "config", "commit.gpgsign", "false")
	gitCmd(t, dir, "symbolic-ref", "HEAD", "refs/heads/main")
	gitCmd(t, dir, "remote", "add", "origin", "https://example.invalid/repo.git")

	writeFile(t, dir, "hello.txt", "one\ntwo\nthree\n")
	gitCmd(t, dir, "add", "hello.txt")
	gitCmd(t, dir, "commit", "-q", "-m", "first commit")
	writeFile(t, dir, "hello.txt", "one\ntwo\nthree\nfour\n")
	gitCmd(t, dir, "add", "hello.txt")
	gitCmd(t, dir, "commit", "-q", "-m", "second commit")
	gitCmd(t, dir, "tag", "v1")
	gitCmd(t, dir, "branch", "feature")

	gitCmd(t, dir, "update-ref", "refs/remotes/origin/feature", "HEAD")
	gitCmd(t, dir, "update-ref", "refs/remotes/origin/onlyremote", "HEAD")
	gitCmd(t, dir, "update-ref", "refs/remotes/origin/HEAD", "HEAD") // skipped by Refs

	writeFile(t, dir, "hello.txt", "one\ntwo\nthree\nfour changed\n")
	writeFile(t, dir, "new.txt", "untracked\n")
	return dir, Repo{Dir: dir}
}

func TestDetect(t *testing.T) {
	dir, _ := newTestRepo(t)
	top, ok := Detect(dir)
	if !ok || realPath(top.Dir) != realPath(dir) {
		t.Fatalf("Detect(%q) = %+v, %v; want %q, true", dir, top, ok, dir)
	}
	sub := filepath.Join(dir, "src", "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	top, ok = Detect(sub)
	if !ok || realPath(top.Dir) != realPath(dir) {
		t.Fatalf("Detect(subdir) = %+v, %v; want root %q", top, ok, dir)
	}
	if _, ok := Detect(t.TempDir()); ok {
		t.Error("Detect(non-repo) = ok, want false")
	}
}

func TestBranchHead(t *testing.T) {
	dir, repo := newTestRepo(t)
	branch, err := repo.Branch()
	if err != nil || branch != "main" {
		t.Fatalf("Branch() = %q, %v; want main", branch, err)
	}
	head, err := repo.Head()
	if err != nil {
		t.Fatal(err)
	}
	want := strings.TrimSpace(gitCmd(t, dir, "rev-parse", "HEAD"))
	if head != want {
		t.Fatalf("Head() = %q, want %q", head, want)
	}
}

func TestStatuses(t *testing.T) {
	_, repo := newTestRepo(t)
	st, err := repo.Statuses()
	if err != nil {
		t.Fatal(err)
	}
	byPath := map[string]Status{}
	for _, s := range st {
		byPath[s.Path] = s
	}
	if s, ok := byPath["hello.txt"]; !ok || s.Kind() != Modified {
		t.Errorf("hello.txt = %+v (present %v), want Modified", s, ok)
	}
	if s, ok := byPath["new.txt"]; !ok || s.Kind() != Untracked || s.Code != "??" {
		t.Errorf("new.txt = %+v (present %v), want Untracked ??", s, ok)
	}
}

func TestStatusesRename(t *testing.T) {
	dir, repo := newTestRepo(t)
	writeFile(t, dir, "old.txt", "content\n")
	gitCmd(t, dir, "add", "old.txt")
	gitCmd(t, dir, "commit", "-q", "-m", "add old")
	gitCmd(t, dir, "mv", "old.txt", "renamed.txt")
	st, err := repo.Statuses()
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, s := range st {
		if s.Path == "renamed.txt" {
			found++
			if s.Kind() != Renamed {
				t.Errorf("renamed.txt Kind = %v, want Renamed", s.Kind())
			}
		}
		if s.Path == "old.txt" {
			t.Errorf("origin path old.txt leaked as a status: %+v", s)
		}
	}
	if found != 1 {
		t.Errorf("renamed.txt found %d times in %+v, want 1", found, st)
	}
}

func TestKind(t *testing.T) {
	cases := map[string]Kind{
		" M": Modified,
		"M ": Modified,
		"MM": Modified,
		"??": Untracked,
		"A ": Added,
		" A": Added,
		" D": Deleted,
		"D ": Deleted,
		"R ": Renamed,
		"C ": Renamed,
		" R": Renamed,
		"UU": Conflict,
		"AA": Conflict,
		"DD": Conflict,
		"AU": Conflict,
		"UD": Conflict,
	}
	for code, want := range cases {
		if got := (Status{Code: code}).Kind(); got != want {
			t.Errorf("Kind(%q) = %v, want %v", code, got, want)
		}
	}
}

func TestShow(t *testing.T) {
	_, repo := newTestRepo(t)
	text, ok, err := repo.Show("HEAD", "hello.txt")
	if err != nil || !ok {
		t.Fatalf("Show(HEAD, hello.txt) ok=%v err=%v", ok, err)
	}
	if text != "one\ntwo\nthree\nfour\n" {
		t.Errorf("Show text = %q", text)
	}
	for _, rel := range []string{"new.txt", "missing.txt"} {
		text, ok, err = repo.Show("HEAD", rel)
		if err != nil || ok || text != "" {
			t.Errorf("Show(HEAD, %q) = %q, %v, %v; want empty, false, nil", rel, text, ok, err)
		}
	}
}

func TestBlame(t *testing.T) {
	_, repo := newTestRepo(t)
	lines, err := repo.Blame("hello.txt")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 4 {
		t.Fatalf("Blame returned %d lines, want 4: %+v", len(lines), lines)
	}
	for i, l := range lines {
		if l.Line != i+1 {
			t.Errorf("line %d has Line=%d, want %d", i, l.Line, i+1)
		}
	}
	first := lines[0]
	if first.Author != "Mega Tester" {
		t.Errorf("author = %q, want Mega Tester", first.Author)
	}
	if len(first.Commit) != 8 {
		t.Errorf("Commit = %q, want 8 chars", first.Commit)
	}
	if first.Time.IsZero() {
		t.Error("committed line has zero Time")
	}
	if first.Summary != "first commit" {
		t.Errorf("summary = %q, want first commit", first.Summary)
	}
	if !lines[3].Boundary {
		t.Error("uncommitted line 4 should be Boundary")
	}
}

func TestRefs(t *testing.T) {
	_, repo := newTestRepo(t)
	refs, err := repo.Refs()
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Ref{}
	for _, r := range refs {
		byName[r.Name] = r
	}
	for name, want := range map[string]RefKind{
		"main":              LocalBranch,
		"feature":           LocalBranch,
		"origin/feature":    RemoteBranch,
		"origin/onlyremote": RemoteBranch,
		"v1":                Tag,
	} {
		r, ok := byName[name]
		if !ok || r.Kind != want {
			t.Errorf("ref %q = %+v (present %v), want Kind %v", name, r, ok, want)
		}
		if ok && r.Full == "" {
			t.Errorf("ref %q has empty Full", name)
		}
	}
	if r, ok := byName["feature"]; !ok || r.Full != "refs/heads/feature" {
		t.Errorf("feature Full = %q, want refs/heads/feature", r.Full)
	}
	if _, ok := byName["origin/HEAD"]; ok {
		t.Error("origin/HEAD should be skipped")
	}
	last := RefKind(0)
	for _, r := range refs {
		if r.Kind < last {
			t.Errorf("refs not grouped in kind order: %+v", refs)
			break
		}
		last = r.Kind
	}
}

func TestDirtyStash(t *testing.T) {
	_, repo := newTestRepo(t)
	dirty, err := repo.Dirty()
	if err != nil || !dirty {
		t.Fatalf("Dirty() = %v, %v; want true", dirty, err)
	}
	if err := repo.Stash("megafactory test"); err != nil {
		t.Fatal(err)
	}
	dirty, err = repo.Dirty()
	if err != nil || dirty {
		t.Fatalf("Dirty() after stash = %v, %v; want false", dirty, err)
	}
}

func TestCreateBranch(t *testing.T) {
	_, repo := newTestRepo(t)
	if err := repo.CreateBranch("dev"); err != nil {
		t.Fatal(err)
	}
	branch, err := repo.Branch()
	if err != nil || branch != "dev" {
		t.Fatalf("Branch() = %q, %v; want dev", branch, err)
	}
}

func TestCheckoutRevision(t *testing.T) {
	dir, repo := newTestRepo(t)
	gitCmd(t, dir, "checkout", "--", "hello.txt") // clean the working tree
	before, _ := repo.Head()
	if err := repo.CheckoutRevision("HEAD~1"); err != nil {
		t.Fatal(err)
	}
	after, _ := repo.Head()
	if before == after || after == "" {
		t.Fatalf("CheckoutRevision did not move HEAD: %q -> %q", before, after)
	}
	branch, err := repo.Branch()
	if err != nil || branch == "main" || branch == "" {
		t.Fatalf("detached Branch() = %q, %v; want a short hash", branch, err)
	}
}

func TestCheckout(t *testing.T) {
	dir, repo := newTestRepo(t)
	gitCmd(t, dir, "checkout", "--", "hello.txt") // clean the working tree

	local := Ref{Name: "feature", Full: "refs/heads/feature", Kind: LocalBranch}
	if err := repo.Checkout(local); err != nil {
		t.Fatal(err)
	}
	branch, _ := repo.Branch()
	if branch != "feature" {
		t.Fatalf("after local checkout Branch() = %q, want feature", branch)
	}

	tag := Ref{Name: "v1", Full: "refs/tags/v1", Kind: Tag}
	if err := repo.Checkout(tag); err != nil {
		t.Fatal(err)
	}

	// A remote whose short local name already exists checks out that branch.
	remote := Ref{Name: "origin/feature", Full: "refs/remotes/origin/feature", Kind: RemoteBranch}
	if err := repo.Checkout(remote); err != nil {
		t.Fatal(err)
	}
	branch, _ = repo.Branch()
	if branch != "feature" {
		t.Errorf("after remote checkout Branch() = %q, want feature", branch)
	}

	// A remote with no local counterpart must still check out cleanly (either
	// git's DWIM or the -b --track fallback).
	only := Ref{Name: "origin/onlyremote", Full: "refs/remotes/origin/onlyremote", Kind: RemoteBranch}
	if err := repo.Checkout(only); err != nil {
		t.Fatal(err)
	}
}

func TestRecentBranches(t *testing.T) {
	dir, repo := newTestRepo(t)
	// Fresh repo: nothing was checked out yet.
	if got, err := repo.RecentBranches(); err != nil || len(got) != 0 {
		t.Fatalf("fresh repo: %q, %v", got, err)
	}
	gitCmd(t, dir, "checkout", "-q", "feature")
	gitCmd(t, dir, "checkout", "-q", "main")
	gitCmd(t, dir, "checkout", "-q", "feature")
	got, err := repo.RecentBranches()
	if err != nil {
		t.Fatal(err)
	}
	// Newest first, each branch once.
	if want := []string{"feature", "main"}; !slices.Equal(got, want) {
		t.Errorf("RecentBranches = %q, want %q", got, want)
	}
}
