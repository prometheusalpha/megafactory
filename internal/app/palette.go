package app

import (
	"os"
	"path/filepath"
	"strings"

	"megafactory/internal/recent"

	"github.com/egoist/mygo/ui"
)

// recentPalette is the Recent Projects palette, as IntelliJ's: a search
// over the projects opened last, then Open Folder.
type recentPalette struct {
	open  bool
	query string
	sel   int
	// shown is the row last scrolled into view, which is sel's once the
	// view caught up with the keys: scrolling it each frame would keep
	// the user from scrolling.
	shown int
	// missing are the projects no longer on disk, which show dimmed; read
	// as the palette opens.
	missing map[string]bool
}

func loadRecent() []string {
	var l []string
	readJSON("recent.json", &l)
	return l
}

func (a *App) setRecent(l []string) {
	a.recent = l
	writeJSON("recent.json", l)
}

func (a *App) OpenRecentPalette() {
	p := &recentPalette{open: true, shown: -1, missing: map[string]bool{}}
	a.palette = p
	paths := a.recent
	a.host.Run(func() func() {
		missing := map[string]bool{}
		for _, path := range paths {
			if fi, err := os.Stat(path); err != nil || !fi.IsDir() {
				missing[path] = true
			}
		}
		return func() { p.missing = missing }
	})
}

// switchProject opens the project at path in place of this one, asking
// first to save the tabs that would lose their changes.
func (a *App) switchProject(path string) {
	if path == a.project {
		return
	}
	if !a.askToSave(a.tabs, func() { a.OpenProject(path) }) {
		a.OpenProject(path)
	}
}

// paletteItem is a row of the palette: a project, or Open Folder.
type paletteItem struct{ path string }

func (a *App) paletteItems() []paletteItem {
	var items []paletteItem
	for _, p := range recent.Match(a.recent, a.palette.query) {
		items = append(items, paletteItem{p})
	}
	return append(items, paletteItem{})
}

func (a *App) choose(it paletteItem) {
	p := a.palette
	a.palette = nil
	switch {
	case it.path == "":
		a.OpenFolder()
	case p.missing[it.path]:
		a.setRecent(recent.Remove(a.recent, it.path))
		a.host.ShowError("Project not found", it.path+" is no longer there; it is removed from Recent Projects.")
	default:
		a.switchProject(it.path)
	}
}

var home, _ = os.UserHomeDir()

// tildePath shortens a path in the home folder, as Finder's Go menu.
func tildePath(p string) string {
	if rest, ok := strings.CutPrefix(p, home); ok && home != "" && (rest == "" || rest[0] == filepath.Separator) {
		return "~" + rest
	}
	return p
}

func (a *App) recentPaletteView(c *ui.Context, k *colors) {
	p := a.palette
	if p == nil {
		return
	}
	items := a.paletteItems()
	p.sel = min(max(p.sel, 0), len(items)-1)
	ui.DialogBase(c.Key("recent"), &p.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.18))
		panel.Width(580).MaxHeight(460).Padding(6).Gap(4).Radius(14).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 12, 40, 0, k.shadowFocused).Label("Recent Projects")
		panel.Children(func() {
			ui.Row(c).Height(36).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon("search")).Size(15, 15).TextColor(k.iconMuted)
				in := ui.TextInputBase(c, &p.query).Label("Search recent projects").Placeholder("Search recent projects").
					FontSize(14).TextColor(k.text).Grow(1).AutoFocus()
				in.OnTextKey(func(key ui.TextKey) bool {
					switch key.Key {
					case ui.KeyDown:
						p.sel = min(p.sel+1, len(items)-1)
					case ui.KeyUp:
						p.sel = max(p.sel-1, 0)
					case ui.KeyEnter:
						a.choose(items[p.sel])
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
				for i, it := range items {
					a.paletteRow(c, k, i, it)
				}
			})
		})
	})
	if !p.open {
		a.palette = nil
	}
}

func (a *App) paletteRow(c *ui.Context, k *colors, i int, it paletteItem) {
	p := a.palette
	c.Key(it.path)
	row := ui.Row(c).Height(44).Padding(0, 6, 0, 10).Gap(10).Radius(9).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton)
	if i == p.sel {
		row.Background(k.selected)
		if p.shown != i {
			p.shown = i
			row.ScrollIntoView()
		}
	} else if row.Hovered() {
		row.Background(k.hover)
	}
	if it.path == "" {
		row.Label("Open Folder…").Height(36).Children(func() {
			ui.Icon(c, icon("folder-open")).Size(16, 16).TextColor(k.iconMuted)
			ui.Text(c, "Open Folder…").FontSize(13).TextColor(k.text)
			ui.Spacer(c)
			ui.Text(c, "⌘O").FontSize(12).TextColor(k.textFaint).Padding(0, 8)
		})
		row.OnClick(func() { a.choose(it) })
		return
	}
	name, textCol := filepath.Base(it.path), k.text
	if p.missing[it.path] {
		textCol = k.textFaint
	}
	row.Label(name).Children(func() {
		ui.Icon(c, icon("folder")).Size(16, 16).TextColor(k.iconMuted)
		ui.Column(c).Grow(1).MinWidth(0).Gap(1).Children(func() {
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, name).FontSize(13).FontWeight(600).TextColor(textCol).SingleLine()
				switch {
				case it.path == a.project:
					ui.Text(c, "current").FontSize(11).TextColor(k.textFaint)
				case p.missing[it.path]:
					ui.Text(c, "not found").FontSize(11).TextColor(k.textFaint)
				}
			})
			ui.Text(c, tildePath(it.path)).FontSize(11.5).TextColor(k.textMuted).SingleLine().Ellipsis("…")
		})
		x := iconButton(c, k, "x", "Remove "+name+" from Recent Projects", 24, 13)
		if i != p.sel && !row.Hovered() {
			x.Opacity(0)
		}
		x.OnClick(func() { a.setRecent(recent.Remove(a.recent, it.path)) })
	})
	row.OnClick(func() { a.choose(it) })
}
