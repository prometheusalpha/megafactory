package app

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
func (a *App) gitDiffBase(t *tab) {
	if !a.git.ok || t == nil || t.ed == nil {
		return
	}
	repo, project := a.git.repo.Dir, a.project
	g, rel := a.git.repo, a.gitRel(t.path)
	a.host.Run(func() func() {
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
func (a *App) openDiff(rel string) {
	if a.activate(diffPath(rel)) {
		return
	}
	a.loadDiff(rel, func(d *editor.DiffState) {
		a.tabs = append(a.tabs, &tab{
			path:    diffPath(rel),
			name:    diffName(rel),
			diffRel: rel,
			diff:    d,
		})
		a.active = len(a.tabs) - 1
	})
}

func diffPath(rel string) string { return "megafactory:diff:" + rel }
func diffName(rel string) string { return filepath.Base(rel) + " (HEAD)" }

// loadDiff reads rel's HEAD and working texts off the UI thread, then
// gives show their diff, unless the project changed or a tab of that diff
// opened meanwhile.
func (a *App) loadDiff(rel string, show func(d *editor.DiffState)) {
	if !a.git.ok {
		return
	}
	repo, project := a.git.repo.Dir, a.project
	g, gitPath, abs := a.git.repo, a.gitRel(rel), a.abs(rel)
	a.host.Run(func() func() {
		oldText := ""
		if text, ok, err := g.Show("HEAD", gitPath); err == nil && ok {
			oldText = text
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			data = nil // a deleted file has no working text
		}
		return func() {
			if a.project != project || a.git.repo.Dir != repo || a.activate(diffPath(rel)) {
				return
			}
			show(&editor.DiffState{
				Old:      editor.NewBuffer(oldText),
				New:      editor.NewBuffer(string(data)),
				Layout:   editor.DiffSplit,
				Inline:   editor.DiffInlineWord,
				Editable: false,
			})
		}
	})
}

// diffNeighbor is the changed file before (dir -1) or after (dir 1) rel
// in the Changes page's order, "" when rel is at that end or not listed.
func (a *App) diffNeighbor(rel string, dir int) string {
	var rels []string
	for _, s := range a.gitSortedChanges() {
		if r := a.projectRel(s.Path); r != "" {
			rels = append(rels, r)
		}
	}
	i := slices.Index(rels, rel)
	if i < 0 || i+dir < 0 || i+dir >= len(rels) {
		return ""
	}
	return rels[i+dir]
}

// diffStepHunk is the change a step in dir goes to from the caret, -1
// when there is none that way.
func diffStepHunk(d *editor.DiffState, dir int) int {
	i, inside := d.CurrentHunk()
	if dir < 0 && !inside {
		i++ // the last change before the caret is the one a step back goes to
	}
	if j := i + dir; j >= 0 && j < len(d.Hunks()) {
		return j
	}
	return -1
}

// diffStep goes to the next (dir 1) or previous (dir -1) change of t. Past
// the last or first, a first press only arms; a second one, with the
// caret where the first left it, shows the next or previous file in t,
// at its first or last change, as IntelliJ does.
func (a *App) diffStep(t *tab, dir int) {
	if h := diffStepHunk(t.diff, dir); h >= 0 {
		t.diffArm = 0
		t.diff.GoToHunk(h)
		return
	}
	next := a.diffNeighbor(t.diffRel, dir)
	if next == "" {
		return
	}
	if !diffArmed(t, dir) {
		t.diffArm, t.diffArmAt = dir, t.diff.Cursor()
		return
	}
	t.diffArm = 0
	a.loadDiff(next, func(d *editor.DiffState) {
		if !slices.Contains(a.tabs, t) {
			return
		}
		// The new file keeps the layout the user picked for this one.
		d.Layout, d.SplitPercent = t.diff.Layout, t.diff.SplitPercent
		t.path, t.name, t.diffRel, t.diff = diffPath(next), diffName(next), next, d
		if dir > 0 {
			d.GoToHunk(0)
		} else {
			d.GoToHunk(len(d.Hunks()) - 1)
		}
	})
}

// diffArmed tells whether a press in dir is the second past the end.
func diffArmed(t *tab, dir int) bool {
	return t.diffArm == dir && t.diffArmAt == t.diff.Cursor()
}

// compareWithHEAD opens the diff of a file tab against HEAD.
func (a *App) compareWithHEAD(t *tab) {
	if t == nil || t.ed == nil {
		return
	}
	a.openDiff(t.path)
}

// diffPage shows a diff tab: a header that switches the layout, and the
// viewer below it. Like an editor card, it takes its style each frame.
func (a *App) diffPage(c *ui.Context, k *colors, t *tab) {
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
			for _, dir := range []int{-1, 1} {
				icon, label := "arrow-up", "Previous Difference"
				if dir > 0 {
					icon, label = "arrow-down", "Next Difference"
				}
				can := diffStepHunk(t.diff, dir) >= 0 || a.diffNeighbor(t.diffRel, dir) != ""
				c.Key(icon)
				diffNavButton(c, k, icon, label, can, func() { a.diffStep(t, dir) })
			}
			switch {
			case diffArmed(t, 1):
				ui.Text(c, "Click again for next file").FontSize(11.5).TextColor(k.textMuted).SingleLine()
			case diffArmed(t, -1):
				ui.Text(c, "Click again for previous file").FontSize(11.5).TextColor(k.textMuted).SingleLine()
			}
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

// diffNavButton is an iconButton of the diff header that can be disabled,
// dimmed and deaf to clicks, which iconButton cannot.
func diffNavButton(c *ui.Context, k *colors, name, label string, enabled bool, step func()) {
	if enabled {
		iconButton(c, k, name, label, 24, 14).OnClick(step)
		return
	}
	ui.Box(c).Size(24, 24).Center().Role(ui.RoleButton).Label(label).Children(func() {
		ui.Icon(c, icon(name)).Size(14, 14).TextColor(k.iconMuted.Alpha(0.35))
	})
}
