package app

import "megafactory/internal/navhistory"

// navState is where the caret has been, for Back and Forward.
type navState struct {
	hist navhistory.History
	// last is where the caret was when the editor last showed: a jump
	// from there leaves it. The editor has no word for a caret's moves, so
	// each frame's place is held against the one before.
	last *navhistory.Place
	// to is where Back or Forward goes. Until the caret is in its file,
	// what shows is the way there, not a place to come back to.
	to *navhistory.Place
}

// navFrame takes where the caret of t is, once the frame placed it: a
// jump far from where it was, in the file or to another, pushes that.
func (a *App) navFrame(t *tab) {
	n := &a.nav
	pos := t.ed.CursorPos()
	cur := navhistory.Place{Path: t.path, Line: pos.Line, Col: pos.Col}
	if n.to != nil {
		if cur.Path != n.to.Path {
			return
		}
		n.to = nil
	} else if n.last != nil && navhistory.Jumped(*n.last, cur) {
		n.hist.Push(*n.last)
	}
	n.last = &cur
}

// NavigateBack goes to where the caret was before it jumped, opening its
// file again if its tab was closed.
func (a *App) NavigateBack() { a.navGo((*navhistory.History).Back) }

// NavigateForward goes back to where NavigateBack left.
func (a *App) NavigateForward() { a.navGo((*navhistory.History).Forward) }

func (a *App) navGo(step func(*navhistory.History, navhistory.Place) (navhistory.Place, bool)) {
	n := &a.nav
	if n.last == nil {
		return
	}
	p, ok := step(&n.hist, *n.last)
	if !ok {
		return
	}
	n.to = &p
	a.openSpan(openLine{path: p.Path, line: p.Line, col: p.Col})
}

// navOpenFailed lets the caret's jumps count again when the file Back or
// Forward went to could not be read.
func (a *App) navOpenFailed(rel string) {
	if n := &a.nav; n.to != nil && n.to.Path == rel {
		n.to = nil
	}
}
