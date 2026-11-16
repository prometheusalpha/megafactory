package app

import (
	"errors"
	"slices"
	"strings"
	"time"

	"megafactory/internal/javalang"
	"megafactory/internal/quickfix"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// staleRetries is how often a quick fix asks again of a server that has
// not yet got the text typed a moment ago: the frame that sends it comes
// every few milliseconds.
const staleRetries = 6

// QuickFix lists what the server offers at the caret, or at the selection,
// as IntelliJ's Alt+Enter, and applies the one chosen. Only the fixes that
// change this file are listed: a fix that creates a class or edits another
// file would need that file's tab to take the edit.
func (a *App) QuickFix() {
	if len(a.tabs) == 0 {
		return
	}
	t := a.tabs[a.active]
	j := &a.java
	srv := j.srv
	if srv == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	a.javaChange(t)
	file := a.abs(t.path)
	sel := t.ed.Selection()
	start, end := min(sel.Anchor, sel.Head), max(sel.Anchor, sel.Head)
	version := t.ed.Buffer.Version()
	ds := a.quickFixDiagnostics(t, file, start, end)
	current := func() bool { return j.srv == srv && a.tabActive(t) && t.ed.Buffer.Version() == version }

	list := &quickfix.List{Loading: true}
	a.showQuickFixes(t, start, list, nil)
	a.host.Run(func() func() {
		var (
			acts []javalang.Action
			err  error
		)
		for range staleRetries {
			acts, err = srv.CodeActions(file, version, start, end, ds)
			if !errors.Is(err, javalang.ErrStale) {
				break
			}
			time.Sleep(50 * time.Millisecond)
		}
		acts = slices.DeleteFunc(acts, func(act javalang.Action) bool { return !onlyIn(act, file) })
		return func() {
			// Escape on the list while it looked is a change of mind.
			if !current() || !t.ed.PopupShown() {
				return
			}
			if err != nil {
				acts = nil
			}
			a.showQuickFixes(t, start, &quickfix.List{Items: quickFixItems(acts)}, acts)
		}
	})
}

// quickFixDiagnostics are the server's diagnostics that touch the range:
// the editor's own places for them, which the edits since moved, and the
// codes the server sent, which the editor has no field for.
func (a *App) quickFixDiagnostics(t *tab, file string, start, end int) []javalang.Diagnostic {
	known := a.java.diagnostics[file].ds
	var out []javalang.Diagnostic
	for _, d := range t.ed.Diagnostics() {
		if d.End < start || d.Start > end {
			continue
		}
		jd := javalang.Diagnostic{Start: d.Start, End: d.End, Severity: int(d.Severity), Message: d.Message, Source: d.Source}
		if i := slices.IndexFunc(known, func(k javalang.Diagnostic) bool { return k.Message == d.Message && k.Source == d.Source }); i >= 0 {
			jd.Code = known[i].Code
		}
		out = append(out, jd)
	}
	return out
}

// onlyIn reports whether every edit of the action is to file.
func onlyIn(act javalang.Action, file string) bool {
	return len(act.Edits) > 0 && !slices.ContainsFunc(act.Edits, func(fe javalang.FileEdit) bool { return fe.Path != file })
}

func quickFixItems(acts []javalang.Action) []quickfix.Item {
	items := make([]quickfix.Item, len(acts))
	for i, act := range acts {
		items[i] = quickfix.Item{Title: act.Title, Kind: quickFixKind(act.Kind)}
	}
	return items
}

func quickFixKind(kind string) string {
	switch {
	case strings.HasPrefix(kind, "refactor"):
		return "Refactor"
	case strings.HasPrefix(kind, "source"):
		return "Source"
	}
	return ""
}

// showQuickFixes shows list below the caret at offset; a choice applies
// acts[i] to t's text as one step of undo.
func (a *App) showQuickFixes(t *tab, offset int, list *quickfix.List, acts []javalang.Action) {
	t.ed.ShowPopup(offset, func(c *ui.Context, panel ui.Element) {
		k := colorsOf(c)
		chosen := list.Build(c, panel, quickfix.Style{
			Panel: k.dialog, Border: k.panelBorder, Shadow: k.shadowFocused,
			Text: k.text, Muted: k.textMuted, Faint: k.textFaint, Hover: k.hover, Selected: k.selected,
		})
		if chosen < 0 || chosen >= len(acts) {
			return
		}
		t.ed.ClosePopup()
		for _, fe := range acts[chosen].Edits {
			edits := make([]editor.Edit, len(fe.Edits))
			for i, e := range fe.Edits {
				edits[i] = editor.Edit{Start: e.Start, End: e.End, Text: e.NewText}
			}
			t.ed.Buffer.Edit(edits...)
		}
	})
}
