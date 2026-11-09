package main

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

func (a *app) saveActive() {
	if len(a.tabs) > 0 && a.tabs[a.active].ed != nil {
		a.save(a.tabs[a.active], nil)
	}
}

// save writes a tab's text, as it is now, off the UI thread, then calls
// then. Edits made while it writes keep the tab modified.
func (a *app) save(t *tab, then func()) {
	snap, file := t.ed.Buffer.Snapshot(), a.abs(t.path)
	a.host.run(func() func() {
		err := writeSnapshot(file, snap)
		return func() {
			if err != nil {
				a.host.showError("Couldn't save "+t.name, err.Error())
				return
			}
			if t.ed.Buffer.Version() == snap.Version() {
				t.ed.Buffer.MarkSaved()
			}
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

func (a *app) closeActive() {
	if len(a.tabs) == 0 {
		// As VS Code, ⌘W with nothing to close closes the window.
		a.host.closeWindow()
		return
	}
	a.requestClose(a.tabs[a.active])
}

// requestClose closes a tab, asking first to save its changes.
func (a *app) requestClose(t *tab) {
	if t.ed == nil || !t.ed.Buffer.Modified() {
		a.closeTabOf(t)
		return
	}
	q := question{
		message:     fmt.Sprintf("Save the changes to “%s”?", t.name),
		detail:      "Your changes will be lost if you don't save them.",
		buttons:     []string{"Save", "Don't Save", "Cancel"},
		destructive: 1,
	}
	a.host.ask(q, func(button int) {
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
func (a *app) closeTabOf(t *tab) {
	if i := slices.Index(a.tabs, t); i >= 0 {
		a.closeTab(i)
	}
}

func (a *app) modifiedTabs() []*tab {
	var mod []*tab
	for _, t := range a.tabs {
		if t.ed != nil && t.ed.Buffer.Modified() {
			mod = append(mod, t)
		}
	}
	return mod
}

// beforeCloseWindow asks to save the modified tabs as the window closes.
// It reports whether the window must wait, and calls proceed once they
// are saved or given up.
func (a *app) beforeCloseWindow(proceed func()) (wait bool) {
	mod := a.modifiedTabs()
	if len(mod) == 0 {
		return false
	}
	msg := fmt.Sprintf("Save the changes to “%s”?", mod[0].name)
	if len(mod) > 1 {
		msg = fmt.Sprintf("Save the changes to %d files?", len(mod))
	}
	q := question{
		message:     msg,
		detail:      "Your changes will be lost if you don't save them.",
		buttons:     []string{"Save", "Don't Save", "Cancel"},
		destructive: 1,
	}
	a.host.ask(q, func(button int) {
		switch button {
		case 0:
			// The window closes once all are written; a failure keeps it.
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

// reloadTabs reads again the files of unmodified tabs under dirs, which
// changed on disk, as another app or git wrote them. A modified tab keeps
// its text: saving it writes over the disk's.
func (a *app) reloadTabs(dirs []string) {
	for _, t := range a.tabs {
		if t.ed == nil || t.ed.Buffer.Modified() || !slices.ContainsFunc(dirs, func(d string) bool {
			return d == "." || d == "" || strings.HasPrefix(t.path, d+"/")
		}) {
			continue
		}
		file, version := a.abs(t.path), t.ed.Buffer.Version()
		a.host.run(func() func() {
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
				}
			}
		})
	}
}
