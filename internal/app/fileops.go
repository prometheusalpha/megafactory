package app

import (
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"megafactory/internal/workspace"

	"github.com/egoist/mygo/ui"
)

// treeEdit is a name being typed in the tree: node's new name, or the
// name of a file or folder new in dir.
type treeEdit struct {
	node   *node
	dir    *node
	newDir bool
	name   string
	// fresh is true until the input first shows, to select the name.
	fresh bool
}

// clipboard is a file or folder cut or copied in the tree, to paste in
// another folder. It is the tree's own, not the system's.
type clipboard struct {
	path string
	cut  bool
}

func parentOf(p string) string {
	if d := path.Dir(p); d != "." {
		return d
	}
	return ""
}

func (a *App) treeMenu(svc ui.Services, m *ui.Menu, n *node) {
	dir := n
	if !n.dir {
		dir = a.root.at(parentOf(n.path))
	}
	isRoot := n == a.root
	if m.Item("New File…").Chosen() {
		a.startNew(dir, false)
	}
	if m.Item("New Folder…").Chosen() {
		a.startNew(dir, true)
	}
	m.Separator()
	if m.Item("Cut").Shortcut(ui.Cmd, ui.KeyX).Disabled(isRoot).Chosen() {
		a.clip = &clipboard{path: n.path, cut: true}
	}
	if m.Item("Copy").Shortcut(ui.Cmd, ui.KeyC).Disabled(isRoot).Chosen() {
		a.clip = &clipboard{path: n.path}
	}
	if m.Item("Paste").Shortcut(ui.Cmd, ui.KeyV).Disabled(a.clip == nil).Chosen() {
		a.paste(dir)
	}
	if m.Item("Duplicate").Disabled(isRoot).Chosen() {
		a.copyInto(n.path, parentOf(n.path), false)
	}
	m.Separator()
	if m.Item("Rename…").Shortcut(0, ui.KeyEnter).Disabled(isRoot).Chosen() {
		a.startRename(n)
	}
	if m.Item("Delete…").Shortcut(ui.Cmd, ui.KeyBackspace).Disabled(isRoot).Chosen() {
		a.deleteNode(n)
	}
	m.Separator()
	if m.Item("Copy Path").Chosen() {
		svc.WriteClipboard(a.abs(n.path))
	}
	if m.Item("Reveal in Finder").Chosen() {
		a.host.Reveal(a.abs(n.path))
	}
}

// treeShortcuts are the context menu's keys, as Finder's: on the row
// with the focus, which focused tells once the tree is built.
func (a *App) treeShortcuts(tree ui.Element, focused func() *node) {
	on := func(mods ui.Modifiers, key ui.Key, fn func(n *node)) {
		tree.OnShortcut(mods, key, func() {
			if n := focused(); n != nil {
				fn(n)
			}
		})
	}
	// What the project itself can't do, its row ignores.
	unlessRoot := func(fn func(n *node)) func(n *node) {
		return func(n *node) {
			if n != a.root {
				fn(n)
			}
		}
	}
	on(ui.Cmd, ui.KeyX, unlessRoot(func(n *node) { a.clip = &clipboard{path: n.path, cut: true} }))
	on(ui.Cmd, ui.KeyC, unlessRoot(func(n *node) { a.clip = &clipboard{path: n.path} }))
	on(ui.Cmd, ui.KeyV, func(n *node) {
		if a.clip == nil {
			return
		}
		if !n.dir {
			n = a.root.at(parentOf(n.path))
		}
		a.paste(n)
	})
	on(0, ui.KeyEnter, unlessRoot(a.startRename))
	on(ui.Cmd, ui.KeyBackspace, unlessRoot(a.deleteNode))
}

func (a *App) startRename(n *node) {
	a.edit = &treeEdit{node: n, name: n.name, fresh: true}
}

func (a *App) abs(rel string) string { return filepath.Join(a.project, filepath.FromSlash(rel)) }

func (a *App) startNew(dir *node, isDir bool) {
	dir.open = true
	if !dir.loaded {
		a.loadDir(dir)
	}
	a.edit = &treeEdit{dir: dir, newDir: isDir, fresh: true}
}

// newRow is the row where the name of a new file or folder is typed, at
// the top of its folder.
func (a *App) newRow(c *ui.Context, k *colors, depth int) {
	c.Key("new")
	iconName := "file"
	if a.edit.newDir {
		iconName = "folder"
	}
	ui.Row(c).Height(26).Gap(6).Padding(0, 8).PaddingStart(8 + 14*float32(depth) + 20).AlignItems(ui.Center).Children(func() {
		ui.Icon(c, icon(iconName)).Size(15, 15).TextColor(k.iconMuted)
		a.nameInput(c, k)
	})
}

// nameInput edits a.edit's name: Enter takes it, Escape drops it, and
// leaving the input takes it unless it is wrong, as in VS Code.
func (a *App) nameInput(c *ui.Context, k *colors) {
	e := a.edit
	label := "New file name"
	switch {
	case e.node != nil:
		label = "New name of " + e.node.name
	case e.newDir:
		label = "New folder name"
	}
	in := ui.TextInputBase(c.Key("name"), &e.name).Label(label).FontSize(13).TextColor(k.text).
		Height(22).Grow(1).MinWidth(0).Padding(0, 6).Radius(6).Background(k.editor).Border(1, k.text.Alpha(0.35)).
		AutoFocus()
	in.OnTextKey(func(key ui.TextKey) bool {
		if key.Key != ui.KeyEscape {
			return false
		}
		a.edit = nil
		return true
	})
	if e.fresh {
		e.fresh = false
		// As Finder, the name without its extension, ready to retype.
		stem := strings.TrimSuffix(e.name, path.Ext(e.name))
		if stem == "" {
			stem = e.name
		}
		in.SetTextSelection(0, utf8.RuneCountInString(stem))
	}
	switch {
	case in.Submitted():
		a.commitEdit(false)
	case in.FocusLeft() && a.edit == e:
		a.commitEdit(true)
	}
}

// badName says what is wrong with a name typed in the tree, "" if nothing.
// A new file or folder may be a path, as "com/example/App.java".
func badName(name string, rename bool) string {
	if rename && strings.Contains(name, "/") {
		return "A name can't contain “/”."
	}
	for part := range strings.SplitSeq(name, "/") {
		if part == "" || part == "." || part == ".." {
			return fmt.Sprintf("“%s” is not a valid name.", name)
		}
	}
	return ""
}

func (a *App) commitEdit(leaving bool) {
	e := a.edit
	name := strings.TrimSpace(e.name)
	if name == "" || (e.node != nil && name == e.node.name) {
		a.edit = nil
		return
	}
	if msg := badName(name, e.node != nil); msg != "" {
		if leaving {
			a.edit = nil
		} else {
			a.host.ShowError("Can't use this name", msg)
		}
		return
	}
	a.edit = nil
	if e.node != nil {
		from := e.node.path
		to := path.Join(parentOf(from), name)
		a.fileOp(func(root string) error { return workspace.Rename(root, from, to) }, func() {
			a.retarget(from, to)
			a.selected = to
		}, parentOf(from))
		return
	}
	rel, isDir := path.Join(e.dir.path, name), e.newDir
	a.fileOp(func(root string) error { return workspace.Create(root, rel, isDir) }, func() {
		a.selected = rel
		if !isDir {
			a.openFile(&node{name: path.Base(rel), path: rel})
		}
	}, e.dir.path)
}

// fileOp runs op on the project off the UI thread, then done, and reads
// again the folders it changed rather than wait for the watch.
func (a *App) fileOp(op func(root string) error, done func(), dirs ...string) {
	tree, root := a.root, a.project
	a.host.Run(func() func() {
		err := op(root)
		return func() {
			if a.root != tree {
				return
			}
			if err != nil {
				a.host.ShowError("Couldn't change the project", err.Error())
			} else if done != nil {
				done()
			}
			a.refresh(dirs)
		}
	})
}

func (a *App) paste(dir *node) {
	clip := a.clip
	if clip.cut {
		a.clip = nil // the cut is gone from where it was
	}
	a.copyInto(clip.path, dir.path, clip.cut)
}

// copyInto copies, or moves, from into the folder dir, and selects what
// it made.
func (a *App) copyInto(from, dir string, move bool) {
	var to string
	a.fileOp(func(root string) (err error) {
		if move {
			to, err = workspace.Move(root, from, dir)
		} else {
			to, err = workspace.Copy(root, from, dir)
		}
		return err
	}, func() {
		if move {
			a.retarget(from, to)
		}
		a.selected = to
		if n := a.root.at(dir); n.path == dir {
			n.open = true
		}
	}, parentOf(from), dir)
}

func (a *App) deleteNode(n *node) {
	what := "file"
	if n.dir {
		what = "folder"
	}
	rel, trash := n.path, a.host.Trash
	q := Question{
		Message:     fmt.Sprintf("Move the %s “%s” to the Trash?", what, n.name),
		Detail:      "You can put it back from the Trash.",
		Buttons:     []string{"Move to Trash", "Cancel"},
		Destructive: 0,
	}
	a.host.Ask(q, func(button int) {
		if button != 0 {
			return
		}
		a.fileOp(func(root string) error { return trash(filepath.Join(root, filepath.FromSlash(rel))) }, func() {
			a.closeUnder(rel)
			if a.clip != nil && (a.clip.path == rel || strings.HasPrefix(a.clip.path, rel+"/")) {
				a.clip = nil
			}
		}, parentOf(rel))
	})
}

// retarget follows a file or folder that moved with the tabs of what was
// in it.
func (a *App) retarget(from, to string) {
	for _, t := range a.tabs {
		rest, ok := strings.CutPrefix(t.path, from)
		if t.ed == nil || !ok || (rest != "" && rest[0] != '/') {
			continue
		}
		t.path = to + rest
		t.name = path.Base(t.path)
		t.ed.Buffer.SetLanguage(languageFor(t.name))
	}
}

// closeUnder closes the tabs of a file deleted, or of what was in a folder.
func (a *App) closeUnder(rel string) {
	for i := len(a.tabs) - 1; i >= 0; i-- {
		if p := a.tabs[i].path; a.tabs[i].ed != nil && (p == rel || strings.HasPrefix(p, rel+"/")) {
			a.closeTab(i)
		}
	}
}
