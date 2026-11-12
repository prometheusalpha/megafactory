package app

import (
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

func TestReference(t *testing.T) {
	b := editor.NewBuffer("one\ntwo\nthree\nfour\n")
	at := func(line, col int) int { return b.Offset(editor.Pos{Line: line, Col: col}) }
	for _, tc := range []struct {
		name string
		sel  editor.Selection
		want string
	}{
		{"caret", editor.Selection{Anchor: at(1, 1), Head: at(1, 1)}, "src/A.java:2"},
		{"within a line", editor.Selection{Anchor: at(2, 0), Head: at(2, 3)}, "src/A.java:3"},
		{"across lines", editor.Selection{Anchor: at(0, 2), Head: at(2, 1)}, "src/A.java:1-3"},
		{"backwards", editor.Selection{Anchor: at(2, 1), Head: at(0, 2)}, "src/A.java:1-3"},
		{"whole lines", editor.Selection{Anchor: at(1, 0), Head: at(3, 0)}, "src/A.java:2-3"},
		{"one whole line", editor.Selection{Anchor: at(1, 0), Head: at(2, 0)}, "src/A.java:2"},
	} {
		if got := reference("src/A.java", b, tc.sel); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
