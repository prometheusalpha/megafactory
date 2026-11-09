package main

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// gitDiffBase loads the file's committed text as its editor's diff base,
// so the gutter shows what the working copy changed. A file with no HEAD
// version is new: its base is empty, so every line reads as added.
func (a *app) gitDiffBase(t *tab) {
	if !a.git.ok || t == nil || t.ed == nil {
		return
	}
	repo, project := a.git.repo.Dir, a.project
	g, rel := a.git.repo, a.gitRel(t.path)
	a.host.run(func() func() {
		text, ok, err := g.Show("HEAD", rel)
		return func() {
			if a.project != project || a.git.repo.Dir != repo || !slices.Contains(a.tabs, t) {
				return
			}
			switch {
			case err != nil:
				t.ed.DiffBase = nil
			case !ok:
				t.ed.DiffBase = editor.NewBuffer("")
			default:
				t.ed.DiffBase = editor.NewBuffer(text)
			}
		}
	})
}

// openDiff opens the diff of rel against HEAD, or activates the one open.
func (a *app) openDiff(rel string) {
	path := "megafactory:diff:" + rel
	if a.activate(path) {
		return
	}
	if !a.git.ok {
		return
	}
	repo, project := a.git.repo.Dir, a.project
	g, gitPath, abs := a.git.repo, a.gitRel(rel), a.abs(rel)
	a.host.run(func() func() {
		oldText := ""
		if text, ok, err := g.Show("HEAD", gitPath); err == nil && ok {
			oldText = text
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			data = nil // a deleted file has no working text
		}
		return func() {
			if a.project != project || a.git.repo.Dir != repo || a.activate(path) {
				return
			}
			d := &editor.DiffState{
				Old:      editor.NewBuffer(oldText),
				New:      editor.NewBuffer(string(data)),
				Layout:   editor.DiffSplit,
				Inline:   editor.DiffInlineWord,
				Editable: false,
			}
			a.tabs = append(a.tabs, &tab{
				path:    path,
				name:    filepath.Base(rel) + " (HEAD)",
				diffRel: rel,
				diff:    d,
			})
			a.active = len(a.tabs) - 1
		}
	})
}

// compareWithHEAD opens the diff of a file tab against HEAD.
func (a *app) compareWithHEAD(t *tab) {
	if t == nil || t.ed == nil {
		return
	}
	a.openDiff(t.path)
}

// diffPage shows a diff tab: a header that switches the layout, and the
// viewer below it. Like an editor card, it takes its style each frame.
func (a *app) diffPage(c *ui.Context, k *colors, t *tab) {
	if t == nil || t.diff == nil {
		return
	}
	_, th, _ := themes.pick(c.Theme().Dark)
	if th == nil {
		th = editor.DefaultTheme(c.Theme())
		th.Background = k.editor
	}
	t.diff.Theme = th
	t.diff.Font, t.diff.LineHeight = code.editorFont(), prefs.EditorLineHeight
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Row(c).Height(headerH).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, t.diffRel).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine()
			ui.Spacer(c)
			c.Key("diff-split")
			diffLayoutButton(c, k, "Split", t.diff.Layout == editor.DiffSplit, func() {
				t.diff.Layout = editor.DiffSplit
			})
			c.Key("diff-unified")
			diffLayoutButton(c, k, "Unified", t.diff.Layout == editor.DiffUnified, func() {
				t.diff.Layout = editor.DiffUnified
			})
		})
		c.Key("diff-view:" + t.diffRel)
		editor.DiffView(c, t.diff).Grow(1).MinHeight(0).FillWidth()
	})
}

// diffLayoutButton is a small text button of the diff header, raised for
// the layout it selects and faced on hover.
func diffLayoutButton(c *ui.Context, k *colors, label string, on bool, choose func()) {
	b := ui.Box(c).Height(24).Padding(0, 12).Radius(6).Center().Shrink(0).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if on {
		b.Background(k.selected)
	} else if b.Hovered() {
		b.Background(k.hover)
	}
	b.Children(func() {
		ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(k.text).SingleLine()
	})
	b.OnClick(choose)
}
