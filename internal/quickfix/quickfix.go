// Package quickfix is the list of fixes the language server offers at the
// caret, shown below it as IntelliJ's Alt+Enter: the import of a class
// that does not resolve, a missing throws, a refactoring of the selection.
package quickfix

import (
	"strconv"

	"github.com/egoist/mygo/ui"
)

// Item is a fix to choose: its title, and its kind in a few words, which
// tells a fix from a refactoring.
type Item struct {
	Title, Kind string
}

// List is what the popup shows: the fixes once they are found.
type List struct {
	Items []Item
	// Loading shows the list is being asked for still.
	Loading bool

	sel   int
	shown int
}

// Style is the app's look for the list.
type Style struct {
	Panel, Border, Shadow ui.Color
	Text, Muted, Faint    ui.Color
	Hover, Selected       ui.Color
}

// rows is how many rows show before the list scrolls.
const rows = 10

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
		Shadow(0, 6, 22, 0, s.Shadow).MinWidth(320).MaxWidth(720).Role(ui.RoleList).Label("Quick fixes")
	panel.Children(func() {
		ui.Text(c, "Quick fixes").FontSize(11.5).FontWeight(600).TextColor(s.Muted).Padding(4, 8).SingleLine()
		switch {
		case l.Loading:
			ui.Text(c, "Looking…").FontSize(12.5).TextColor(s.Faint).Padding(4, 8)
			return
		case len(l.Items) == 0:
			ui.Text(c, "No fixes here").FontSize(12.5).TextColor(s.Faint).Padding(4, 8)
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

func (l *List) row(c *ui.Context, s Style, i int, it Item, h float32) bool {
	c.Key(strconv.Itoa(i) + it.Title)
	row := ui.Row(c).Height(h).Padding(0, 8).Gap(12).Radius(6).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(it.Title)
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
		ui.Text(c, it.Title).FontSize(12.5).TextColor(s.Text).SingleLine().Ellipsis("…").Shrink(1).Grow(1)
		if it.Kind != "" {
			ui.Text(c, it.Kind).FontSize(11.5).TextColor(s.Faint).SingleLine().Shrink(0)
		}
	})
	return row.Clicked()
}
