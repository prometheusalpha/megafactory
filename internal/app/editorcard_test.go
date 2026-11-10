package app

import (
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

// TestEditorCardKeepsEditorHeight guards a file tab's editor from collapsing
// to zero height: a Row centers its children on the cross axis unless told
// to stretch them, which leaves the pane blank while the tab bar still shows.
func TestEditorCardKeepsEditorHeight(t *testing.T) {
	for _, gutter := range []bool{false, true} {
		name := "plain"
		if gutter {
			name = "blame gutter"
		}
		t.Run(name, func(t *testing.T) {
			old := prefs
			t.Cleanup(func() { prefs = old })
			prefs.GutterBlame = gutter
			a, tt := openSample(t)
			if gutter {
				a.git.ok = true
			}
			click(t, tt, "src", "main", "java", "com", "example", "demo", "DemoApplication.java")
			tt.Frame()
			ed := a.tabs[a.active].ed
			if ed == nil {
				t.Fatal("no editor for the file tab")
			}
			if h := ed.ViewportHeight(); h <= 0 {
				t.Errorf("editor viewport height = %v, want > 0 (blank tab)", h)
			}
		})
	}
}

// TestDiffTabKeepsViewerHeight guards the diff tab's viewer, which the
// editor card builds through its own column of a header and the viewer.
func TestDiffTabKeepsViewerHeight(t *testing.T) {
	a, tt := openSample(t)
	d := &editor.DiffState{Old: editor.NewBuffer("before\n"), New: editor.NewBuffer("after\n")}
	a.tabs = append(a.tabs, &tab{
		path: "megafactory:diff:App.java", name: "App.java (HEAD)",
		diffRel: "App.java", diff: d,
	})
	a.active = len(a.tabs) - 1
	tt.Frame()
	if h := d.ViewportHeight(); h <= 0 {
		t.Errorf("diff viewport height = %v, want > 0 (blank diff)", h)
	}
}
