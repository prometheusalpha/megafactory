package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// node is an entry of the project tree. A folder reads its children the
// first time it opens.
type node struct {
	name     string
	path     string // relative to the project, with slashes
	dir      bool
	open     bool
	loaded   bool
	ignored  bool
	children []*node
}

// tab is an open file, or the settings page, which has no editor.
type tab struct {
	path string
	name string
	ed   *editor.State
}

const settingsPath = "megafactory:settings"

// host is what the app asks of its window and the desktop. newApp's runs
// in place, without either, as tests want; main gives the window's.
type host struct {
	// run does job's I/O off the UI thread, then what it returns on it.
	run func(job func() (apply func()))
	// pickFolder asks the user for a folder and calls done with it on the
	// UI thread, unless they cancel.
	pickFolder    func(done func(path string))
	setAppearance func(source string)
	// watch reports the folders of the project at root in or under which
	// files changed, on the UI thread, until stop.
	watch func(root string, changed func(dirs []string)) (stop func())
	// trash moves a file or folder to the Trash; it may block.
	trash  func(path string) error
	reveal func(path string)
	// ask asks the user q, and calls done on the UI thread with the
	// button they chose, unless they dismissed it.
	ask       func(q question, done func(button int))
	showError func(title, message string)
	// closeWindow closes the window as its close button does.
	closeWindow func()
}

// question is a message the user answers with one of its buttons, the
// first by default; Cancel is the last. destructive is the button that
// loses data, -1 for none.
type question struct {
	message, detail string
	buttons         []string
	destructive     int
}

// The cards that can hold the user's attention.
const (
	focusSidebar = iota
	focusEditor
	focusBottom
)

type app struct {
	host host

	// project is the open folder's path, "" for none; root is its tree.
	project   string
	stopWatch func()
	branch    string
	root      *node

	tabs   []*tab
	active int
	focus  int

	showSidebar bool
	sidebarW    float32
	sidePane    int // 0 Project, 1 Git
	selected    string
	treeFocus   string // the path of the tree's row with the focus
	revealing   string // the path the tree opens its folders to show
	edit        *treeEdit
	clip        *clipboard

	showBottom bool
	bottomH    float32
	bottomTab  int

	runConfig  string
	runConfigs []string
}

// newApp opens no project: main opens the one of st once the window is
// there to load it.
func newApp(st windowState) *app {
	return &app{
		host: host{
			run:           func(job func() func()) { job()() },
			pickFolder:    func(func(string)) {},
			setAppearance: func(string) {},
			watch:         func(string, func([]string)) func() { return func() {} },
			trash:         func(string) error { return errors.New("no Trash without the app") },
			reveal:        func(string) {},
			ask:           func(_ question, done func(int)) { done(0) },
			closeWindow:   func() {},
			showError:     func(title, msg string) { log.Printf("%s: %s", title, msg) },
		},
		branch:      "main",
		focus:       focusEditor,
		showSidebar: st.ShowSidebar,
		sidebarW:    st.SidebarW,
		showBottom:  st.ShowBottom,
		bottomH:     st.BottomH,
		runConfig:   "DemoApplication",
		runConfigs:  []string{"DemoApplication", "mvn clean install", "All tests"},
	}
}

const (
	sidebarDefault = 270
	bottomDefault  = 220
)

func (a *app) state() windowState {
	return windowState{Project: a.project, ShowSidebar: a.showSidebar, SidebarW: a.sidebarW, ShowBottom: a.showBottom, BottomH: a.bottomH}
}

func (a *app) view(c *ui.Context) {
	c.OnShortcut(ui.Cmd, ui.KeyB, func() { a.showSidebar = !a.showSidebar })
	c.OnShortcut(ui.Cmd, ui.KeyJ, func() { a.showBottom = !a.showBottom })
	c.OnShortcut(ui.Cmd, ui.KeyO, a.openFolder)
	c.OnShortcut(ui.Cmd, ui.KeyComma, a.openSettings)
	// On macOS the File menu takes these first, and does the same.
	c.OnShortcut(ui.Cmd, ui.KeyS, a.saveActive)
	c.OnShortcut(ui.Cmd, ui.KeyW, a.closeActive)
	k := colorsOf(c)
	c.Root().Background(k.bgBottom)
	ui.Column(c).Fill().Draw(func(p *ui.Painter, r ui.Rect) { paintBackground(p, r, k) }).Children(func() {
		a.titleBar(c, k)
		ui.Column(c).Grow(1).MinHeight(0).PaddingX(gap).Children(func() {
			ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch).Children(func() {
				if a.showSidebar {
					a.sidebar(c, k).Width(a.sidebarW).Shrink(0)
					divider(c, k, "sidebar", false, &a.sidebarW, 1, 180, 600, sidebarDefault)
				}
				a.editorCard(c, k).Grow(1)
			})
			// The bottom panel spans the whole width, under the sidebar too.
			if a.showBottom {
				divider(c, k, "bottom", true, &a.bottomH, -1, 100, 700, bottomDefault)
				a.bottomPanel(c, k).Height(a.bottomH).Shrink(0)
			}
		})
		a.statusBar(c, k)
	})
}

// divider sits in the gap between two cards and resizes the one whose
// size it holds: sign is 1 when that card is before it, -1 after.
func divider(c *ui.Context, k *colors, key string, vertical bool, size *float32, sign, lo, hi, reset float32) {
	div := ui.Box(c.Key(key)).Role(ui.RoleSplitter).Label("Divider").Shrink(0)
	if vertical {
		div.Height(gap).Cursor(ui.CursorResizeRow)
	} else {
		div.Width(gap).Cursor(ui.CursorResizeColumn)
	}
	if dx, dy, ok := div.Dragged(); ok {
		d := dx
		if vertical {
			d = dy
		}
		*size = min(max(*size+sign*d, lo), hi)
	}
	if div.DoubleClicked() {
		*size = reset
	}
	if div.Hovered() || div.Dragging() || div.Pressed() {
		div.Draw(func(p *ui.Painter, r ui.Rect) {
			if vertical {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 18, Y: r.Y + r.H/2 - 1.5, W: 36, H: 3}, k.iconMuted.Alpha(0.6), 1.5)
			} else {
				p.Fill(ui.Rect{X: r.X + r.W/2 - 1.5, Y: r.Y + r.H/2 - 18, W: 3, H: 36}, k.iconMuted.Alpha(0.6), 1.5)
			}
		})
	}
}

// titleBar draws the title bar beside the traffic lights, which drags the
// window.
func (a *app) titleBar(c *ui.Context, k *colors) {
	bar := c.TitleBar()
	left := bar.Left
	if left == 0 {
		left = 12 // in full screen, or beside no controls
	} else {
		left += 14
	}
	ui.Row(c).Height(titleH).Padding(0, max(bar.Right, 12), 0, left).Gap(8).AlignItems(ui.Center).DragWindow().Children(func() {
		if iconButton(c, k, "panel-left", "Toggle Sidebar  ⌘B", 30, 17).Clicked() {
			a.showSidebar = !a.showSidebar
		}
		// Until Recent Projects (1.9), the project's chip opens a folder.
		label := "Open Folder…"
		if a.root != nil {
			label = a.root.name
		}
		if chip(c, k, "folder-open", label).Tooltip("Open Folder  ⌘O").Clicked() {
			a.openFolder()
		}
		if a.root != nil {
			chip(c, k, "git-branch", a.branch).Tooltip("Branches")
		}
		ui.Spacer(c)
		a.runConfigSelect(c, k)
		ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
			iconButton(c, k, "play", "Run '"+a.runConfig+"'", 30, 16)
			iconButton(c, k, "bug", "Debug '"+a.runConfig+"'", 30, 17)
			iconButton(c, k, "refresh-cw", "Sync Maven/Gradle", 30, 16)
			if iconButton(c, k, "panel-bottom", "Toggle Panel  ⌘J", 30, 17).Clicked() {
				a.showBottom = !a.showBottom
			}
			if iconButton(c, k, "settings-2", "Settings  ⌘,", 30, 16).Clicked() {
				a.openSettings()
			}
		})
	})
}

// chip is a capsule button of the title bar: an icon and a label.
func chip(c *ui.Context, k *colors, iconName, label string) ui.Element {
	e := ui.Row(c).Height(28).Padding(0, 12, 0, 10).Gap(7).AlignItems(ui.Center).Radius(14).Shrink(0).
		Background(k.track).Border(0.5, k.trackBorder).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label)
	if e.Pressed() {
		e.Background(k.pressed)
	} else if e.Hovered() {
		e.Background(k.hover)
	}
	e.Children(func() {
		ui.Icon(c, icon(iconName)).Size(15, 15).TextColor(k.iconMuted)
		ui.Text(c, label).FontSize(13).FontWeight(600).TextColor(k.text).SingleLine()
	})
	return e
}

// runConfigSelect chooses the run configuration, as a chip of the title
// bar whose options drop in a panel.
func (a *app) runConfigSelect(c *ui.Context, k *colors) {
	sel := ui.SelectBase(c, &a.runConfig)
	tr := sel.Trigger.Row().Height(28).MaxWidth(240).Padding(0, 8, 0, 12).Gap(7).AlignItems(ui.Center).Radius(14).Shrink(1).
		Background(k.track).Border(0.5, k.trackBorder).Cursor(ui.CursorPointer).Label("Run Configuration")
	if tr.Hovered() || sel.Open() {
		tr.Background(k.hover)
	}
	tr.Children(func() {
		ui.Text(c, a.runConfig).FontSize(13).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		ui.Icon(c, icon("chevron-down")).Size(14, 14).TextColor(k.iconMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Margin(6, 0, 0, 0).Padding(4).MinWidth(220).Radius(12).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 8, 24, 0, k.shadowFocused)
		for _, cfg := range a.runConfigs {
			item := sel.Item(cfg).Row().Height(28).Padding(0, 10).Gap(8).AlignItems(ui.Center).Radius(8)
			if item.Highlighted() {
				item.Background(k.selected)
			}
			item.Children(func() {
				ui.Icon(c, icon("play")).Size(13, 13).TextColor(k.iconMuted)
				ui.Text(c, cfg).FontSize(13).TextColor(k.text).SingleLine()
			})
		}
	})
}

// focusable makes a card take the user's attention when pressed.
func (a *app) focusable(c *ui.Context, k *colors, which int) ui.Element {
	e := card(c, k, a.focus == which)
	if e.Pressed() {
		a.focus = which
	}
	return e
}

func (a *app) sidebar(c *ui.Context, k *colors) ui.Element {
	e := a.focusable(c, k, focusSidebar)
	e.Children(func() {
		ui.Row(c).Height(headerH).Padding(0, 8).AlignItems(ui.Center).Children(func() {
			tabs := ui.TabsBase(c, &a.sidePane, 2)
			track(k, tabs.List).Children(func() {
				for i, l := range []string{"Project", "Git"} {
					trackTab(k, tabs.Tab(i), i == a.sidePane).Padding(0, 12).Children(func() {
						ui.Text(c, l).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine()
					})
				}
			})
			ui.Spacer(c)
			// IntelliJ's Select Opened File.
			if a.sidePane == 0 && len(a.tabs) > 0 && a.tabs[a.active].ed != nil {
				if iconButton(c, k, "locate-fixed", "Select Opened File", 26, 15).Clicked() {
					a.revealInTree(a.tabs[a.active].path)
				}
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			switch {
			case a.sidePane == 0 && a.root == nil:
				placeholder(c, k, "No folder open")
			case a.sidePane == 0:
				a.tree(c, k)
			default:
				placeholder(c, k, "Changes — Phase 3")
			}
		})
	})
	return e
}

func (a *app) tree(c *ui.Context, k *colors) {
	tree := ui.TreeBase(c)
	if a.revealing != "" {
		a.expandToReveal()
	}
	// focused is the row with the keyboard's focus, which the tree's
	// shortcuts act on.
	var focused *node
	var item func(n *node)
	item = func(n *node) {
		c.Key(n.path)
		var open *bool
		if n.dir {
			open = &n.open
		}
		it := tree.Item(open)
		row := it.Row.Height(26).Gap(6).Padding(0, 8).PaddingStart(8 + 14*float32(it.Depth())).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorPointer)
		if a.revealing != "" && a.revealing == n.path {
			a.revealing = ""
			// The focus too, so that the tree's keys act on it.
			row.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollCenter}).Focus()
		}
		if a.selected == n.path {
			row.Background(k.selected)
		} else if row.Hovered() {
			row.Background(k.hover)
		}
		renaming := a.edit != nil && a.edit.node == n
		if row.Clicked() && !renaming {
			a.selected = n.path
			if n.dir {
				n.open = !n.open
			} else {
				a.openFile(n)
			}
		}
		// The selection follows the focus as the keys move it, but stays
		// where an action put it until the focus moves.
		if row.Focused() {
			focused = n
			if a.treeFocus != n.path {
				a.treeFocus, a.selected = n.path, n.path
			}
		}
		if row.RightClicked() {
			a.selected = n.path
		}
		svc := c.Services()
		row.ContextMenu(func(m *ui.Menu) { a.treeMenu(svc, m, n) })
		// The tree's keys open folders too, through n.open.
		if n.dir && n.open && !n.loaded {
			a.loadDir(n)
		}
		// What git ignores is dimmed, not hidden.
		iconCol, textCol := k.iconMuted, k.text
		if n.ignored {
			iconCol, textCol = k.iconMuted.Alpha(0.5), k.textFaint
		}
		row.Children(func() {
			if n.dir {
				arrow := ui.Icon(c, icon("chevron-right")).Size(14, 14).TextColor(iconCol)
				if n.open {
					arrow.Rotate(90)
				}
				folder := "folder"
				if n.open {
					folder = "folder-open"
				}
				ui.Icon(c, icon(folder)).Size(15, 15).TextColor(iconCol)
			} else {
				ui.Box(c).Width(14).Shrink(0)
				ui.Icon(c, icon(fileIcon(n.name))).Size(15, 15).TextColor(iconCol)
			}
			if renaming {
				a.nameInput(c, k)
			} else {
				ui.Text(c, n.name).FontSize(13).TextColor(textCol).SingleLine().Ellipsis("…")
			}
		})
		it.Group(func() {
			if a.edit != nil && a.edit.dir == n {
				a.newRow(c, k, it.Depth()+1)
			}
			for _, ch := range n.children {
				item(ch)
			}
		})
	}
	tree.Root.Padding(0, 6, 6, 6).Label("Project").Children(func() { item(a.root) })
	// Not while a name is typed, whose input has keys of its own.
	if a.edit == nil {
		a.treeShortcuts(tree.Root, func() *node { return focused })
	}
}

func (a *app) openFile(n *node) {
	a.focus = focusEditor
	if a.activate(n.path) {
		return
	}
	tree, file := a.root, filepath.Join(a.project, filepath.FromSlash(n.path))
	a.host.run(func() func() {
		data, err := os.ReadFile(file)
		return func() {
			if a.root != tree {
				return
			}
			if err != nil {
				log.Printf("open %s: %v", n.path, err)
				return
			}
			// A second click may have opened it while the first read.
			if a.activate(n.path) {
				return
			}
			buf := editor.NewBuffer(string(data))
			buf.SetLanguage(languageFor(n.name))
			a.tabs = append(a.tabs, &tab{path: n.path, name: n.name, ed: &editor.State{Buffer: buf, WrapGuides: []int{120}}})
			a.active = len(a.tabs) - 1
		}
	})
}

func (a *app) openSettings() {
	a.focus = focusEditor
	if !a.activate(settingsPath) {
		a.tabs = append(a.tabs, &tab{path: settingsPath, name: "Settings"})
		a.active = len(a.tabs) - 1
	}
}

// activate makes the tab of path the active one, if it is open.
func (a *app) activate(path string) bool {
	i := slices.IndexFunc(a.tabs, func(t *tab) bool { return t.path == path })
	if i >= 0 {
		a.active = i
	}
	return i >= 0
}

func (a *app) closeTab(i int) {
	a.tabs = slices.Delete(a.tabs, i, i+1)
	if a.active >= len(a.tabs) || a.active > i {
		a.active = max(0, a.active-1)
	}
}

func (a *app) editorCard(c *ui.Context, k *colors) ui.Element {
	e := a.focusable(c, k, focusEditor).Background(k.editor)
	e.Children(func() {
		if len(a.tabs) == 0 {
			ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
				if a.root == nil {
					ui.Text(c, "No folder open").FontSize(16).FontWeight(600).TextColor(k.text)
					ui.Text(c, "Open a project folder · ⌘O").FontSize(12.5).TextColor(k.textMuted)
					if chip(c, k, "folder-open", "Open Folder…").Margin(10, 0, 0, 0).Clicked() {
						a.openFolder()
					}
					return
				}
				ui.Text(c, "No file open").FontSize(16).FontWeight(600).TextColor(k.text)
				ui.Text(c, "Open a file from the project tree · ⌘B sidebar · ⌘J panel").FontSize(12.5).TextColor(k.textMuted)
			})
			return
		}
		a.tabBar(c, k)
		// A tab's menu may have closed them all; the next pass shows so.
		if len(a.tabs) == 0 {
			return
		}
		cur := a.tabs[a.active]
		if cur.ed == nil {
			a.settingsPage(c, k)
			return
		}
		_, th, _ := themes.pick(c.Theme().Dark)
		if th == nil {
			th = editor.DefaultTheme(c.Theme())
			th.Background = k.editor
		}
		cur.ed.Theme = th
		cur.ed.Font, cur.ed.LineHeight = code.editorFont(), prefs.EditorLineHeight
		// Each file keeps its own editor widget state (scroll, focus).
		c.Key(cur.path)
		editor.View(c, cur.ed).Grow(1).MinHeight(0).FillWidth().AutoFocus()
	})
	return e
}

func (a *app) tabBar(c *ui.Context, k *colors) {
	ui.ScrollHorizontal(c).FillWidth().Shrink(0).Children(func() {
		ui.Row(c).Height(headerH).Padding(0, 8).AlignItems(ui.Center).Children(func() {
			tabs := ui.TabsBase(c, &a.active, len(a.tabs))
			track(k, tabs.List).Children(func() {
				// A tab's menu closes tabs as it builds: the pass goes on
				// over those it began with, and the next shows the rest.
				for i, tb := range slices.Clone(a.tabs) {
					c.Key(tb.path)
					active := i == a.active
					t := trackTab(k, tabs.Tab(i), active).Padding(0, 4, 0, 10)
					svc := c.Services()
					t.ContextMenu(func(m *ui.Menu) { a.tabMenu(svc, m, tb) })
					hovered := t.Hovered()
					t.Children(func() {
						iconName := "settings-2"
						if tb.ed != nil {
							iconName = fileIcon(tb.name)
						}
						ui.Icon(c, icon(iconName)).Size(14, 14).TextColor(k.iconMuted)
						col := k.textMuted
						if active {
							col = k.text
						}
						label := tb.name
						if tb.ed != nil && tb.ed.Buffer.Modified() {
							label = "● " + label
						}
						ui.Text(c, label).FontSize(12.5).FontWeight(500).TextColor(col).SingleLine()
						x := iconButton(c, k, "x", "Close "+tb.name, 20, 13)
						if !active && !hovered {
							x.Opacity(0)
						}
						x.OnClick(func() { a.requestClose(tb) })
					})
				}
			})
		})
	})
}

func (a *app) bottomPanel(c *ui.Context, k *colors) ui.Element {
	type page struct{ icon, label string }
	pages := []page{{"play", "Run"}, {"bug", "Debug"}, {"terminal", "Terminal"}, {"circle-alert", "Problems"}}
	e := a.focusable(c, k, focusBottom)
	e.Children(func() {
		ui.Row(c).Height(headerH).Padding(0, 6, 0, 8).AlignItems(ui.Center).Children(func() {
			tabs := ui.TabsBase(c, &a.bottomTab, len(pages))
			track(k, tabs.List).Children(func() {
				for i, p := range pages {
					trackTab(k, tabs.Tab(i), i == a.bottomTab).Padding(0, 12, 0, 10).Children(func() {
						ui.Icon(c, icon(p.icon)).Size(14, 14).TextColor(k.iconMuted)
						ui.Text(c, p.label).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine()
					})
				}
			})
			ui.Spacer(c)
			if iconButton(c, k, "x", "Hide Panel  ⌘J", 26, 15).Clicked() {
				a.showBottom = false
			}
		})
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			switch a.bottomTab {
			case 0:
				placeholder(c, k, "Console output — Phase 4")
			case 1:
				placeholder(c, k, "Variables · Watches · Call stack — Phase 5")
			case 2:
				placeholder(c, k, "Terminal (libghostty) — Phase 4")
			case 3:
				placeholder(c, k, "Problems from jdtls — Phase 2")
			}
		})
	})
	return e
}

func (a *app) statusBar(c *ui.Context, k *colors) {
	ui.Row(c).Height(26).Gap(16).Padding(0, 16).AlignItems(ui.Center).Children(func() {
		faint := func(s string) { ui.Text(c, s).FontSize(11.5).TextColor(k.textFaint).SingleLine() }
		if a.root != nil {
			faint(a.branch)
			faint("jdtls: not started")
		}
		ui.Spacer(c)
		if len(a.tabs) > 0 && a.tabs[a.active].ed != nil {
			ed := a.tabs[a.active].ed
			pos := ed.CursorPos()
			faint(fmt.Sprintf("Ln %d, Col %d", pos.Line+1, pos.Col+1))
			lang := "Plain Text"
			if l := ed.Buffer.Language(); l != nil {
				lang = l.Name
			}
			faint(lang)
		}
	})
}

func placeholder(c *ui.Context, k *colors, s string) {
	ui.Column(c).Grow(1).Center().Padding(16).Children(func() {
		ui.Text(c, s).FontSize(12.5).TextColor(k.textMuted)
	})
}
