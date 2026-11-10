package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"megafactory/internal/git"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// readEditorInlays observes the hints SetInlays left in the editor. mygo
// v0.8.3 has no public inlay getter, so the test reads the (unexported)
// view.inlays slice through reflection, keeping only kind-0 hint inlays.
// Len/Index/Int/String read fine on read-only reflected values.
func readEditorInlays(st *editor.State) ([]editor.Inlay, bool) {
	v := reflect.ValueOf(st)
	if v.Kind() != reflect.Ptr || v.IsNil() {
		return nil, false
	}
	v = v.Elem().FieldByName("ed").FieldByName("view").FieldByName("inlays")
	if !v.IsValid() || v.Kind() != reflect.Slice {
		return nil, false
	}
	var out []editor.Inlay
	for i := range v.Len() {
		in := v.Index(i)
		kind, at, text := in.FieldByName("kind"), in.FieldByName("at"), in.FieldByName("text")
		if !kind.IsValid() || !at.IsValid() || !text.IsValid() {
			return nil, false
		}
		if kind.Int() != 0 {
			continue
		}
		out = append(out, editor.Inlay{Offset: int(at.Int()), Text: text.String()})
	}
	return out, true
}

func TestApplyInlaysComposeJavaAndBlame(t *testing.T) {
	saveGutter, saveInline := prefs.GutterBlame, prefs.InlineBlame
	prefs.GutterBlame, prefs.InlineBlame = false, true
	t.Cleanup(func() { prefs.GutterBlame, prefs.InlineBlame = saveGutter, saveInline })

	a := newApp(defaultState)
	a.project = "/project"
	a.git.ok = true
	a.git.repo = git.Repo{Dir: "/project"}

	const rel = "src/App.java"
	buf := editor.NewBuffer("alpha\nbeta\ngamma\n")
	ed := &editor.State{Buffer: buf}
	tb := &tab{path: rel, name: "App.java", ed: ed}
	a.tabs = []*tab{tb}
	a.active = 0

	// The caret sits on line 1 (0-based); the blame line that matches is
	// Line 2 (1-based).
	ed.GoTo(editor.Pos{Line: 1})
	ed.SetDiagnostics(buf.Snapshot().Version(), []editor.Diagnostic{
		{Start: 0, End: 5, Severity: editor.SeverityError, Message: "bad"},
	})

	hints := []editor.Inlay{{Offset: 0, Text: " hint A"}, {Offset: 3, Text: " hint B"}}
	a.java.inlays = map[string][]editor.Inlay{rel: hints}
	a.git.blame = map[string]*blameFile{rel: &blameFile{lines: []git.BlameLine{
		{Line: 1, Author: "Ada", Summary: "first", Time: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		{Line: 2, Author: "Ada", Summary: "Fix the thing", Time: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
		{Line: 3, Author: "Grace", Summary: "last", Boundary: true, Time: time.Date(2024, 1, 3, 0, 0, 0, 0, time.UTC)},
	}}}

	// The blame contributes exactly one inlay, at the caret line's end.
	blame := a.blameCaretInlay(tb)
	if len(blame) != 1 {
		t.Fatalf("blame inlays %d, want 1", len(blame))
	}
	wantOffset := buf.Snapshot().LineEnd(1)
	if blame[0].Offset != wantOffset {
		t.Errorf("blame offset %d, want %d", blame[0].Offset, wantOffset)
	}
	if !strings.HasPrefix(blame[0].Text, "  ") || !strings.Contains(blame[0].Text, "Ada") {
		t.Errorf("blame text %q, want a prefixed label naming Ada", blame[0].Text)
	}

	a.applyInlays(tb)

	got, ok := readEditorInlays(ed)
	if !ok {
		t.Fatal("could not observe the editor's inlays")
	}
	if len(got) != len(hints)+1 {
		t.Errorf("composed inlays %d, want %d (java hints + blame)", len(got), len(hints)+1)
	}
	// Composing clones the Java hints rather than growing their slice.
	if len(a.java.inlays[rel]) != len(hints) {
		t.Errorf("java hints mutated to %d, want %d", len(a.java.inlays[rel]), len(hints))
	}
	found := false
	for _, in := range got {
		if in.Offset == wantOffset && strings.HasPrefix(in.Text, "  ") {
			found = true
		}
	}
	if !found {
		t.Errorf("no blame inlay at offset %d among %v", wantOffset, got)
	}
}

func TestBlameGutterDisabledBuildsNothing(t *testing.T) {
	saveGutter, saveInline := prefs.GutterBlame, prefs.InlineBlame
	prefs.GutterBlame, prefs.InlineBlame = false, false
	t.Cleanup(func() { prefs.GutterBlame, prefs.InlineBlame = saveGutter, saveInline })

	a := newApp(defaultState)
	a.project = "/project"
	a.git.ok = true
	a.git.repo = git.Repo{Dir: "/project"}

	ed := &editor.State{Buffer: editor.NewBuffer("alpha\nbeta\ngamma\n"), SoftWrap: true}
	tb := &tab{path: "src/App.java", name: "App.java", ed: ed}
	a.tabs = []*tab{tb}
	a.active = 0
	a.git.blame = map[string]*blameFile{tb.path: &blameFile{lines: []git.BlameLine{
		{Line: 1, Author: "Ada", Summary: "first", Time: time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)},
	}}}

	built := false
	tt := ui.NewTester(func(c *ui.Context) {
		c.Root()
		a.blameGutter(c, &lightColors, tb)
		built = true
	}, 1280, 800)
	tt.Frame()
	if !built {
		t.Fatal("view was never built")
	}
	// Disabled, the gutter builds nothing and leaves the editor untouched:
	// enabling it is what turns SoftWrap off.
	if !ed.SoftWrap {
		t.Error("disabled gutter changed the editor's SoftWrap")
	}
}
