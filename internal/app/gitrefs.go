package app

import (
	"strings"

	"megafactory/internal/git"

	"github.com/egoist/mygo/ui"
)

// branchPicker is the searchable Branches dialog: it lists the local,
// remote and tag refs and offers to create a branch or check out a raw
// revision. sel is the chosen row of the flat items list; shown is the row
// last scrolled into view, so keys move it only once per selection.
type branchPicker struct {
	open  bool
	query string
	sel   int
	shown int
	// creating reveals the new-branch name input in place of the search.
	creating bool
	newName  string
}

// checkoutPrompt remembers a checkout git refused, so the stash-and-retry
// offer can run that very same checkout once the working tree is clean.
type checkoutPrompt struct {
	ref git.Ref
	rev string
	err error
}

func (p *checkoutPrompt) run(repo git.Repo) error {
	if p.rev != "" {
		return repo.CheckoutRevision(p.rev)
	}
	return repo.Checkout(p.ref)
}

// pickerItem is a selectable row. An empty action marks a ref row; the
// group names the dim header the row is listed under.
type pickerItem struct {
	group  string
	ref    git.Ref
	action string
	label  string
}

func (a *App) openBranchPicker() {
	if !a.git.ok {
		return
	}
	repo, project := a.git.repo, a.project
	a.host.Run(func() func() {
		refs, err := repo.Refs()
		return func() {
			if a.project != project || a.git.repo.Dir != repo.Dir {
				return
			}
			if err != nil {
				a.host.ShowError("Couldn't load branches", err.Error())
			}
			a.git.refs = refs
			a.git.picker = &branchPicker{open: true, shown: -1}
		}
	})
}

func (a *App) pickerItems(p *branchPicker) []pickerItem {
	q := strings.ToLower(p.query)
	match := func(s string) bool {
		return q == "" || strings.Contains(strings.ToLower(s), q)
	}
	groups := []struct {
		title string
		kind  git.RefKind
	}{
		{"Local Branches", git.LocalBranch},
		{"Remote Branches", git.RemoteBranch},
		{"Tags", git.Tag},
	}
	var items []pickerItem
	for _, g := range groups {
		for _, r := range a.git.refs {
			if r.Kind == g.kind && match(r.Name) {
				items = append(items, pickerItem{group: g.title, ref: r, label: r.Name})
			}
		}
	}
	if q == "" || match("Create new branch from HEAD") {
		items = append(items, pickerItem{group: "Actions", action: "create", label: "Create new branch from HEAD…"})
	}
	if q != "" {
		items = append(items, pickerItem{group: "Actions", action: "revision", label: "Checkout revision " + p.query + "…"})
	}
	return items
}

func (a *App) pickerChoose(p *branchPicker, it pickerItem) {
	switch it.action {
	case "create":
		p.creating = true
		p.newName = ""
	case "revision":
		a.checkoutRevision(p.query)
	default:
		a.checkoutRef(it.ref)
	}
}

func (a *App) branchPickerView(c *ui.Context, k *colors) {
	p := a.git.picker
	if p == nil {
		return
	}
	items := a.pickerItems(p)
	p.sel = min(max(p.sel, 0), max(len(items)-1, 0))
	ui.DialogBase(c.Key("branches"), &p.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.18))
		panel.Width(560).MaxHeight(460).Padding(6).Gap(4).Radius(14).
			Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 12, 40, 0, k.shadowFocused).Label("Branches")
		panel.Children(func() {
			ui.Row(c).Height(36).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Icon(c, icon("search")).Size(15, 15).TextColor(k.iconMuted)
				in := ui.TextInputBase(c, &p.query).Label("Search branches").
					Placeholder("Search branches or enter a revision").
					FontSize(14).TextColor(k.text).Grow(1)
				if !p.creating {
					in = in.AutoFocus()
				}
				in.OnTextKey(func(key ui.TextKey) bool {
					switch key.Key {
					case ui.KeyDown:
						p.sel = min(p.sel+1, max(len(items)-1, 0))
					case ui.KeyUp:
						p.sel = max(p.sel-1, 0)
					case ui.KeyEnter:
						if len(items) > 0 {
							a.pickerChoose(p, items[p.sel])
						}
					case ui.KeyEscape:
						p.open = false
					default:
						return false
					}
					return true
				})
				if in.Changed() {
					p.sel = 0
				}
			})
			if p.creating {
				ui.Row(c).Height(36).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Icon(c, icon("plus")).Size(15, 15).TextColor(k.iconMuted)
					name := ui.TextInputBase(c, &p.newName).Label("New branch name").
						Placeholder("Branch name").FontSize(13).TextColor(k.text).Grow(1).AutoFocus()
					name.OnTextKey(func(key ui.TextKey) bool {
						switch key.Key {
						case ui.KeyEnter:
							a.createBranch(p.newName)
						case ui.KeyEscape:
							p.creating = false
						default:
							return false
						}
						return true
					})
				})
			}
			ui.Divider(c)
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
				group := ""
				for i, it := range items {
					if it.group != group {
						group = it.group
						c.Key("group:" + group)
						ui.Text(c, group).FontSize(11).FontWeight(600).
							TextColor(k.textFaint).Padding(8, 10, 2, 10).SingleLine()
					}
					a.pickerRow(c, k, p, i, it)
				}
			})
		})
	})
	if !p.open {
		a.git.picker = nil
	}
}

func (a *App) pickerRow(c *ui.Context, k *colors, p *branchPicker, i int, it pickerItem) {
	c.Key(i)
	row := ui.Row(c).Height(36).Padding(0, 6, 0, 10).Gap(10).Radius(9).AlignItems(ui.Center).
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
	iconName := "git-branch"
	switch it.action {
	case "create":
		iconName = "plus"
	case "revision":
		iconName = "chevron-right"
	}
	row.Label(it.label).Children(func() {
		ui.Icon(c, icon(iconName)).Size(16, 16).TextColor(k.iconMuted)
		ui.Text(c, it.label).FontSize(13).TextColor(k.text).SingleLine()
		ui.Spacer(c)
	})
	row.OnClick(func() { a.pickerChoose(p, it) })
}

func (a *App) checkoutRef(ref git.Ref) {
	a.runCheckout(&checkoutPrompt{ref: ref})
}

func (a *App) checkoutRevision(rev string) {
	a.runCheckout(&checkoutPrompt{rev: rev})
}

func (a *App) runCheckout(p *checkoutPrompt) {
	repo, project := a.git.repo, a.project
	a.host.Run(func() func() {
		err := p.run(repo)
		var dirty bool
		if err != nil {
			dirty, _ = repo.Dirty()
		}
		return func() {
			if a.project != project || a.git.repo.Dir != repo.Dir {
				return
			}
			if err == nil {
				a.git.prompt = nil
				a.git.picker = nil
				a.gitRefresh()
				return
			}
			p.err = err
			if !dirty {
				a.git.prompt = nil
				a.host.ShowError("Checkout failed", err.Error())
				return
			}
			// Git's own refusal stays in the detail: it names the files that
			// would be lost, which is why a stash is being offered (3.9).
			a.git.prompt = p
			a.host.Ask(Question{
				Message:     "Checkout blocked by local changes",
				Detail:      err.Error() + "\n\nStash your changes and check out anyway?",
				Buttons:     []string{"Stash & Checkout", "Cancel"},
				Destructive: -1,
			}, func(btn int) {
				pending := a.git.prompt
				a.git.prompt = nil
				if btn != 0 || pending == nil {
					return
				}
				a.host.Run(func() func() {
					stErr := repo.Stash("megafactory: before checkout")
					var retryErr error
					if stErr == nil {
						retryErr = pending.run(repo)
					}
					return func() {
						if a.project != project || a.git.repo.Dir != repo.Dir {
							return
						}
						if stErr != nil {
							a.host.ShowError("Checkout failed", stErr.Error())
							return
						}
						if retryErr != nil {
							a.host.ShowError("Checkout failed", retryErr.Error())
							return
						}
						a.git.picker = nil
						a.gitRefresh()
					}
				})
			})
		}
	})
}

func (a *App) createBranch(name string) {
	repo, project := a.git.repo, a.project
	a.host.Run(func() func() {
		err := repo.CreateBranch(name)
		return func() {
			if a.project != project || a.git.repo.Dir != repo.Dir {
				return
			}
			if err != nil {
				a.host.ShowError("Couldn't create branch", err.Error())
				return
			}
			a.git.picker = nil
			a.gitRefresh()
		}
	})
}
