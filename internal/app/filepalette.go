package app

import (
	"path"

	"megafactory/internal/search"

	"github.com/egoist/mygo/ui"
)

// filePalette is Go to File: a search over the project's files by name,
// ranked as dbx's Quick Open ranks its items.
type filePalette struct {
	open  bool
	query string
	sel   int
	// shown is the row last scrolled into view; see recentPalette.
	shown int
	// files are the project's, read off the UI thread as the palette
	// opens; nil until they arrive.
	files []string
	hits  []search.Hit
	// ranked is the query hits were found for, so a frame ranks only when
	// the query or the files changed.
	ranked string
	dirty  bool
}

func (a *App) OpenFilePalette() {
	if a.project == "" {
		return
	}
	p := &filePalette{open: true, shown: -1, dirty: true}
	a.files = p
	project := a.project
	a.host.Run(func() func() {
		files := search.Files(project)
		return func() {
			if a.files != p {
				return
			}
			p.files = a.openFirst(files)
			p.dirty = true
		}
	})
}

// openFirst puts the files of open tabs first, the active one left out as
// it is already in front of the user, so an empty query lists where the
// user just was.
func (a *App) openFirst(files []string) []string {
	var first []string
	seen := map[string]bool{}
	for i := len(a.tabs) - 1; i >= 0; i-- {
		t := a.tabs[i]
		if t.ed == nil || i == a.active {
			continue
		}
		first = append(first, t.path)
		seen[t.path] = true
	}
	out := first
	for _, f := range files {
		if !seen[f] {
			out = append(out, f)
		}
	}
	return out
}

// openRel opens a project file by its path: its tab, the tree's node, or
// the disk's text for a file the tree has not read yet.
func (a *App) openRel(rel string) {
	a.focus = focusEditor
	if a.activate(rel) || a.root == nil {
		return
	}
	if n := a.root.at(rel); n != nil && !n.dir && n.path == rel {
		a.openFile(n)
		return
	}
	a.openFile(&node{name: path.Base(rel), path: rel})
}

func (a *App) filePaletteView(c *ui.Context, k *colors) {
	p := a.files
	if p == nil {
		return
	}
	if p.dirty || p.ranked != p.query {
		p.hits, p.ranked, p.dirty = search.Find(p.files, p.query), p.query, false
	}
	hits := p.hits
	p.sel = min(max(p.sel, 0), max(len(hits)-1, 0))
	choose := func(h search.Hit) {
		a.files = nil
		a.openRel(h.Path)
	}
	ui.DialogBase(c.Key("files"), &p.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.18))
		panel.Width(640).MaxHeight(460).Padding(6).Gap(4).Radius(14).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 12, 40, 0, k.shadowFocused).Label("Go to File")
		panel.Children(func() {
			ui.Row(c).Height(36).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon("search")).Size(15, 15).TextColor(k.iconMuted)
				in := ui.TextInputBase(c, &p.query).Label("Search files by name").Placeholder("Search files by name").
					FontSize(14).TextColor(k.text).Grow(1).AutoFocus()
				in.OnTextKey(func(key ui.TextKey) bool {
					switch key.Key {
					case ui.KeyDown:
						p.sel = min(p.sel+1, len(hits)-1)
					case ui.KeyUp:
						p.sel = max(p.sel-1, 0)
					case ui.KeyEnter:
						if len(hits) > 0 {
							choose(hits[p.sel])
						}
					default:
						return false
					}
					return true
				})
				if in.Changed() {
					p.sel = 0
				}
			})
			ui.Divider(c)
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
				for i, h := range hits {
					a.fileRow(c, k, p, i, h, choose)
				}
				if len(hits) == 0 {
					msg := "No files match"
					if p.files == nil {
						msg = "Reading files…"
					}
					ui.Text(c, msg).FontSize(12.5).TextColor(k.textFaint).Padding(12, 10)
				}
			})
		})
	})
	if !p.open {
		a.files = nil
	}
}

func (a *App) fileRow(c *ui.Context, k *colors, p *filePalette, i int, h search.Hit, choose func(search.Hit)) {
	c.Key(h.Path)
	row := ui.Row(c).Height(38).Padding(0, 10).Gap(10).Radius(9).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(h.Path)
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
		ui.Icon(c, icon(fileIcon(h.Name))).Size(16, 16).TextColor(k.iconMuted)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			a.highlighted(c, k, h)
			if dir := path.Dir(h.Path); dir != "." {
				ui.Text(c, dir).FontSize(11.5).TextColor(k.textMuted).SingleLine().Ellipsis("…")
			}
		})
	})
	row.OnClick(func() { choose(h) })
}

// highlighted is the file's name with the runes the query matched in bold
// and in the accent color.
func (a *App) highlighted(c *ui.Context, k *colors, h search.Hit) {
	name := []rune(h.Name)
	hit := map[int]bool{}
	for _, i := range h.Indices {
		hit[i] = true
	}
	ui.RichText(c).FontSize(13).SingleLine().Children(func() {
		for from := 0; from < len(name); {
			to := from
			for to < len(name) && hit[to] == hit[from] {
				to++
			}
			t := ui.Text(c, string(name[from:to]))
			if hit[from] {
				t.FontWeight(700).TextColor(c.Theme().Accent)
			} else {
				t.TextColor(k.text)
			}
			from = to
		}
	})
}
