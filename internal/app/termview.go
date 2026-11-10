package app

import (
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf8"

	"megafactory/internal/runner"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// TermHandle is what the app asks of a terminal. Tests give their own, as
// terminal.New loads a native library that no headless test has.
type TermHandle interface {
	View(c *ui.Context) ui.Element
	Title() string
	Close() error
}

// LiveTerm is the terminal plugin's terminal as a TermHandle.
type LiveTerm struct{ T *terminal.Terminal }

func (l LiveTerm) View(c *ui.Context) ui.Element { return terminal.View(c, l.T) }
func (l LiveTerm) Title() string                 { return l.T.Title() }
func (l LiveTerm) Close() error                  { return l.T.Close() }

// termTab is one terminal of the Terminal page. Its program keeps running
// while another tab shows, as a terminal shows in one view at a time.
type termTab struct {
	name    string
	title   string
	term    TermHandle
	err     error
	renamed bool
	editing bool
	rename  string
	fresh   bool
	exited  bool
	code    int
}

func termLabel(t *termTab) string {
	if t.renamed {
		return t.name
	}
	if t.title != "" {
		return t.title
	}
	return t.name
}

// terminalPage shows the project's terminals, starting one at the project
// root the first time the page opens, as the Build page loads its tasks.
func (a *App) terminalPage(c *ui.Context, k *colors) {
	if len(a.terms) == 0 && !a.termStarting && a.termErr == nil {
		a.newTerm()
	}
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Row(c).Height(34).Padding(0, 8, 0, 6).Gap(8).AlignItems(ui.Center).Children(func() {
			tabs := ui.TabsBase(c, &a.activeTerm, len(a.terms))
			track(k, tabs.List).Children(func() {
				for i, t := range slices.Clone(a.terms) {
					a.termTabView(c, k, tabs.Tab(i), i, t)
				}
			})
			if iconButton(c, k, "plus", "New Terminal", 24, 14).Clicked() {
				a.newTerm()
			}
		})
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			switch {
			case a.termErr != nil:
				buildNote(c, a.termErr.Error(), c.Theme().Danger)
			case len(a.terms) == 0:
				buildNote(c, "Starting terminal…", k.textMuted)
			case a.terms[a.activeTerm].err != nil:
				buildNote(c, a.terms[a.activeTerm].err.Error(), c.Theme().Danger)
			case a.terms[a.activeTerm].term == nil:
				buildNote(c, "Starting terminal…", k.textMuted)
			default:
				t := a.terms[a.activeTerm]
				t.term.View(c).Grow(1).MinHeight(0).FillWidth()
				if t.exited {
					ui.Text(c, fmt.Sprintf("[Process exited with code %d]", t.code)).
						FontSize(11.5).TextColor(k.textFaint).Padding(6)
				}
			}
		})
	})
}

func (a *App) termTabView(c *ui.Context, k *colors, e ui.Element, i int, t *termTab) {
	trackTab(k, e, i == a.activeTerm).Padding(0, 8, 0, 10).Label(termLabel(t)).Children(func() {
		if t.editing {
			in := ui.TextInputBase(c.Key("term-name"), &t.rename).Label("Terminal name").FontSize(12).TextColor(k.text).
				Width(140).AutoFocus()
			if t.fresh {
				t.fresh = false
				in.SetTextSelection(0, utf8.RuneCountInString(t.rename))
			}
			in.OnTextKey(func(key ui.TextKey) bool {
				if key.Key != ui.KeyEscape {
					return false
				}
				t.editing = false
				return true
			})
			if in.Submitted() {
				t.name, t.renamed, t.editing = strings.TrimSpace(t.rename), true, false
			} else if in.FocusLeft() {
				t.name, t.renamed, t.editing = strings.TrimSpace(t.rename), true, false
			}
		} else {
			ui.Text(c, termLabel(t)).FontSize(12).TextColor(k.text).SingleLine()
		}
		if iconButton(c, k, "x", "Close "+termLabel(t), 18, 12).Clicked() {
			a.closeTerm(i)
		}
	})
	e.ContextMenu(func(m *ui.Menu) {
		if m.Item("Rename…").Chosen() {
			t.rename, t.editing, t.fresh = termLabel(t), true, true
		}
		if m.Item("New Terminal").Chosen() {
			a.newTerm()
		}
		if m.Item("Close").Chosen() {
			a.closeTerm(i)
		}
	})
}

// newTerm starts a shell at the project root. terminal.New loads a native
// library, so the host makes it off the UI thread and tests replace it.
func (a *App) newTerm() {
	a.termStarting, a.termErr = true, nil
	project := a.project
	tab := &termTab{}
	opts := terminal.Options{
		Dir:       project,
		Font:      code.terminalFont(),
		Theme:     themes.light,
		DarkTheme: themes.dark,
		OpenLink:  a.openStackLink,
		OnTitle: func(title string) {
			a.host.Update(func() { tab.title = title })
		},
		OnExit: func(code int) {
			a.host.Update(func() { tab.exited, tab.code = true, code })
		},
	}
	a.host.Run(func() func() {
		h, err := a.host.NewTerm(opts)
		return func() {
			if a.project != project {
				// A project switch drained the tabs while the shell
				// started. It belongs to no open project.
				if h != nil {
					_ = h.Close()
				}
				return
			}
			a.termStarting = false
			if err != nil {
				a.termErr = err
				return
			}
			tab.term = h
			tab.name = fmt.Sprintf("Terminal %d", len(a.terms)+1)
			a.terms = append(a.terms, tab)
			a.activeTerm = len(a.terms) - 1
		}
	})
}

func (a *App) closeTerm(i int) {
	if i < 0 || i >= len(a.terms) {
		return
	}
	t := a.terms[i]
	a.terms = slices.Delete(a.terms, i, i+1)
	if a.activeTerm >= len(a.terms) {
		a.activeTerm = max(len(a.terms)-1, 0)
	}
	if t.term == nil {
		return
	}
	a.host.Run(func() func() {
		err := t.term.Close()
		return func() {
			if err != nil {
				log.Printf("terminal: %v", err)
			}
		}
	})
}

// closeTerms hangs up the shells of the project being left: their cwd is
// the old root, so keeping them would run commands in a project not open.
func (a *App) closeTerms() {
	closing := a.terms
	a.terms, a.activeTerm, a.termStarting, a.termErr = nil, 0, false, nil
	a.host.Run(func() func() {
		for _, t := range closing {
			if t.term != nil {
				_ = t.term.Close()
			}
		}
		return func() {}
	})
}

// openStackLink opens a path a program printed, as File.java:123. Relative
// paths are the project's; a bare name is looked up in the loaded tree.
func (a *App) openStackLink(link string) bool {
	if strings.Contains(link, "://") {
		return false // a hyperlink or URL belongs to the system
	}
	file, line := link, 0
	if ls := runner.Links(link); len(ls) > 0 {
		file, line = ls[0].Path, max(ls[0].Line-1, 0)
	}
	rel, ok := a.stackRel(file)
	if !ok {
		return false
	}
	a.openAt(rel, line)
	return true
}

func (a *App) stackRel(file string) (string, bool) {
	if filepath.IsAbs(file) {
		return a.relative(file)
	}
	if strings.ContainsRune(file, '/') {
		abs := filepath.Join(a.project, file)
		if fi, err := os.Stat(abs); err == nil && !fi.IsDir() {
			return a.relative(abs)
		}
	}
	if a.root != nil {
		var found string
		a.root.walkLoaded(func(n *node) {
			if !n.dir && n.name == file && found == "" {
				found = n.path
			}
		})
		if found != "" {
			return found, true
		}
	}
	// Stack traces print only the file name, and the tree may not have
	// read the folder yet. Look it up on disk, skipping build output.
	var found string
	_ = filepath.WalkDir(a.project, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			switch d.Name() {
			case ".git", "target", "build", "node_modules", ".megafactory":
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() == file && found == "" {
			found = p
		}
		return nil
	})
	if found != "" {
		return a.relative(found)
	}
	return "", false
}
