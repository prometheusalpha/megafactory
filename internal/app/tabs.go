package app

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func (a *App) SaveActive() {
	if len(a.tabs) > 0 && a.tabs[a.active].ed != nil {
		a.save(a.tabs[a.active], nil)
	}
}

// save writes a tab's text, as it is now, off the UI thread, then calls
// then. Edits made while it writes keep the tab modified.
func (a *App) save(t *tab, then func()) {
	snap, file := t.ed.Buffer.Snapshot(), a.abs(t.path)
	a.host.Run(func() func() {
		err := writeSnapshot(file, snap)
		return func() {
			if err != nil {
				a.host.ShowError("Couldn't save "+t.name, err.Error())
				return
			}
			if t.ed.Buffer.Version() == snap.Version() {
				t.ed.Buffer.MarkSaved()
			}
			// The server sees the file as saved, with the text just
			// written, which this frame may not have sent yet.
			a.javaDidSave(t)
			if then != nil {
				then()
			}
		}
	})
}

// writeSnapshot writes the file in place, as editors save, so that it
// keeps its permissions, owner and links.
func writeSnapshot(file string, snap editor.Snapshot) error {
	f, err := os.OpenFile(file, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := snap.WriteTo(f); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func (a *App) CloseActive() {
	if len(a.tabs) == 0 {
		// As VS Code, ⌘W with nothing to close closes the window.
		a.host.CloseWindow()
		return
	}
	a.requestClose(a.tabs[a.active])
}

func (a *App) CloseOtherTabs() {
	if len(a.tabs) > 1 {
		a.closeOthers(a.tabs[a.active])
	}
}

func (a *App) closeOthers(keep *tab) {
	a.closeTabs(slices.DeleteFunc(slices.Clone(a.tabs), func(o *tab) bool { return o == keep }))
}

func (a *App) NextTab() { a.stepTab(1) }
func (a *App) PrevTab() { a.stepTab(-1) }

// stepTab activates the tab delta places along, wrapping round the ends.
func (a *App) stepTab(delta int) {
	if n := len(a.tabs); n > 1 {
		a.active = (a.active + delta%n + n) % n
	}
}

// requestClose closes a tab, asking first to save its changes.
func (a *App) requestClose(t *tab) {
	if t.ed == nil || !t.ed.Buffer.Modified() {
		a.closeTabOf(t)
		return
	}
	q := Question{
		Message:     fmt.Sprintf("Save the changes to “%s”?", t.name),
		Detail:      "Your changes will be lost if you don't save them.",
		Buttons:     []string{"Save", "Don't Save", "Cancel"},
		Destructive: 1,
	}
	a.host.Ask(q, func(button int) {
		switch button {
		case 0:
			a.save(t, func() { a.closeTabOf(t) })
		case 1:
			a.closeTabOf(t)
		}
	})
}

// closeTabOf closes a tab by itself rather than its place, which a
// question asked meanwhile may have moved.
func (a *App) closeTabOf(t *tab) {
	if i := slices.Index(a.tabs, t); i >= 0 {
		a.closeTab(i)
	}
}

// closeTabs closes several tabs, asking once to save those modified.
func (a *App) closeTabs(ts []*tab) {
	closeAll := func() {
		for _, t := range ts {
			a.closeTabOf(t)
		}
	}
	if !a.askToSave(ts, closeAll) {
		closeAll()
	}
}

// BeforeCloseWindow asks to save the modified tabs as the window closes.
// It reports whether the window must wait for proceed.
func (a *App) BeforeCloseWindow(proceed func()) (wait bool) {
	return a.askToSave(a.tabs, proceed)
}

// askToSave asks to save the modified tabs of ts, all in one question,
// then calls proceed once they are saved or given up; a failure to save
// stops it. With none modified it asks nothing, calls nothing, and
// reports false: the caller goes on as it is.
func (a *App) askToSave(ts []*tab, proceed func()) (asked bool) {
	var mod []*tab
	for _, t := range ts {
		if t.ed != nil && t.ed.Buffer.Modified() {
			mod = append(mod, t)
		}
	}
	if len(mod) == 0 {
		return false
	}
	msg := fmt.Sprintf("Save the changes to “%s”?", mod[0].name)
	if len(mod) > 1 {
		msg = fmt.Sprintf("Save the changes to %d files?", len(mod))
	}
	q := Question{
		Message:     msg,
		Detail:      "Your changes will be lost if you don't save them.",
		Buttons:     []string{"Save", "Don't Save", "Cancel"},
		Destructive: 1,
	}
	a.host.Ask(q, func(button int) {
		switch button {
		case 0:
			left := len(mod)
			for _, t := range mod {
				a.save(t, func() {
					if left--; left == 0 {
						proceed()
					}
				})
			}
		case 1:
			proceed()
		}
	})
	return true
}

// tabMenu is a tab's context menu, as IntelliJ's.
func (a *App) tabMenu(svc ui.Services, m *ui.Menu, t *tab) {
	i := slices.Index(a.tabs, t)
	if a.shortcutOf(m.Item("Close"), "closeTab").Chosen() {
		a.requestClose(t)
	}
	if a.shortcutOf(m.Item("Close Others"), "closeOtherTabs").Disabled(len(a.tabs) < 2).Chosen() {
		a.closeOthers(t)
	}
	if m.Item("Close to the Right").Disabled(i == len(a.tabs)-1).Chosen() {
		a.closeTabs(slices.Clone(a.tabs[i+1:]))
	}
	if m.Item("Close All").Chosen() {
		a.closeTabs(slices.Clone(a.tabs))
	}
	m.Separator()
	file := t.ed != nil
	if m.Item("Copy Path").Disabled(!file).Chosen() {
		svc.WriteClipboard(a.abs(t.path))
	}
	if m.Item("Reveal in Tree").Disabled(!file).Chosen() {
		a.revealInTree(t.path)
	}
	m.Separator()
	if m.Item("Compare with HEAD").Disabled(!file || !a.git.ok).Chosen() {
		a.compareWithHEAD(t)
	}
}

// revealInTree selects a file in the tree, and gives its row the focus,
// opening the folders above it, which the tree builds toward
// (expandToReveal) as they are read.
func (a *App) revealInTree(rel string) {
	a.showSidebar, a.sidePane, a.focus = true, 0, focusSidebar
	a.selected, a.revealing = rel, rel
}

// expandToReveal opens the folders above a.revealing as far as the tree
// has read them, reading the next, until it shows its row.
func (a *App) expandToReveal() {
	n := a.root
	for name := range strings.SplitSeq(parentOf(a.revealing), "/") {
		n.open = true
		// Its children are not in until the read answers; looking for the
		// next folder among none would take it for gone.
		if !n.read {
			if !n.loaded {
				a.loadDir(n)
			}
			return
		}
		if name == "" {
			break
		}
		i := slices.IndexFunc(n.children, func(ch *node) bool { return ch.name == name })
		if i < 0 {
			a.revealing = "" // gone meanwhile
			return
		}
		n = n.children[i]
	}
	n.open = true
	if !n.loaded {
		a.loadDir(n)
	}
}

// reloadTabs reads again the files of unmodified tabs under dirs, which
// changed on disk, as another app or git wrote them. A modified tab keeps
// its text: saving it writes over the disk's.
func (a *App) reloadTabs(dirs []string) {
	for _, t := range a.tabs {
		if t.ed == nil || t.ed.Buffer.Modified() || !slices.ContainsFunc(dirs, func(d string) bool {
			return d == "." || d == "" || strings.HasPrefix(t.path, d+"/")
		}) {
			continue
		}
		file, version := a.abs(t.path), t.ed.Buffer.Version()
		a.host.Run(func() func() {
			data, err := os.ReadFile(file)
			return func() {
				// Gone files keep their tab, to save again; edited ones
				// their edits.
				b := t.ed.Buffer
				if err != nil || b.Version() != version {
					return
				}
				if text := string(data); strings.ReplaceAll(text, "\r\n", "\n") != b.String() {
					b.Reset(text)
					// A reloaded text is a change, not an open: the
					// server keeps the document, at the new version.
					a.javaChange(t)
				}
			}
		})
	}
}
