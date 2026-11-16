package app

import (
	"fmt"
	"os"
	"path/filepath"

	"megafactory/internal/git"
)

// leavesNoFile is whether reverting s removes its file, as git has no
// version of it at HEAD to return to.
func leavesNoFile(s git.Status) bool {
	switch s.Kind() {
	case git.Untracked, git.Added, git.Renamed:
		return true
	}
	return false
}

// revertChange asks, then undoes s: a tracked file returns to HEAD, one
// git doesn't hold goes to the Trash. A tab's unsaved edits go with it.
func (a *App) revertChange(s git.Status) {
	rel := a.projectRel(s.Path)
	if rel == "" {
		return
	}
	detail := "Its changes are discarded, and it goes back to its last commit."
	if leavesNoFile(s) {
		detail = "It is moved to the Trash."
	}
	q := Question{
		Message:     fmt.Sprintf("Revert “%s”?", filepath.Base(s.Path)),
		Detail:      detail + " Unsaved edits in its tab are lost.",
		Buttons:     []string{"Revert", "Cancel"},
		Destructive: 0,
	}
	a.host.Ask(q, func(button int) {
		if button == 0 {
			a.doRevert(s, rel)
		}
	})
}

func (a *App) doRevert(s git.Status, rel string) {
	repo, trash, tree := a.git.repo, a.host.Trash, a.root
	abs, gone := a.abs(rel), leavesNoFile(s)
	dirs := []string{parentOf(rel)}
	if s.Orig != "" {
		if orig := a.projectRel(s.Orig); orig != "" {
			dirs = append(dirs, parentOf(orig))
		}
	}
	a.host.Run(func() func() {
		err := repo.Revert(s)
		var text string
		if err == nil && gone {
			err = trash(abs)
		} else if err == nil {
			var data []byte
			if data, err = os.ReadFile(abs); err == nil {
				text = string(data)
			}
		}
		return func() {
			if a.root != tree {
				return
			}
			if err != nil {
				a.host.ShowError("Couldn't revert", err.Error())
			} else {
				a.revertedTabs(rel, text, gone)
			}
			a.refresh(dirs)
		}
	})
}

// revertedTabs closes the diff of rel, and the file's tab when the file is
// gone, or else puts text in it, over any edits.
func (a *App) revertedTabs(rel, text string, gone bool) {
	for i := len(a.tabs) - 1; i >= 0; i-- {
		t := a.tabs[i]
		switch {
		case t.path == diffPath(rel) || (gone && t.ed != nil && t.path == rel):
			a.closeTab(i)
		case t.ed != nil && t.path == rel:
			t.ed.Buffer.Reset(text)
			a.javaChange(t)
		}
	}
}
