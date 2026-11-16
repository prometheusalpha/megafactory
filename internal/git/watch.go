package git

import (
	"os/exec"
	"strings"
)

// GitDir is the absolute path of the repository's git directory, which a
// worktree keeps apart from its working tree.
func GitDir(dir string) (string, bool) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--absolute-git-dir").Output()
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(string(out)), true
}

// WatchIgnored reports whether a path of the git directory, relative to
// it, is left out of a watch for commits and branch changes: the object
// store, logs and hooks change with no ref moving. The index is watched
// for staging, which Repo's commands leave alone (GIT_OPTIONAL_LOCKS=0).
func WatchIgnored(rel string) bool {
	top, _, _ := strings.Cut(rel, "/")
	switch top {
	case "objects", "logs", "hooks", "info", "lfs":
		return true
	}
	return false
}
