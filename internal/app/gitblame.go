package app

import (
	"fmt"

	"megafactory/internal/git"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// blameFile is the blame of one open file, kept per tab path. A file with
// no blame (or one git could not read) keeps its err for the gutter to skip.
type blameFile struct {
	lines []git.BlameLine
	err   error
}

// blameLabel is the compact annotation at the end of a line. Boundary lines
// are changes not committed yet, so they are the user's, not an author's.
func blameLabel(bl git.BlameLine) string {
	if bl.Boundary {
		return "You · Uncommitted changes"
	}
	return fmt.Sprintf("%s, %s · %s", bl.Author, bl.Time.Format("02 Jan 2006"), clip(bl.Summary, 40))
}

// blameCaretInlay is the inline blame of the caret's line, or nil when the
// setting, repo or blame data say not to show it. Blame lines are 1-based;
// the editor's lines are 0-based.
func (a *App) blameCaretInlay(t *tab) []editor.Inlay {
	if !prefs.InlineBlame || !a.git.ok || t == nil || t.ed == nil {
		return nil
	}
	bf := a.git.blame[t.path]
	if bf == nil {
		return nil
	}
	line := t.ed.CursorPos().Line
	for _, bl := range bf.lines {
		if bl.Line-1 == line {
			snap := t.ed.Buffer.Snapshot()
			return []editor.Inlay{{Offset: snap.LineEnd(line), Text: "  " + blameLabel(bl), Trailing: true}}
		}
	}
	return nil
}

// applyInlays composes the Java hints and the inline blame into the single
// inlay list the editor keeps. It clones the Java hints so the blame never
// grows the slice the language server owns.
func (a *App) applyInlays(t *tab) {
	if t == nil || t.ed == nil {
		return
	}
	out := append([]editor.Inlay{}, a.java.inlays[t.path]...)
	out = append(out, a.blameCaretInlay(t)...)
	t.ed.SetInlays(out)
}

// gitBlame loads one file's blame off the UI thread. The result is dropped
// when the repo (or project) changed while git ran, so a switched project
// cannot paint another's annotations.
func (a *App) gitBlame(t *tab) {
	if !a.git.ok || t == nil || t.ed == nil {
		return
	}
	repo, project := a.git.repo, a.project
	path := t.path
	rel := a.gitRel(path)
	a.host.Run(func() func() {
		lines, err := repo.Blame(rel)
		return func() {
			if a.git.repo.Dir != repo.Dir || a.project != project {
				return
			}
			if a.git.blame == nil {
				a.git.blame = map[string]*blameFile{}
			}
			a.git.blame[path] = &blameFile{lines: lines, err: err}
			a.applyInlays(t)
		}
	})
}

// blameGutter draws a fixed-width column of blame beside the editor. Blame
// labels need straight, unwrapped lines to line up with the text, so it
// turns soft wrap off while it is shown. When disabled it builds nothing,
// leaving the editor's layout untouched.
func (a *App) blameGutter(c *ui.Context, k *colors, t *tab) {
	if !prefs.GutterBlame || !a.git.ok || t == nil || t.ed == nil {
		return
	}
	t.ed.SoftWrap = false
	byLine := map[int]git.BlameLine{}
	if bf := a.git.blame[t.path]; bf != nil {
		for _, bl := range bf.lines {
			byLine[bl.Line-1] = bl
		}
	}
	ui.Box(c).Width(170).Shrink(0).Clip().Draw(func(p *ui.Painter, r ui.Rect) {
		p.Fill(ui.Rect{X: r.X + r.W - 1, Y: r.Y, W: 1, H: r.H}, k.panelBorder, 0)
		if t.ed.Buffer == nil {
			return
		}
		first, last := t.ed.VisibleLines()
		last = min(last, t.ed.Buffer.LineCount()-1)
		for line := max(first, 0); line <= last; line++ {
			bl, ok := byLine[line]
			if !ok {
				continue
			}
			y := r.Y + float32(t.ed.LineTop(line)-t.ed.ScrollY())
			if y+11 < r.Y || y > r.Y+r.H {
				continue
			}
			p.Text(r.X+6, y, clip(blameLabel(bl), 22), 11, k.textFaint)
		}
	})
}

// gitFrame keeps the inline blame on the caret's line. Rebuilding the inlay
// list every frame is wasteful, so it only reapplies when the active tab or
// its caret line changes.
func (a *App) gitFrame() {
	if !prefs.InlineBlame || !a.git.ok {
		a.git.blameLine, a.git.blameCaret = "", -1
		return
	}
	if len(a.tabs) == 0 || a.active < 0 || a.active >= len(a.tabs) {
		return
	}
	t := a.tabs[a.active]
	if t.ed == nil {
		return
	}
	line := t.ed.CursorPos().Line
	if t.path == a.git.blameLine && line == a.git.blameCaret {
		return
	}
	a.git.blameLine, a.git.blameCaret = t.path, line
	a.applyInlays(t)
}

// applyBlameSettings reloads (or clears) every tab's blame as the gutter or
// inline setting changes, then rebuilds their inlays with the new setting.
func (a *App) applyBlameSettings() {
	on := prefs.GutterBlame || prefs.InlineBlame
	for _, t := range a.tabs {
		if on {
			if a.git.ok && t.ed != nil {
				a.gitBlame(t)
			}
		} else {
			delete(a.git.blame, t.path)
		}
		a.applyInlays(t)
	}
}
