package dap

import (
	"path/filepath"
	"slices"
	"testing"
)

// TestToggleSetsAndClears keeps a file's lines ascending, and drops the
// file when its last line goes.
func TestToggleSetsAndClears(t *testing.T) {
	b := NewBreakpoints()
	if !b.Toggle("a/A.java", 20) || !b.Toggle("a/A.java", 12) {
		t.Fatal("toggle did not set")
	}
	if got := b.Lines("a/A.java"); !slices.Equal(got, []int{12, 20}) {
		t.Fatalf("lines %v", got)
	}
	if b.Toggle("a/A.java", 12) {
		t.Fatal("toggle did not clear")
	}
	if got := b.Lines("a/A.java"); !slices.Equal(got, []int{20}) {
		t.Fatalf("lines after clear %v", got)
	}
	b.Toggle("a/A.java", 20)
	if !b.Empty() || len(b.Files()) != 0 {
		t.Fatalf("empty %v %v", b.Empty(), b.Files())
	}
}

// TestBreakpointsRoundTrip keeps them across sessions, and reads a missing
// file as none.
func TestBreakpointsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".idea", "megafactory_breakpoints.json")
	missing, err := LoadBreakpoints(path)
	if err != nil || !missing.Empty() {
		t.Fatalf("missing file: %v %v", err, missing.Files())
	}
	b := NewBreakpoints()
	b.Toggle("src/main/java/A.java", 7)
	b.Toggle("src/main/java/B.java", 3)
	if err := b.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := LoadBreakpoints(path)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Files(), []string{"src/main/java/A.java", "src/main/java/B.java"}) {
		t.Fatalf("files %v", got.Files())
	}
	if !slices.Equal(got.Lines("src/main/java/A.java"), []int{7}) {
		t.Fatalf("lines %v", got.Lines("src/main/java/A.java"))
	}
}

// TestRemapFollowsEdits moves a breakpoint with the text: lines added above
// push it down, removals above pull it up, and an edit around it keeps it.
func TestRemapFollowsEdits(t *testing.T) {
	old := []string{"a", "b", "c", "d"}
	cases := []struct {
		name string
		text []string
		line int
		want int
	}{
		{"line added above", []string{"a", "new", "b", "c", "d"}, 3, 4},
		{"lines removed above", []string{"b", "c", "d"}, 3, 2},
		{"line edited in place", []string{"a", "b", "changed", "d"}, 3, 3},
		{"line added below", []string{"a", "b", "c", "d", "e"}, 3, 3},
		{"text emptied", nil, 3, 0},
	}
	for _, c := range cases {
		got := Remap(old, c.text, []int{c.line})
		if c.want == 0 {
			if len(got) != 0 {
				t.Errorf("%s: %v, want none", c.name, got)
			}
			continue
		}
		if !slices.Equal(got, []int{c.want}) {
			t.Errorf("%s: %v, want %d", c.name, got, c.want)
		}
	}
}

// TestRemapKeepsEveryLine maps a whole set at once, without duplicates.
func TestRemapKeepsEveryLine(t *testing.T) {
	old := []string{"a", "b", "c", "d", "e"}
	got := Remap(old, append([]string{"x", "a", "b"}, old[2:]...), []int{1, 4})
	if !slices.Equal(got, []int{2, 5}) {
		t.Fatalf("lines %v", got)
	}
}
