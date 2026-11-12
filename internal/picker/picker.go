// Package picker is the list a go to shows below the caret when it finds
// more than one place to go, as IntelliJ's: the usages of a declaration,
// or the implementations of a method.
package picker

import (
	"path"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// Item is a place to go: a file of the project and where in it.
type Item struct {
	// Rel is the file, from the project's root, with slashes.
	Rel string
	// Line and Col are from 0; Col counts bytes.
	Line, Col int
	// Text is the line, trimmed, to tell the places of a file apart.
	Text string
}

// List is what the popup shows: its title, and the places once they are
// found.
type List struct {
	Title string
	Items []Item
	// Loading shows the list is being asked for still.
	Loading bool

	sel int
	// shown is the row last scrolled into view, which is sel's once the
	// view caught up with the keys: scrolling it each frame would keep
	// the user from scrolling.
	shown int
}

// Style is the app's look for the list.
type Style struct {
	Panel, Border, Shadow ui.Color
	Text, Muted, Faint    ui.Color
	Hover, Selected       ui.Color
	CodeFamily            string
	CodeSize              float32
}

// rows is how many rows show before the list scrolls.
const rows = 12

// Build fills panel with the list and takes its keys: Up and Down move
// through it, Enter or a click chooses. It returns the index of the item
// chosen in this frame, or -1.
func (l *List) Build(c *ui.Context, panel ui.Element, s Style) int {
	chosen := -1
	if n := len(l.Items); n > 0 {
		l.sel = min(max(l.sel, 0), n-1)
		switch {
		case panel.Shortcut(0, ui.KeyDown):
			l.sel = min(l.sel+1, n-1)
		case panel.Shortcut(0, ui.KeyUp):
			l.sel = max(l.sel-1, 0)
		case panel.Shortcut(0, ui.KeyEnter):
			chosen = l.sel
		}
	}
	const rowH = 26
	panel.Column().Padding(4).Gap(2).Radius(10).Background(s.Panel).Border(1, s.Border).
		Shadow(0, 6, 22, 0, s.Shadow).MinWidth(360).MaxWidth(720).Role(ui.RoleList).Label(l.Title)
	panel.Children(func() {
		title := l.Title
		if !l.Loading {
			title += "  " + strconv.Itoa(len(l.Items))
		}
		ui.Text(c, title).FontSize(11.5).FontWeight(600).TextColor(s.Muted).Padding(4, 8).SingleLine()
		switch {
		case l.Loading:
			ui.Text(c, "Searching…").FontSize(12.5).TextColor(s.Faint).Padding(4, 8)
			return
		case len(l.Items) == 0:
			ui.Text(c, "Nothing found").FontSize(12.5).TextColor(s.Faint).Padding(4, 8)
			return
		}
		ui.Scroll(c).Key("rows").MaxHeight(rows * (rowH + 2)).FillWidth().Children(func() {
			for i, it := range l.Items {
				if l.row(c, s, i, it, rowH) {
					chosen = i
				}
			}
		})
	})
	return chosen
}

// row draws a place: the line's text, then the file's name and the line's
// number, muted. It reports a click.
func (l *List) row(c *ui.Context, s Style, i int, it Item, h float32) bool {
	c.Key(it.Rel + ":" + strconv.Itoa(it.Line) + ":" + strconv.Itoa(it.Col))
	at := path.Base(it.Rel) + ":" + strconv.Itoa(it.Line+1)
	row := ui.Row(c).Height(h).Padding(0, 8).Gap(12).Radius(6).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(at).Tooltip(it.Rel)
	if i == l.sel {
		row.Background(s.Selected)
		if l.shown != i {
			l.shown = i
			row.ScrollIntoView()
		}
	} else if row.Hovered() {
		row.Background(s.Hover)
	}
	row.Children(func() {
		ui.Text(c, it.Text).Font(s.CodeFamily).FontSize(s.CodeSize).TextColor(s.Text).SingleLine().Ellipsis("…").Shrink(1).Grow(1)
		ui.Text(c, at).FontSize(11.5).TextColor(s.Faint).SingleLine().Shrink(0)
	})
	return row.Clicked()
}
