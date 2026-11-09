// Package workspace reads a project's files from disk.
package workspace

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Entry is a file or folder of a project folder.
type Entry struct {
	Name string
	Dir  bool
	// Ignored is git's verdict: the entry is shown, but dimmed, as IDEs
	// show what .gitignore leaves out.
	Ignored bool
}

// List reads the folder rel of the project at root, folders first, each by
// name. Inside an ignored folder everything is ignored, without asking git.
func List(root, rel string, ignored bool) ([]Entry, error) {
	des, err := os.ReadDir(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		e := Entry{Name: de.Name(), Dir: de.IsDir(), Ignored: ignored}
		if de.Type()&os.ModeSymlink != 0 {
			if fi, err := os.Stat(filepath.Join(root, filepath.FromSlash(rel), de.Name())); err == nil {
				e.Dir = fi.IsDir()
			}
		}
		entries = append(entries, e)
	}
	if !ignored {
		markIgnored(root, rel, entries)
	}
	slices.SortFunc(entries, func(x, y Entry) int {
		if x.Dir != y.Dir {
			if x.Dir {
				return -1
			}
			return 1
		}
		return strings.Compare(x.Name, y.Name)
	})
	return entries, nil
}

// markIgnored asks git which entries .gitignore leaves out, so that nested
// .gitignore files, negations and the global excludes count as git counts
// them. Outside a repository nothing is ignored. .git is git's own, which
// check-ignore doesn't report.
func markIgnored(root, rel string, entries []Entry) {
	var in bytes.Buffer
	for i := range entries {
		if entries[i].Name == ".git" {
			entries[i].Ignored = true
			continue
		}
		in.WriteString(path.Join(rel, entries[i].Name))
		in.WriteByte(0)
	}
	cmd := exec.Command("git", "-C", root, "check-ignore", "-z", "--stdin")
	cmd.Stdin = &in
	out, err := cmd.Output()
	var exit *exec.ExitError
	if err != nil && !(errors.As(err, &exit) && exit.ExitCode() == 1) {
		return // 1 is none ignored; anything else, not a repository or no git
	}
	ignored := map[string]bool{}
	for p := range strings.SplitSeq(strings.TrimSuffix(string(out), "\x00"), "\x00") {
		ignored[path.Base(p)] = true
	}
	for i := range entries {
		if ignored[entries[i].Name] {
			entries[i].Ignored = true
		}
	}
}
