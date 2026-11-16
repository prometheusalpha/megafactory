package app

import "testing"

func TestIsTestPath(t *testing.T) {
	for rel, want := range map[string]bool{
		"src/test":                          true,
		"src/test/java/a/ATest.java":        true,
		"svc/src/test/java/a/ATest.java":    true,
		"svc/src/main/java/a/A.java":        false,
		"src/testing/A.java":                false,
		"src/main/resources/src/tests/x.md": false,
	} {
		if got := isTestPath(rel); got != want {
			t.Errorf("isTestPath(%q) = %v, want %v", rel, got, want)
		}
	}
}
