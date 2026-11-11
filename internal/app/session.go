package app

import (
	"os"
	"path"
	"path/filepath"

	"megafactory/internal/session"
)

// sessionState is what the app keeps of the tabs between runs.
type sessionState struct {
	store session.Store
	// saved is the session the store holds for the open project, so that
	// a frame writes only when the tabs changed.
	saved session.Project
	// restoring is set while the files of the saved session are read:
	// the tabs then are not yet the project's, and must not replace it.
	restoring bool
}

func loadSessions() session.Store {
	var s session.Store
	readJSON("sessions.json", &s)
	return s
}

// sessionNow is the tabs of files as they are, diffs and settings left out.
func (a *App) sessionNow() session.Project {
	var p session.Project
	for i, t := range a.tabs {
		if t.ed == nil {
			continue
		}
		p.Files = append(p.Files, t.path)
		if i == a.active {
			p.Active = t.path
		}
	}
	return p
}

// saveSession writes the open project's tabs when they changed, each
// frame, so that a crash loses none.
func (a *App) saveSession() {
	if a.project == "" || a.sess.restoring {
		return
	}
	now := a.sessionNow()
	if a.sess.saved.Equal(now) {
		return
	}
	a.sess.saved = now
	a.sess.store = a.sess.store.With(a.project, now)
	writeJSON("sessions.json", a.sess.store)
}

// restoreTabs opens again the files the project had open. A file gone
// since is skipped; a tab the user opened meanwhile keeps the focus.
func (a *App) restoreTabs(p session.Project) {
	a.sess.saved, a.sess.restoring = p, len(p.Files) > 0
	if !a.sess.restoring {
		return
	}
	tree, root := a.root, a.project
	a.host.Run(func() func() {
		type file struct {
			path string
			data []byte
		}
		var files []file
		for _, rel := range p.Files {
			if data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))); err == nil {
				files = append(files, file{rel, data})
			}
		}
		return func() {
			if a.root != tree {
				return
			}
			a.sess.restoring = false
			fresh := len(a.tabs) == 0
			for _, f := range files {
				if !a.activate(f.path) {
					a.addFileTab(f.path, path.Base(f.path), f.data)
				}
			}
			if fresh {
				a.activate(p.Active)
			}
		}
	})
}
