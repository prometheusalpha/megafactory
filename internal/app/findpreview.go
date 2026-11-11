package app

import (
	"os"
	"path/filepath"

	"megafactory/internal/search"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

const (
	previewHeight = 240
	// previewMaxBytes keeps a huge file from stalling the dialog; its
	// match still opens in a tab.
	previewMaxBytes = 2 << 20
	// previewLeadLines is how many lines show above the match.
	previewLeadLines = 5
)

// findPreview is the read-only view of the selected match's file below the
// list. One editor serves every match of the same file, so moving down the
// matches of a file only moves the selection.
type findPreview struct {
	path string
	ed   *editor.State
	at   search.Found
	// pending is a match the editor has not selected yet: it can only do
	// so once the frame has built it.
	pending bool
	// loading is the file being read, and failed the one that could not
	// be, so that neither is asked for again every frame.
	loading, failed string
}

// syncPreview points the preview at the selected match, reading its file off
// the UI thread when it is not the one shown. The old file stays on screen
// until the new one arrives, so arrowing through files does not flicker.
func (a *App) syncPreview(p *findInFiles, ms []search.Found) {
	pv := &p.preview
	if len(ms) == 0 {
		*pv = findPreview{}
		return
	}
	m := ms[p.sel]
	switch {
	case pv.ed != nil && pv.path == m.Path:
		if pv.at != m {
			pv.at, pv.pending = m, true
		}
		return
	case pv.loading == m.Path || pv.failed == m.Path:
		return
	}
	pv.loading = m.Path
	file := filepath.Join(a.project, filepath.FromSlash(m.Path))
	name := filepath.Base(file)
	a.host.Run(func() func() {
		data, err := readLimited(file, previewMaxBytes)
		return func() {
			if a.findIn != p {
				return
			}
			pv.loading = ""
			if err != nil {
				pv.failed = m.Path
				return
			}
			buf := editor.NewBuffer(string(data))
			buf.SetLanguage(languageFor(name))
			pv.path, pv.at, pv.pending = m.Path, m, true
			pv.ed = &editor.State{Buffer: buf, ReadOnly: true, NoFolding: true, NoActiveLine: true,
				Font: code.editorFont(), LineHeight: prefs.EditorLineHeight}
		}
	})
}

// readLimited reads a file, or fails when it is longer than max.
func readLimited(file string, max int64) ([]byte, error) {
	info, err := os.Stat(file)
	if err != nil {
		return nil, err
	}
	if info.Size() > max {
		return nil, os.ErrInvalid
	}
	return os.ReadFile(file)
}

// previewPane draws the preview under the list: the match's whole path,
// which the rows leave out, and the file at the match.
func (a *App) previewPane(c *ui.Context, k *colors, p *findInFiles) {
	pv := &p.preview
	if pv.ed == nil {
		return
	}
	ui.Divider(c)
	ui.Text(c, pv.path).FontSize(11.5).TextColor(k.textMuted).Padding(2, 10).SingleLine().Ellipsis("…")
	pv.ed.Theme = activeEditorTheme(c, k)
	pv.ed.Font, pv.ed.LineHeight = code.editorFont(), prefs.EditorLineHeight
	c.Key("preview:" + pv.path)
	editor.View(c, pv.ed).Height(previewHeight).FillWidth().Shrink(0)
	if pv.pending {
		pv.pending = false
		start := pv.ed.Buffer.Offset(editor.Pos{Line: pv.at.Line, Col: pv.at.Col})
		pv.ed.Select(start, start+pv.at.Len)
		pv.ed.ScrollTo(max(pv.at.Line-previewLeadLines, 0))
	}
}
