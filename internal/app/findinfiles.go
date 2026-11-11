package app

import (
	"context"
	"fmt"
	"path"

	"megafactory/internal/search"

	"github.com/egoist/mygo/ui"
)

// findInFiles is Find in Files: a search for text in the project's files,
// on the disk, as a dialog.
type findInFiles struct {
	open  bool
	query search.Query
	sel   int
	// shown is the row last scrolled into view; see recentPalette.
	shown int
	// files are the project's, read off the UI thread as the dialog
	// opens; nil until they arrive.
	files []string
	// ran is the query the last search was started for, and gen counts
	// the searches, so that the result of an old one is dropped.
	ran     search.Query
	started bool
	gen     int
	cancel  context.CancelFunc
	running bool
	res     search.Result
	err     error
}

func (a *App) OpenFindInFiles() {
	if a.project == "" {
		return
	}
	p := &findInFiles{open: true, shown: -1}
	// What is selected in the editor is what the user most likely means.
	if t := a.activeTab(); t != nil && t.ed != nil {
		if s := t.ed.SelectedText(); s != "" && len(s) <= 100 && !containsLineBreak(s) {
			p.query.Text = s
		}
	}
	a.findIn = p
	project := a.project
	a.host.Run(func() func() {
		files := search.Files(project)
		return func() {
			if a.findIn == p {
				p.files = files
			}
		}
	})
}

func containsLineBreak(s string) bool {
	for _, r := range s {
		if r == '\n' || r == '\r' {
			return true
		}
	}
	return false
}

func (a *App) activeTab() *tab {
	if a.active < 0 || a.active >= len(a.tabs) {
		return nil
	}
	return a.tabs[a.active]
}

// run starts the search for the query as it is now, ending the one
// running, if the query or the options changed since.
func (a *App) runFind(p *findInFiles) {
	if p.files == nil || p.started && p.ran == p.query {
		return
	}
	p.started, p.ran = true, p.query
	if p.cancel != nil {
		p.cancel()
	}
	p.gen++
	gen, q, files, root := p.gen, p.query, p.files, a.project
	p.sel, p.shown = 0, -1
	if q.Text == "" {
		p.res, p.err, p.running, p.cancel = search.Result{}, nil, false, nil
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	p.cancel, p.running = cancel, true
	a.host.Run(func() func() {
		res, err := search.Content(ctx, root, files, q)
		return func() {
			if a.findIn != p || p.gen != gen {
				return
			}
			p.res, p.err, p.running = res, err, false
		}
	})
}

func (a *App) findInFilesView(c *ui.Context, k *colors) {
	p := a.findIn
	if p == nil {
		return
	}
	a.runFind(p)
	ms := p.res.Matches
	p.sel = min(max(p.sel, 0), max(len(ms)-1, 0))
	choose := func(m search.Found) {
		a.findIn = nil
		if p.cancel != nil {
			p.cancel()
		}
		a.openSpan(openLine{path: m.Path, line: m.Line, col: m.Col, n: m.Len})
	}
	ui.DialogBase(c.Key("find-in-files"), &p.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.18))
		panel.Width(760).MaxHeight(520).Padding(6).Gap(4).Radius(14).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 12, 40, 0, k.shadowFocused).Label("Find in Files")
		panel.Children(func() {
			ui.Row(c).Height(36).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon("search")).Size(15, 15).TextColor(k.iconMuted)
				in := ui.TextInputBase(c, &p.query.Text).Label("Search text in files").Placeholder("Find in files").
					FontSize(14).TextColor(k.text).Grow(1).AutoFocus()
				in.OnTextKey(func(key ui.TextKey) bool {
					switch key.Key {
					case ui.KeyDown:
						p.sel = min(p.sel+1, len(ms)-1)
					case ui.KeyUp:
						p.sel = max(p.sel-1, 0)
					case ui.KeyEnter:
						if len(ms) > 0 {
							choose(ms[p.sel])
						}
					default:
						return false
					}
					return true
				})
				toggle(c, k, "Aa", "Match case", &p.query.MatchCase)
				toggle(c, k, "W", "Whole word", &p.query.WholeWord)
				toggle(c, k, ".*", "Regex", &p.query.Regex)
			})
			ui.Divider(c)
			ui.Text(c, a.findStatus(p)).FontSize(11.5).TextColor(k.textMuted).Padding(2, 10).SingleLine()
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
				for i, m := range ms {
					a.findRow(c, k, p, i, m, choose)
				}
			})
		})
	})
	if !p.open {
		if p.cancel != nil {
			p.cancel()
		}
		a.findIn = nil
	}
}

func (a *App) findStatus(p *findInFiles) string {
	switch {
	case p.files == nil:
		return "Reading files…"
	case p.query.Text == "":
		return "Type to search the project's files"
	case p.err != nil:
		return "Not a valid regular expression"
	case p.running:
		return "Searching…"
	case len(p.res.Matches) == 0:
		return "No matches"
	case p.res.More:
		return fmt.Sprintf("First %d matches", len(p.res.Matches))
	}
	n := len(p.res.Matches)
	return fmt.Sprintf("%d %s in %d %s", n, plural(n, "match", "matches"), p.res.Files, plural(p.res.Files, "file", "files"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// toggle is an option of the search, a small button that stays pressed.
func toggle(c *ui.Context, k *colors, text, label string, on *bool) {
	c.Key(label)
	e := ui.Row(c).Size(28, 24).Radius(6).Center().Shrink(0).Cursor(ui.CursorPointer).
		Role(ui.RoleCheckBox).Label(label).Checked(*on)
	switch {
	case *on:
		e.Background(k.selected).Border(0.5, c.Theme().Accent)
	case e.Hovered():
		e.Background(k.hover)
	}
	e.Children(func() {
		ui.Text(c, text).FontSize(12).FontWeight(600).TextColor(k.text).SingleLine()
	})
	if e.Clicked() {
		*on = !*on
	}
}

func (a *App) findRow(c *ui.Context, k *colors, p *findInFiles, i int, m search.Found, choose func(search.Found)) {
	c.Key(i)
	row := ui.Row(c).Height(30).Padding(0, 10).Gap(14).Radius(9).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(fmt.Sprintf("%s:%d", m.Path, m.Line+1))
	if i == p.sel {
		row.Background(k.selected)
		if p.shown != i {
			p.shown = i
			row.ScrollIntoView()
		}
	} else if row.Hovered() {
		row.Background(k.hover)
	}
	row.Children(func() {
		from, to := min(m.From, len(m.Text)), min(m.To, len(m.Text))
		ui.RichText(c).FontSize(12.5).SingleLine().Grow(1).MinWidth(0).Children(func() {
			ui.Text(c, m.Text[:from]).TextColor(k.text)
			ui.Text(c, m.Text[from:to]).FontWeight(700).TextColor(c.Theme().Accent)
			ui.Text(c, m.Text[to:]).TextColor(k.text)
		})
		// The file name is what tells matches apart; the directories only
		// crowd the line out, and the row's label still carries the path.
		ui.Text(c, fmt.Sprintf("%s:%d", path.Base(m.Path), m.Line+1)).FontSize(11.5).TextColor(k.textMuted).
			SingleLine().Ellipsis("…").MaxWidth(280)
	})
	row.OnClick(func() { choose(m) })
}
