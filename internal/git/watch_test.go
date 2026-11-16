package git

import "testing"

func TestWatchIgnored(t *testing.T) {
	for rel, want := range map[string]bool{
		"HEAD":                    false,
		"packed-refs":             false,
		"refs":                    false,
		"refs/heads/main":         false,
		"objects":                 true,
		"objects/ab/cdef":         true,
		"logs/HEAD":               true,
		"index":                   false,
		"index.lock":              false,
		"refs/heads/index":        false,
		"hooks/pre-commit.sample": true,
	} {
		if got := WatchIgnored(rel); got != want {
			t.Errorf("WatchIgnored(%q) = %v, want %v", rel, got, want)
		}
	}
}

func TestGitDir(t *testing.T) {
	dir, _ := newTestRepo(t)
	got, ok := GitDir(dir)
	if !ok || got == "" {
		t.Fatalf("GitDir(repo) = %q, %v", got, ok)
	}
	if _, ok := GitDir(t.TempDir()); ok {
		t.Error("GitDir(non-repo) = ok, want false")
	}
}
