package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"

	"megafactory/internal/javalang"
	"megafactory/internal/picker"

	"github.com/egoist/mygo/ui"
)

// goPlaces are the places of the project among locs, each with its
// line's text, read from disk as most are in files not open.
func (a *App) goPlaces(locs []javalang.Location) []picker.Item {
	var items []picker.Item
	files := map[string][]byte{}
	for _, l := range locs {
		rel, ok := a.relative(l.Path)
		if !ok {
			continue
		}
		data, ok := files[l.Path]
		if !ok {
			data, _ = os.ReadFile(l.Path)
			files[l.Path] = data
		}
		start := min(max(l.Start, 0), len(data))
		lineStart := bytes.LastIndexByte(data[:start], '\n') + 1
		lineEnd := bytes.IndexByte(data[start:], '\n')
		if lineEnd < 0 {
			lineEnd = len(data)
		} else {
			lineEnd += start
		}
		items = append(items, picker.Item{
			Rel: rel, Line: bytes.Count(data[:start], []byte("\n")), Col: start - lineStart,
			Text: strings.TrimSpace(string(data[lineStart:lineEnd])),
		})
	}
	return items
}

// showPicker shows list below the caret at offset of t's editor; a choice
// opens its place. A list of one place goes there instead, as IntelliJ's.
func (a *App) showPicker(t *tab, offset int, list *picker.List) {
	if !list.Loading && len(list.Items) == 1 {
		t.ed.ClosePopup()
		a.goToItem(list.Items[0])
		return
	}
	t.ed.ShowPopup(offset, func(c *ui.Context, panel ui.Element) {
		k := colorsOf(c)
		font := code.editorFont()
		chosen := list.Build(c, panel, picker.Style{
			Panel: k.dialog, Border: k.panelBorder, Shadow: k.shadowFocused,
			Text: k.text, Muted: k.textMuted, Faint: k.textFaint, Hover: k.hover, Selected: k.selected,
			CodeFamily: font.Family, CodeSize: font.Size - 1,
		})
		if chosen >= 0 {
			t.ed.ClosePopup()
			a.goToItem(list.Items[chosen])
		}
	})
}

func (a *App) goToItem(it picker.Item) {
	a.openSpan(openLine{path: filepath.ToSlash(it.Rel), line: it.Line, col: it.Col})
}
