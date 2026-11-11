// Package git shells out to the git CLI to read a repository's state. Every
// path it takes is relative to the repository root, with forward slashes.
package git

import (
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Repo is a working tree; Dir is its top-level directory.
type Repo struct{ Dir string }

// Detect finds the repository containing dir. The returned Dir is the
// top-level directory, which may be an ancestor of dir.
func Detect(dir string) (Repo, bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").CombinedOutput()
	if err != nil {
		return Repo{}, false
	}
	return Repo{Dir: strings.TrimSpace(string(out))}, true
}

// run invokes git in r.Dir, keeping git's own error output in the error.
func (r Repo) run(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", r.Dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return out, fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return out, nil
}

// Kind is how a path changed, read from a status code.
type Kind uint8

const (
	Modified Kind = iota
	Added
	Deleted
	Untracked
	Renamed
	Conflict
)

// Status is one changed path. Code is git's two-character XY status.
type Status struct{ Path, Code string }

// Kind classifies the change. Conflict wins over rename over deletion over
// addition; anything else is a modification.
func (s Status) Kind() Kind {
	switch {
	case s.Code == "??":
		return Untracked
	case strings.Contains(s.Code, "U") || s.Code == "AA" || s.Code == "DD":
		return Conflict
	case strings.ContainsAny(s.Code, "RC"):
		return Renamed
	case strings.Contains(s.Code, "D"):
		return Deleted
	case strings.Contains(s.Code, "A"):
		return Added
	default:
		return Modified
	}
}

// BlameLine is one line's provenance. Line is 1-based.
type BlameLine struct {
	Line     int
	Author   string
	Time     time.Time
	Summary  string
	Commit   string
	Boundary bool
}

// RefKind is the kind of a ref a checkout can target.
type RefKind uint8

const (
	LocalBranch RefKind = iota
	RemoteBranch
	Tag
)

// Ref is a branch or tag. Full is the full refname, Name the short one.
type Ref struct {
	Name, Full string
	Kind       RefKind
}

// Branch is the current branch name, or a short hash on a detached HEAD.
func (r Repo) Branch() (string, error) {
	out, err := r.run("symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		// Detached HEAD: symbolic-ref fails, fall back to the commit hash.
		out, err = r.run("rev-parse", "--short", "HEAD")
		if err != nil {
			return "", err
		}
	}
	return strings.TrimSpace(string(out)), nil
}

// Head is the full hash of the current commit.
func (r Repo) Head() (string, error) {
	out, err := r.run("rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// Statuses lists changed paths, repo-root-relative with forward slashes. In
// -z form a rename/copy emits an extra bare record holding the origin path;
// consume it and keep the new path as Status.Path.
func (r Repo) Statuses() ([]Status, error) {
	out, err := r.run("status", "--porcelain=v1", "-z", "--untracked-files=normal")
	if err != nil {
		return nil, err
	}
	records := strings.Split(string(out), "\x00")
	statuses := make([]Status, 0, len(records))
	for i := 0; i < len(records); i++ {
		rec := records[i]
		// A real status record is "XY <path>"; record[2] is the separator.
		// Bare records (rename origins) and the trailing NUL record are skipped.
		if len(rec) < 4 || rec[2] != ' ' {
			continue
		}
		code, p := rec[:2], rec[3:]
		statuses = append(statuses, Status{Path: p, Code: code})
		if strings.ContainsAny(code, "RC") && i+1 < len(records) {
			i++ // consume the following bare origin-path record
		}
	}
	return statuses, nil
}

// Show reads a blob from a revision. A path absent from that revision is not
// an error: ok is false. Other git failures are returned wrapped.
func (r Repo) Show(rev, rel string) (text string, ok bool, err error) {
	out, e := r.run("show", rev+":"+rel)
	if e != nil {
		msg := string(out)
		if strings.Contains(msg, "does not exist") || strings.Contains(msg, "exists on disk, but not in") {
			return "", false, nil
		}
		return "", false, e
	}
	return string(out), true, nil
}

// blameMeta is the per-commit information porcelain prints once per commit.
type blameMeta struct {
	author   string
	time     time.Time
	summary  string
	boundary bool
}

// Blame attributes every line of rel. Metadata is cached by commit hash:
// porcelain repeats only the header line for later lines of a known commit.
func (r Repo) Blame(rel string) ([]BlameLine, error) {
	out, err := r.run("blame", "--porcelain", "--", rel)
	if err != nil {
		return nil, err
	}
	cache := map[string]*blameMeta{}
	var lines []BlameLine
	var hash string
	var final int
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "\t") {
			// The tab-prefixed content line finalizes the current entry.
			if m, ok := cache[hash]; ok {
				lines = append(lines, BlameLine{
					Line:     final,
					Author:   m.author,
					Time:     m.time,
					Summary:  m.summary,
					Commit:   shortHash(hash),
					Boundary: m.boundary || isZeroHash(hash),
				})
			}
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 3 && isBlameHash(fields[0]) {
			hash = strings.TrimPrefix(fields[0], "^")
			final, _ = strconv.Atoi(fields[2])
			if _, ok := cache[hash]; !ok {
				cache[hash] = &blameMeta{}
			}
			continue
		}
		m := cache[hash]
		if m == nil {
			continue
		}
		switch {
		case strings.HasPrefix(line, "author "):
			m.author = strings.TrimPrefix(line, "author ")
		case strings.HasPrefix(line, "author-time "):
			if sec, e := strconv.ParseInt(strings.TrimPrefix(line, "author-time "), 10, 64); e == nil {
				m.time = time.Unix(sec, 0)
			}
		case strings.HasPrefix(line, "summary "):
			m.summary = strings.TrimPrefix(line, "summary ")
		case line == "boundary":
			m.boundary = true
		}
	}
	slices.SortFunc(lines, func(a, b BlameLine) int { return a.Line - b.Line })
	return lines, nil
}

func isBlameHash(s string) bool {
	s = strings.TrimPrefix(s, "^")
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}

func isZeroHash(s string) bool {
	if s == "" {
		return false
	}
	for _, c := range s {
		if c != '0' {
			return false
		}
	}
	return true
}

func shortHash(s string) string {
	if len(s) > 8 {
		return s[:8]
	}
	return s
}

// Refs lists local branches, remote-tracking branches and tags, grouped in
// that order and alphabetical within each group. Symbolic .../HEAD refs are
// skipped.
func (r Repo) Refs() ([]Ref, error) {
	out, err := r.run("for-each-ref", "--format=%(refname)", "--", "refs/heads", "refs/remotes", "refs/tags")
	if err != nil {
		return nil, err
	}
	var refs []Ref
	for _, line := range strings.Split(string(out), "\n") {
		full := strings.TrimSpace(line)
		var ref Ref
		switch {
		case strings.HasPrefix(full, "refs/heads/"):
			ref = Ref{Name: strings.TrimPrefix(full, "refs/heads/"), Full: full, Kind: LocalBranch}
		case strings.HasPrefix(full, "refs/remotes/"):
			name := strings.TrimPrefix(full, "refs/remotes/")
			if name == "HEAD" || strings.HasSuffix(name, "/HEAD") {
				continue
			}
			ref = Ref{Name: name, Full: full, Kind: RemoteBranch}
		case strings.HasPrefix(full, "refs/tags/"):
			ref = Ref{Name: strings.TrimPrefix(full, "refs/tags/"), Full: full, Kind: Tag}
		default:
			continue
		}
		refs = append(refs, ref)
	}
	slices.SortFunc(refs, func(a, b Ref) int {
		if a.Kind != b.Kind {
			return int(a.Kind) - int(b.Kind)
		}
		return strings.Compare(a.Name, b.Name)
	})
	return refs, nil
}

// RecentBranches lists the branches HEAD moved to, newest first, from the
// reflog's "checkout: moving from A to B" entries. Names are exact, so the
// caller decides which still exist. A fresh clone has no checkouts to list.
func (r Repo) RecentBranches() ([]string, error) {
	out, err := r.run("reflog", "show", "--format=%gs", "-n", "500", "HEAD")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(string(out), "\n") {
		rest, ok := strings.CutPrefix(line, "checkout: moving from ")
		if !ok {
			continue
		}
		_, to, ok := strings.Cut(rest, " to ")
		if !ok || slices.Contains(names, to) {
			continue
		}
		names = append(names, to)
	}
	return names, nil
}

// Checkout switches to a ref. Remote branches get a local tracking branch
// whose name drops the remote's first path segment (origin/feature -> feature).
func (r Repo) Checkout(ref Ref) error {
	switch ref.Kind {
	case LocalBranch:
		_, err := r.run("checkout", ref.Name)
		return err
	case Tag:
		_, err := r.run("checkout", "--detach", ref.Name)
		return err
	case RemoteBranch:
		local := ref.Name
		if i := strings.IndexByte(local, '/'); i >= 0 {
			local = local[i+1:]
		}
		if _, err := r.run("checkout", local); err == nil {
			return nil
		}
		_, err := r.run("checkout", "-b", local, "--track", ref.Full)
		return err
	}
	return nil
}

// CheckoutRevision detaches HEAD at an arbitrary revision.
func (r Repo) CheckoutRevision(rev string) error {
	_, err := r.run("checkout", "--detach", rev)
	return err
}

// CreateBranch creates a branch at HEAD and switches to it.
func (r Repo) CreateBranch(name string) error {
	_, err := r.run("checkout", "-b", name)
	return err
}

// Dirty reports whether tracked files have uncommitted changes.
func (r Repo) Dirty() (bool, error) {
	out, err := r.run("status", "--porcelain", "--untracked-files=no")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(out)) != "", nil
}

// Stash pushes tracked changes onto the stash with a message.
func (r Repo) Stash(message string) error {
	_, err := r.run("stash", "push", "-m", message)
	return err
}
