package app

import (
	"log"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"megafactory/internal/recent"
	"megafactory/internal/workspace"
)

// OpenProject replaces the open project with the folder at dir, and its
// tabs with none: switchProject asked to save them.
func (a *App) OpenProject(dir string) {
	if a.stopWatch != nil {
		a.stopWatch()
	}
	// The old project's server closes before its tabs, whose paths need
	// the old project to name.
	a.stopJava()
	a.closeTerms()
	a.project = dir
	a.loadRunConfigs()
	a.builds = buildView{}
	a.gitProjectOpened()
	a.pendingLine = nil
	a.root = &node{name: filepath.Base(dir), dir: true, open: true}
	a.tabs = slices.DeleteFunc(a.tabs, func(t *tab) bool { return t.ed != nil || t.diff != nil })
	a.active, a.selected, a.treeFocus = 0, "", ""
	tree := a.root
	a.stopWatch = a.host.Watch(dir, func(dirs []string) {
		if a.root == tree {
			a.refresh(dirs)
		}
	})
	a.loadDir(a.root)
	SaveState(a.State())
	a.setRecent(recent.Add(a.recent, dir))
}

func (a *App) OpenFolder() {
	a.host.PickFolder(a.switchProject)
}

// loadDir reads a folder's children, again when it changed on disk,
// dropping them if another project replaced its tree meanwhile.
func (a *App) loadDir(n *node) {
	n.loaded = true
	tree, root, rel, ignored := a.root, a.project, n.path, n.ignored
	a.host.Run(func() func() {
		entries, err := workspace.List(root, rel, ignored)
		return func() {
			if a.root != tree {
				return
			}
			// A folder that fails stays empty rather than retried each frame.
			if err != nil {
				log.Printf("read %s: %v", rel, err)
				n.children = nil
				return
			}
			n.merge(entries)
			if n == tree {
				a.considerJava(entries)
			}
		}
	})
}

// considerJava starts the language server when the project is a Java one
// and the one running is not its own: the root was just read.
func (a *App) considerJava(entries []workspace.Entry) {
	if !javaProject(entries) || a.java.project == a.project {
		return
	}
	a.startJava(a.project)
}

// merge makes a folder's children its entries, keeping the nodes of those
// that stay, so that what was open in them stays open.
func (n *node) merge(entries []workspace.Entry) {
	old := map[string]*node{}
	for _, ch := range n.children {
		old[ch.name] = ch
	}
	n.children = make([]*node, len(entries))
	for i, e := range entries {
		ch := old[e.Name]
		if ch == nil || ch.dir != e.Dir {
			ch = &node{name: e.Name, path: path.Join(n.path, e.Name), dir: e.Dir}
		}
		// Whether a folder is ignored is its children's too: they are
		// read again, when it is open.
		if ch.ignored != e.Ignored && ch.dir {
			ch.loaded, ch.children = false, nil
		}
		ch.ignored = e.Ignored
		n.children[i] = ch
	}
}

// refresh reads again the folders a watch hints at, and those the tree
// has read under them: a hint says only that something changed in or
// under its folder, and a .gitignore changed there can ignore files
// anywhere below. "." is everything, as after the watch lost track.
func (a *App) refresh(dirs []string) {
	a.noteBuildChanges(dirs)
	for _, d := range dirs {
		if d == "." {
			d = ""
		}
		a.root.at(d).walkLoaded(a.loadDir)
	}
	a.reloadTabs(dirs)
	a.gitRefresh()
}

// at is the node at a project path, or the deepest the tree has above it,
// as the folder of a hint a watch gave that the tree hasn't listed yet.
func (n *node) at(p string) *node {
	if p == "" {
		return n
	}
	for name := range strings.SplitSeq(p, "/") {
		i := slices.IndexFunc(n.children, func(ch *node) bool { return ch.name == name })
		if i < 0 {
			break
		}
		n = n.children[i]
	}
	return n
}

func (n *node) walkLoaded(fn func(*node)) {
	if !n.loaded {
		return
	}
	fn(n)
	for _, ch := range n.children {
		if ch.dir {
			ch.walkLoaded(fn)
		}
	}
}
