package app

import (
	"path/filepath"
	"slices"
	"strings"

	"megafactory/internal/git"

	"github.com/egoist/mygo/ui"
)

// gitService is the Git state the status bar and the sidebar's Git page
// show, owned by the UI thread.
type gitService struct {
	repo       git.Repo
	ok         bool
	head       string
	changes    []git.Status
	err        error
	loading    bool
	refs       []git.Ref
	picker     *branchPicker         // type in gitrefs.go
	prompt     *checkoutPrompt       // type in gitrefs.go
	blame      map[string]*blameFile // type in gitblame.go
	blameLine  string
	blameCaret int
}

// gitProjectOpened starts the Git state over for the project that just
// opened, then reads the repository it may be in.
func (a *App) gitProjectOpened() {
	a.git = gitService{blame: map[string]*blameFile{}}
	a.gitRefresh()
}

// gitRefresh reads the branch, HEAD and working-tree changes of the
// project's repository. The git commands run off the UI thread; their
// result is dropped if another project opened meanwhile.
func (a *App) gitRefresh() {
	// A flood of watch hints must not pile up refreshes; a refresh after
	// a git command runs once this one's apply has reset loading.
	if a.git.loading {
		return
	}
	a.git.loading = true
	project := a.project
	a.host.Run(func() func() {
		repo, ok := git.Repo{}, false
		if project != "" {
			repo, ok = git.Detect(project)
		}
		var branch, head string
		var changes []git.Status
		var err error
		if ok {
			if branch, err = repo.Branch(); err == nil {
				if head, err = repo.Head(); err == nil {
					changes, err = repo.Statuses()
				}
			}
		}
		return func() {
			a.git.loading = false
			if a.project != project {
				return
			}
			if !ok {
				a.git.repo = git.Repo{}
				a.git.ok = false
				a.git.changes, a.git.head, a.git.err = nil, "", nil
				return // a.branch belongs to the project that was here
			}
			a.git.repo = repo
			a.git.ok = true
			if branch != "" {
				a.branch = branch
			}
			if err != nil {
				a.git.err = err
				a.git.changes, a.git.head = nil, ""
				return
			}
			prev := a.git.head
			a.git.err = nil
			a.git.changes = changes
			a.git.head = head
			if head != prev {
				a.gitHeadChanged()
			}
		}
	})
}

// gitHeadChanged drops blame kept for the old HEAD and reads it again,
// with the diff base, for every open file.
func (a *App) gitHeadChanged() {
	clear(a.git.blame)
	for _, t := range a.tabs {
		if t.ed != nil {
			a.gitDiffBase(t)
			a.gitBlame(t)
		}
	}
}

func (a *App) gitTabOpened(t *tab) {
	if a.git.ok && t.ed != nil {
		a.gitDiffBase(t)
		a.gitBlame(t)
	}
}

func (a *App) gitTabClosed(t *tab) {
	delete(a.git.blame, t.path)
}

// gitBranchChip is the status bar's branch: it opens the ref picker when
// the project is a repository, and says so when it is not.
func (a *App) gitBranchChip(c *ui.Context, k *colors) {
	e := ui.Row(c).Gap(5).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Current branch")
	e.Children(func() {
		ui.Icon(c, icon("git-branch")).Size(12, 12).TextColor(k.textFaint)
		ui.Text(c, a.branch).FontSize(11.5).TextColor(k.text).SingleLine()
	})
	e.OnClick(a.clickBranch)
}

// clickBranch is shared by the title bar's and the status bar's branch
// chips: open the picker, or say why it can't be.
func (a *App) clickBranch() {
	if a.git.ok {
		a.openBranchPicker()
	} else {
		a.host.ShowError("Not a git repository", "This project is not a git repository.")
	}
}

// changesPage lists the working tree's changes, which a click compares
// with HEAD, as problemsPage and buildPage do.
func (a *App) changesPage(c *ui.Context, k *colors) {
	if !a.git.ok {
		placeholder(c, k, "Not a git repository")
		return
	}
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Padding(6).Gap(1).Children(func() {
			ui.Row(c).Height(30).Padding(0, 8, 0, 6).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Changes").FontSize(12).FontWeight(600).TextColor(k.textMuted).SingleLine()
				ui.Spacer(c)
				if iconButton(c, k, "refresh-cw", "Refresh Changes", 24, 14).Clicked() {
					a.gitRefresh()
				}
			})
			if a.git.err != nil {
				ui.Text(c, a.git.err.Error()).FontSize(11.5).TextColor(c.Theme().Danger).SingleLine().Padding(6)
				return
			}
			if len(a.git.changes) == 0 {
				ui.Text(c, "No changes").FontSize(12.5).TextColor(k.textMuted).SingleLine().Padding(6)
				return
			}
			changes := slices.Clone(a.git.changes)
			slices.SortStableFunc(changes, func(x, y git.Status) int {
				if d := gitChangeOrder(x.Kind()) - gitChangeOrder(y.Kind()); d != 0 {
					return d
				}
				return strings.Compare(x.Path, y.Path)
			})
			for _, s := range changes {
				a.gitChangeRow(c, k, s)
			}
		})
	})
}

// Conflicts lead, tracked changes follow, and untracked files come last.
func gitChangeOrder(kind git.Kind) int {
	switch kind {
	case git.Conflict:
		return 0
	case git.Untracked:
		return 2
	}
	return 1
}

func gitChangeLabel(c *ui.Context, kind git.Kind) (string, ui.Color) {
	th := c.Theme()
	switch kind {
	case git.Modified:
		return "Modified", th.Warning
	case git.Added:
		return "Added", th.Success
	case git.Deleted:
		return "Deleted", th.Danger
	case git.Untracked:
		return "Untracked", th.Success
	case git.Renamed:
		return "Renamed", th.Accent
	case git.Conflict:
		return "Conflict", th.Danger
	}
	return "Changed", th.TextMuted
}

func (a *App) gitChangeRow(c *ui.Context, k *colors, s git.Status) {
	rel := a.projectRel(s.Path)
	label, col := gitChangeLabel(c, s.Kind())
	c.Key("git-change:" + s.Path)
	row := ui.Row(c).Height(26).Gap(8).Padding(0, 8).AlignItems(ui.Center).Radius(8).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(s.Path)
	if row.Hovered() {
		row.Background(k.hover)
	}
	row.Children(func() {
		ui.Box(c).Size(7, 7).Radius(3.5).Shrink(0).Background(col)
		ui.Text(c, label).FontSize(11).TextColor(col).SingleLine().Shrink(0)
		ui.Text(c, s.Path).FontSize(12.5).TextColor(k.text).SingleLine().Ellipsis("…")
	})
	row.OnClick(func() {
		if rel != "" {
			a.openDiff(rel)
		}
	})
}

// gitRel turns a project-relative path into one relative to the repository
// root. projectRel is the inverse, "" for a path outside the project.
func (a *App) gitRel(rel string) string {
	return relTo(a.git.repo.Dir, a.abs(rel))
}

func (a *App) projectRel(p string) string {
	return relTo(a.project, filepath.Join(a.git.repo.Dir, filepath.FromSlash(p)))
}

// relTo is relative to root, "" when the path is an error or escapes root.
// Both sides are normalized to physical paths: git reports the toplevel's
// real path, while a project opened through a symlink keeps that alias.
func relTo(root, file string) string {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(filepath.Dir(file)); err == nil {
		file = filepath.Join(resolved, filepath.Base(file))
	}
	r, err := filepath.Rel(root, file)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return ""
	}
	return filepath.ToSlash(r)
}
