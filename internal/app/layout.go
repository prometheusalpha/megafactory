package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"time"

	"megafactory/internal/javalang"
	"megafactory/internal/jdk"
	"megafactory/internal/keymap"
	"megafactory/internal/runcfg"
	"megafactory/internal/runner"
	"megafactory/internal/setup"

	"github.com/egoist/mygo/plugins/terminal"
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
	loaded   bool // read, or being read
	read     bool // children are what the disk listed, or failed to
	ignored  bool
	children []*node
}

// tab is an open file, or the settings page, which has no editor.
type tab struct {
	path string
	name string
	ed   *editor.State
	// Diff tabs have no ed; they show a DiffState instead.
	diff    *editor.DiffState
	diffRel string
}

const settingsPath = "megafactory:settings"

// Host is what the app asks of its window and the desktop. newApp's runs
// in place, without either, as tests want; main gives the window's.
type Host struct {
	// Run does job's I/O off the UI thread, then what it returns on it.
	Run func(job func() (apply func()))
	// Update runs fn on the UI thread, from a job's goroutine, as for
	// its progress.
	Update func(fn func())
	// After runs fn on the UI thread once d has passed.
	After func(d time.Duration, fn func())
	// PickFolder asks the user for a folder and calls done with it on the
	// UI thread, unless they cancel.
	PickFolder    func(done func(path string))
	SetAppearance func(source string)
	// Watch reports the folders of the project at root in or under which
	// files changed, on the UI thread, until stop.
	Watch func(root string, changed func(dirs []string)) (stop func())
	// Trash moves a file or folder to the Trash; it may block.
	Trash  func(path string) error
	Reveal func(path string)
	// Ask asks the user q, and calls done on the UI thread with the
	// button they chose, unless they dismissed it.
	Ask       func(q Question, done func(button int))
	ShowError func(title, message string)
	// CloseWindow closes the window as its close button does.
	CloseWindow func()
	// NewTerm makes a terminal running opts. It is here, not in the view,
	// as terminal.New loads libghostty-vt, which no headless test has.
	NewTerm func(opts terminal.Options) (TermHandle, error)
	// StartProc runs a run configuration's program. It is here so tests
	// give lines without spawning a process.
	StartProc func(spec runner.Spec, onLine func(runner.Line), onExit func(error)) (*runner.Process, error)
	// JavaJDK finds or installs a JDK 25 for jdtls and for running Java.
	// Tests use a finder that never downloads.
	JavaJDK func(ctx context.Context, progress func(setup.Progress)) (jdk.JDK, []jdk.JDK, error)
}

// Question is a message the user answers with one of its buttons, the
// first by default; Cancel is the last. Destructive is the button that
// loses data, -1 for none.
type Question struct {
	Message, Detail string
	Buttons         []string
	Destructive     int
}

// The cards that can hold the user's attention.
const (
	focusSidebar = iota
	focusEditor
	focusBottom
)

// App is one window of the IDE: the open project, its tabs, and the
// panels, palette and language server around them.
type App struct {
	host Host

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
	revealFocus bool   // whether the row revealed takes the keyboard's focus
	followed    *tab   // the file tab the tree last followed
	edit        *treeEdit
	clip        *clipboard

	showBottom bool
	bottomH    float32
	bottomTab  int

	terms        []*termTab
	activeTerm   int
	termStarting bool
	termErr      error

	// runConfig is the id of the configuration the toolbar selected.
	runConfig   string
	runStore    *runcfg.Store
	runEdit     *runEditState
	runConsole  *runConsoleState
	runProc     *runner.Process
	runGen      int
	mainClasses []javalang.MainClass

	recent  []string // the projects opened last, the most recent first
	sess    sessionState
	palette *recentPalette
	files   *filePalette
	findIn  *findInFiles

	// keys is the shortcuts of keymap.Actions; keysChanged tells the
	// window, whose menu bar shows some, that one was rebound.
	keys        *keymap.Keymap
	keysChanged func()
	recording   *recording

	servers servers

	// java is the language server of the project, and builds the Build
	// page's tasks of it.
	java   javaService
	builds buildView
	// Phase 3 Git state.
	git gitService
	// debug owns the debugger's state: session, breakpoints, panels.
	debug debugService
	// pendingLine is where a tab's editor puts the caret once it shows,
	// as a problem's or a definition's open asks.
	pendingLine *openLine
}

// newApp opens no project: main opens the one of st once the window is
// there to load it.
func newApp(st State) *App {
	return &App{
		host: Host{
			Run:           func(job func() func()) { job()() },
			Update:        func(fn func()) { fn() },
			After:         func(time.Duration, func()) {},
			PickFolder:    func(func(string)) {},
			SetAppearance: func(string) {},
			Watch:         func(string, func([]string)) func() { return func() {} },
			Trash:         func(string) error { return errors.New("no Trash without the app") },
			Reveal:        func(string) {},
			Ask:           func(_ Question, done func(int)) { done(0) },
			CloseWindow:   func() {},
			ShowError:     func(title, msg string) { log.Printf("%s: %s", title, msg) },
			NewTerm:       func(terminal.Options) (TermHandle, error) { return nil, errors.New("no terminal without the app") },
			StartProc:     runner.Start,
			JavaJDK: func(ctx context.Context, _ func(setup.Progress)) (jdk.JDK, []jdk.JDK, error) {
				j, err := jdk.System.Find(ctx)
				return j, nil, err
			},
		},
		keys:        keymap.New(prefs.Keymap),
		keysChanged: func() {},
		runStore:    mustEmptyStore(),
		branch:      "main",
		focus:       focusEditor,
		showSidebar: st.ShowSidebar,
		sidebarW:    st.SidebarW,
		showBottom:  st.ShowBottom,
		bottomH:     st.BottomH,
		java:        newJavaService(),
		debug:       newDebugService(),
	}
}

// mustEmptyStore is the store of an app with no project yet. runcfg.Load
// on a missing path is the only constructor, and that cannot fail here.
func mustEmptyStore() *runcfg.Store {
	s, _ := runcfg.Load("")
	return s
}

// New is main's app: it reads the projects opened last, gives the Build
// page the servers' artifacts, and wires jdtls to start with a project.
// newApp does none of that, as tests want.
func New(st State, s Settings) *App {
	prefs, themes = s, themeNamed(s.Theme)
	a := newApp(st)
	a.recent = loadRecent()
	a.sess.store = loadSessions()
	a.servers = servers{artifacts: setup.Artifacts, client: http.DefaultClient}
	a.java.starter = startLanguageServer(a)
	return a
}

// UseHost gives the app the window and the desktop it runs against, and
// keeps the app's own JDK finder where the window offers none.
func (a *App) UseHost(h Host) {
	if h.JavaJDK == nil {
		h.JavaJDK = a.javaJDK
	}
	a.host = h
}

const (
	sidebarDefault = 270
	bottomDefault  = 220
)

func (a *App) State() State {
	return State{Project: a.project, ShowSidebar: a.showSidebar, SidebarW: a.sidebarW, ShowBottom: a.showBottom, BottomH: a.bottomH}
}

func (a *App) View(c *ui.Context) {
	// The active file's text goes to the server as it changes, a frame
	// after.
	a.javaFrame()
	a.gitFrame()
	a.followActiveTab()
	a.saveSession()
	a.debugFrame()
	// While the settings page waits for a key, none is a shortcut.
	if !a.Recording() {
		c.OnShortcut(ui.Cmd, ui.KeyB, func() { a.showSidebar = !a.showSidebar })
		c.OnShortcut(ui.Cmd, ui.KeyJ, func() { a.showBottom = !a.showBottom })
		c.OnShortcut(ui.Cmd, ui.KeyO, a.OpenFolder)
		c.OnShortcut(ui.Cmd, ui.KeyComma, a.openSettings)
		// On macOS the File menu takes these first, and does the same.
		c.OnShortcut(ui.Cmd, ui.KeyS, a.SaveActive)
		a.keyShortcuts(c)
	}
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
	a.recentPaletteView(c, k)
	a.filePaletteView(c, k)
	a.findInFilesView(c, k)
	a.branchPickerView(c, k)
	a.runConfigsDialog(c, k)
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
func (a *App) titleBar(c *ui.Context, k *colors) {
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
		label := "Open Folder…"
		if a.root != nil {
			label = a.root.name
		}
		if chip(c, k, "folder-open", label).Tooltip(shortcutTip("Recent Projects", a.keys.Binding("recentProjects"))).Clicked() {
			a.OpenRecentPalette()
		}
		if a.root != nil {
			if chip(c, k, "git-branch", a.branch).Tooltip("Branches").Clicked() {
				a.clickBranch()
			}
		}
		ui.Spacer(c)
		a.runConfigSelect(c, k)
		ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
			if iconButton(c, k, "play", "Run", 30, 16).Clicked() {
				a.runCurrent()
			}
			if iconButton(c, k, "x", "Stop", 30, 14).Clicked() {
				a.stopRun()
			}
			if iconButton(c, k, "refresh-cw", "Rerun", 30, 16).Clicked() {
				a.rerun()
			}
			if iconButton(c, k, "bug", "Debug", 30, 17).Clicked() {
				a.startDebug()
			}
			sync := iconButton(c, k, "refresh-cw", "Sync Maven/Gradle", 30, 16)
			if a.java.buildChanged {
				sync.Background(k.selected)
			}
			if sync.Clicked() {
				a.syncBuild()
			}
			if iconButton(c, k, "panel-bottom", "Toggle Panel  ⌘J · Ctrl+`", 30, 17).Clicked() {
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
func (a *App) runConfigSelect(c *ui.Context, k *colors) {
	prev := a.runConfig
	sel := ui.SelectBase(c, &a.runConfig)
	tr := sel.Trigger.Row().Height(28).MaxWidth(240).Padding(0, 8, 0, 12).Gap(7).AlignItems(ui.Center).Radius(14).Shrink(1).
		Background(k.track).Border(0.5, k.trackBorder).Cursor(ui.CursorPointer).Label("Run Configuration")
	if tr.Hovered() || sel.Open() {
		tr.Background(k.hover)
	}
	name := "Run Configuration"
	if cfg, ok := a.runStore.ByID(a.runConfig); ok {
		name = cfg.Name
	}
	tr.Children(func() {
		ui.Text(c, name).FontSize(13).FontWeight(600).TextColor(k.text).SingleLine().Ellipsis("…").Shrink(1).MinWidth(0)
		ui.Icon(c, icon("chevron-down")).Size(14, 14).TextColor(k.iconMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Margin(6, 0, 0, 0).Padding(4).MinWidth(240).Radius(12).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 8, 24, 0, k.shadowFocused)
		configs := a.runStore.All()
		if len(configs) == 0 {
			ui.Text(c, "No run configurations").FontSize(12).TextColor(k.textFaint).SingleLine().Padding(0, 10, 6, 10)
		}
		for _, cfg := range configs {
			item := sel.Item(cfg.ID).Row().Height(28).Padding(0, 10).Gap(8).AlignItems(ui.Center).Radius(8).Label(cfg.Name)
			if item.Highlighted() {
				item.Background(k.selected)
			}
			item.Children(func() {
				ui.Icon(c, icon("play")).Size(13, 13).TextColor(k.iconMuted)
				ui.Text(c, cfg.Name).FontSize(13).TextColor(k.text).SingleLine()
			})
		}
		// The dialog is where configurations are written, so its one way
		// in is here, among them, rather than a button of its own.
		edit := sel.Item(editConfigsItem).Row().Height(28).Padding(0, 10).Gap(8).AlignItems(ui.Center).Radius(8).
			Label("Edit Configurations…")
		if edit.Highlighted() {
			edit.Background(k.selected)
		}
		edit.Children(func() {
			ui.Icon(c, icon("settings-2")).Size(13, 13).TextColor(k.iconMuted)
			ui.Text(c, "Edit Configurations…").FontSize(13).TextColor(k.text).SingleLine()
		})
	})
	// The item is a choice the select writes like any other: it opens the
	// dialog and leaves the configuration the toolbar showed.
	if a.runConfig == editConfigsItem {
		a.runConfig = prev
		a.editConfigurations()
		return
	}
	if a.runConfig != prev {
		a.runStore.Select(a.runConfig)
		a.saveRunConfigs()
	}
}

// editConfigsItem is the dropdown's entry to the dialog, in the same value
// space as a configuration's id but never one.
const editConfigsItem = "megafactory:edit-configs"

// focusable makes a card take the user's attention when pressed.
func (a *App) focusable(c *ui.Context, k *colors, which int) ui.Element {
	e := card(c, k, a.focus == which)
	if e.Pressed() {
		a.focus = which
	}
	return e
}

func (a *App) sidebar(c *ui.Context, k *colors) ui.Element {
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
			if a.sidePane == 0 {
				a.treeOptions(c, k)
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			switch {
			case a.sidePane == 0 && a.root == nil:
				placeholder(c, k, "No folder open")
			case a.sidePane == 0:
				a.tree(c, k)
			default:
				a.changesPage(c, k)
			}
		})
	})
	return e
}

// treeOptions is the "…" of the tree's header, whose panel holds the
// options of how the tree shows. They are the settings page's too.
func (a *App) treeOptions(c *ui.Context, k *colors) {
	var picked string
	sel := ui.SelectBase(c, &picked)
	tr := sel.Trigger.Row().Size(26, 26).Center().Radius(10).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Tree Options").Tooltip("Tree Options")
	if tr.Hovered() || sel.Open() {
		tr.Background(k.hover)
	}
	tr.Children(func() {
		ui.Icon(c, icon("ellipsis")).Size(15, 15).TextColor(k.iconMuted)
	})
	sel.Popup(func(panel ui.Element) {
		panel.Margin(6, 0, 0, 0).Padding(4).MinWidth(220).Radius(12).Background(k.panel).Border(1, k.panelBorder).
			Shadow(0, 8, 24, 0, k.shadowFocused)
		item := sel.Item("compact").Row().Height(28).Padding(0, 10).Gap(8).AlignItems(ui.Center).Radius(8).
			Role(ui.RoleCheckBox).Label("Compact Middle Folders").Checked(prefs.CompactFolders)
		if item.Highlighted() {
			item.Background(k.selected)
		}
		item.Children(func() {
			check := ui.Box(c).Size(14, 14).Shrink(0).Center()
			if prefs.CompactFolders {
				check.Children(func() { ui.Icon(c, icon("check")).Size(14, 14).TextColor(k.text) })
			}
			ui.Text(c, "Compact Middle Folders").FontSize(13).TextColor(k.text).SingleLine()
		})
	})
	if picked == "compact" {
		prefs.CompactFolders = !prefs.CompactFolders
		saveSettings()
	}
}

func (a *App) tree(c *ui.Context, k *colors) {
	tree := ui.TreeBase(c)
	if a.revealing != "" {
		a.expandToReveal()
	}
	// focused is the row with the keyboard's focus, which the tree's
	// shortcuts act on.
	var focused *node
	var item func(n *node)
	item = func(n *node) {
		// A folder whose only child is a folder shows as one row, a/b/c,
		// that stands for the last of them.
		label := n.name
		if prefs.CompactFolders && n.dir && n != a.root {
			n, label = a.compactChain(n)
		}
		c.Key(n.path)
		var open *bool
		if n.dir {
			open = &n.open
		}
		it := tree.Item(open)
		row := it.Row.Height(26).Gap(6).Padding(0, 8).PaddingStart(8 + 10*float32(it.Depth())).Radius(8).AlignItems(ui.Center).Cursor(ui.CursorPointer)
		if a.revealing != "" && a.revealing == n.path {
			a.revealing = ""
			row.ScrollIntoView(ui.ScrollIntoViewOptions{Block: ui.ScrollCenter})
			// The focus too when asked to, so that the tree's keys act on it.
			if a.revealFocus {
				row.Focus()
			}
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
				ui.Text(c, label).FontSize(13).TextColor(textCol).SingleLine().Ellipsis("…")
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

// compactChain follows n down its only-folder children to the last, and
// names the way as the row shows it. A folder not read yet is read, shut
// as it is, for the row cannot tell whether it continues without.
func (a *App) compactChain(n *node) (*node, string) {
	label := n.name
	for {
		if !n.loaded {
			a.loadDir(n)
		}
		if len(n.children) != 1 || !n.children[0].dir {
			return n, label
		}
		n = n.children[0]
		label += "/" + n.name
	}
}

func (a *App) openFile(n *node) {
	a.focus = focusEditor
	if a.activate(n.path) {
		return
	}
	tree, file := a.root, filepath.Join(a.project, filepath.FromSlash(n.path))
	a.host.Run(func() func() {
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
			a.addFileTab(n.path, n.name, data)
		}
	})
}

// addFileTab opens a tab on a file's text, and makes it the active one.
func (a *App) addFileTab(rel, name string, data []byte) {
	buf := editor.NewBuffer(string(data))
	buf.SetLanguage(languageFor(name))
	// Diagnostics or inlays may build the editor before its first
	// frame, which takes the style then and not again.
	ed := &editor.State{Buffer: buf, WrapGuides: []int{120}, Font: code.editorFont(), LineHeight: prefs.EditorLineHeight}
	t := &tab{path: rel, name: name, ed: ed}
	a.tabs = append(a.tabs, t)
	a.active = len(a.tabs) - 1
	a.javaTabOpened(t)
	a.gitTabOpened(t)
}

func (a *App) openSettings() {
	a.focus = focusEditor
	if !a.activate(settingsPath) {
		a.tabs = append(a.tabs, &tab{path: settingsPath, name: "Settings"})
		a.active = len(a.tabs) - 1
	}
}

// activate makes the tab of path the active one, if it is open.
func (a *App) activate(path string) bool {
	i := slices.IndexFunc(a.tabs, func(t *tab) bool { return t.path == path })
	if i >= 0 {
		a.active = i
	}
	return i >= 0
}

func (a *App) closeTab(i int) {
	a.gitTabClosed(a.tabs[i])
	a.javaDidClose(a.tabs[i])
	a.tabs = slices.Delete(a.tabs, i, i+1)
	if a.active >= len(a.tabs) || a.active > i {
		a.active = max(0, a.active-1)
	}
}

func (a *App) editorCard(c *ui.Context, k *colors) ui.Element {
	e := a.focusable(c, k, focusEditor).Background(k.editor)
	e.Children(func() {
		if len(a.tabs) == 0 {
			ui.Column(c).Grow(1).Center().Gap(6).Children(func() {
				if a.root == nil {
					ui.Text(c, "No folder open").FontSize(16).FontWeight(600).TextColor(k.text)
					ui.Text(c, "Open a project folder · ⌘O").FontSize(12.5).TextColor(k.textMuted)
					if chip(c, k, "folder-open", "Open Folder…").Margin(10, 0, 0, 0).Clicked() {
						a.OpenFolder()
					}
					return
				}
				ui.Text(c, "No file open").FontSize(16).FontWeight(600).TextColor(k.text)
				ui.Text(c, "Open a file from the project tree · ⌘B sidebar · ⌘J or Ctrl+` panel").FontSize(12.5).TextColor(k.textMuted)
			})
			return
		}
		a.tabBar(c, k)
		// A tab's menu may have closed them all; the next pass shows so.
		if len(a.tabs) == 0 {
			return
		}
		cur := a.tabs[a.active]
		if cur.diff != nil {
			a.diffPage(c, k, cur)
			return
		}
		if cur.ed == nil {
			a.settingsPage(c, k)
			return
		}
		cur.ed.Theme = activeEditorTheme(c, k)
		cur.ed.Font, cur.ed.LineHeight = code.editorFont(), prefs.EditorLineHeight
		// The server answers a Java file's completions, hovers and
		// definitions; the editor asks through these.
		if isJavaPath(cur.path) {
			cur.ed.OnCompletion = func(r editor.CompletionRequest) { a.javaComplete(cur, r) }
			cur.ed.OnHover = func(r editor.HoverRequest) { a.javaHover(cur, r) }
			cur.ed.OnDefinition = func(offset int) { a.javaDefinition(cur, offset) }
		}
		a.wireGutter(c, cur.ed, cur)
		// Each file keeps its own editor widget state (scroll, focus).
		c.Key(cur.path)
		if prefs.GutterBlame && a.git.ok {
			// A Row centers its children across its axis unless told to
			// stretch them; without it the editor collapses to zero height.
			ui.Row(c).Grow(1).MinHeight(0).FillWidth().AlignItems(ui.Stretch).Children(func() {
				a.blameGutter(c, k, cur)
				editor.View(c, cur.ed).Grow(1).MinHeight(0).AutoFocus()
			})
		} else {
			editor.View(c, cur.ed).Grow(1).MinHeight(0).FillWidth().AutoFocus()
		}
		// A problem's or a definition's line waits for the editor the
		// frame builds, so that it scrolls and colors as the others.
		if a.pendingLine != nil && a.pendingLine.path == cur.path {
			at := *a.pendingLine
			a.pendingLine = nil
			if at.n > 0 {
				start := cur.ed.Buffer.Offset(editor.Pos{Line: at.line, Col: at.col})
				cur.ed.Select(start, start+at.n)
				cur.ed.ScrollTo(at.line)
			} else {
				// GoTo already scrolls the line to the middle; a ScrollTo
				// after it would pull the line up to the top.
				cur.ed.GoTo(editor.Pos{Line: at.line})
			}
		}
	})
	return e
}

func (a *App) tabBar(c *ui.Context, k *colors) {
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
						} else if tb.diff != nil {
							iconName = fileIcon(tb.diffRel)
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

func (a *App) bottomPanel(c *ui.Context, k *colors) ui.Element {
	type page struct{ icon, label string }
	pages := []page{
		{"play", "Run"},
		{"bug", "Debug"},
		{"terminal", "Terminal"},
		{"circle-alert", a.problemsLabel()},
		{"refresh-cw", "Build"},
	}
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
			if iconButton(c, k, "x", "Hide Panel  ⌘J · Ctrl+`", 26, 15).Clicked() {
				a.showBottom = false
			}
		})
		ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			switch a.bottomTab {
			case 0:
				a.runConsolePage(c, k)
			case 1:
				a.debugPage(c, k)
			case 2:
				a.terminalPage(c, k)
			case 3:
				a.problemsPage(c, k)
			case 4:
				a.buildPage(c, k)
			}
		})
	})
	return e
}

func (a *App) statusBar(c *ui.Context, k *colors) {
	ui.Row(c).Height(26).Gap(16).Padding(0, 16).AlignItems(ui.Center).Children(func() {
		faint := func(s string) { ui.Text(c, s).FontSize(11.5).TextColor(k.textFaint).SingleLine() }
		if a.root != nil {
			a.gitBranchChip(c, k)
			a.serversStatus(c, k)
			a.javaStatus(c, k)
			a.syncStatus(c, k)
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

// activeEditorTheme is the theme the editors take: the picked one, else the
// default's for the window's colors.
func activeEditorTheme(c *ui.Context, k *colors) *editor.Theme {
	_, th, _ := themes.pick(c.Theme().Dark)
	if th == nil {
		th = editor.DefaultTheme(c.Theme())
		th.Background = k.editor
	}
	return th
}
