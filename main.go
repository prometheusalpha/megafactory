package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"time"

	"megafactory/internal/setup"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func main() {
	registerFonts()
	loadGhostty()
	prefs = loadSettings()
	themes = themeNamed(prefs.Theme)
	// Before Run, so the window opens in it.
	mygo.Theme.SetSource(mygo.ThemeSource(prefs.Appearance))
	st := loadState()
	a := newApp(st)
	a.recent = loadRecent()
	a.servers = servers{artifacts: setup.Artifacts, client: http.DefaultClient}
	// newApp starts none, as tests want: the app's starts jdtls.
	a.java.starter = startLanguageServer(a)
	mygo.App.WhenReady(func() {
		win := mygo.NewWindow(mygo.WindowOptions{
			Title:     "megafactory",
			Width:     1280,
			Height:    800,
			MinWidth:  800,
			MinHeight: 500,
			// Opens where the user left it last time.
			StateKey: "main",
			// The title bar is the app's own, beside the traffic lights.
			TitleBarStyle:        mygo.TitleBarHidden,
			TrafficLightPosition: &mygo.Point{X: 16, Y: 15},
			BackgroundColor:      "light-dark(#e3e7ef, #1e2229)",
			Content:              ui.View(a.view),
		})
		a.host = windowHost(win)
		mygo.App.SetMenu(appMenu(a, win))
		closing := false
		win.OnClose(func(e *mygo.CloseEvent) {
			if !closing && a.beforeCloseWindow(func() { closing = true; win.Close() }) {
				e.PreventDefault()
			}
		})
		win.OnClosed(func() { saveState(a.state()) })
		// A project moved or deleted since opens as none.
		if fi, err := os.Stat(st.Project); err == nil && fi.IsDir() {
			a.openProject(st.Project)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// appMenu is macOS's default menu bar, whose File menu closes the tab
// with ⌘W, as editors do, and the window with ⌘⇧W.
func appMenu(a *app, win *mygo.Window) *mygo.Menu {
	do := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { win.Update(fn) }
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "Open Folder…", Accelerator: "CmdOrCtrl+O", Click: do(a.openFolder)},
			{Label: "Recent Projects…", Accelerator: "CmdOrCtrl+Shift+O", Click: do(a.openRecentPalette)},
			mygo.Separator(),
			{Label: "Save", Accelerator: "CmdOrCtrl+S", Click: do(a.saveActive)},
			mygo.Separator(),
			{Label: "Close Tab", Accelerator: "CmdOrCtrl+W", Click: do(a.closeActive)},
			{Label: "Close Window", Accelerator: "CmdOrCtrl+Shift+W", Click: do(win.Close)},
		}},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleViewMenu},
		{Role: mygo.RoleWindowMenu},
	})
}

func windowHost(win *mygo.Window) host {
	return host{
		run: func(job func() func()) {
			go func() { win.Update(job()) }()
		},
		update: win.Update,
		after: func(d time.Duration, fn func()) {
			time.AfterFunc(d, func() { win.Update(fn) })
		},
		pickFolder: func(done func(string)) {
			// Dialog.Open waits for the user, so not on the UI thread.
			go func() {
				paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: win, Directory: true, CreateDirectories: true, ButtonLabel: "Open"})
				if err != nil {
					log.Printf("open folder: %v", err)
				}
				if len(paths) > 0 {
					win.Update(func() { done(paths[0]) })
				}
			}()
		},
		setAppearance: func(source string) { mygo.Theme.SetSource(mygo.ThemeSource(source)) },
		trash:         mygo.Shell.TrashItem,
		reveal:        mygo.Shell.ShowItemInFolder,
		ask: func(q question, done func(int)) {
			go func() {
				opts := mygo.MessageOptions{
					Parent: win, Type: mygo.MessageWarning, Message: q.message, Detail: q.detail,
					Buttons: q.buttons, DefaultButton: 0, CancelButton: len(q.buttons) - 1,
				}
				if q.destructive >= 0 {
					opts.DestructiveButtons = []int{q.destructive}
				}
				r, err := mygo.Dialog.Message(opts)
				if err == nil && r.Button >= 0 {
					win.Update(func() { done(r.Button) })
				}
			}()
		},
		closeWindow: win.Close,
		showError: func(title, message string) {
			go mygo.Dialog.Message(mygo.MessageOptions{Parent: win, Type: mygo.MessageError, Message: title, Detail: message})
		},
		watch: func(root string, changed func([]string)) func() {
			ctx, stop := context.WithCancel(context.Background())
			go watchProject(ctx, win, root, changed)
			return stop
		},
	}
}

// watchProject reports the folders where files changed until ctx ends.
// Hints are enough: the tree reads a changed folder again whole. .git is
// left out, as git writes there on every command.
func watchProject(ctx context.Context, win *mygo.Window, root string, changed func([]string)) {
	w, err := mygo.WatchFiles(ctx, root, mygo.FileWatchOptions{
		Recursive: true,
		HintsOnly: true,
		Ignore:    func(p string, isDir bool) bool { return isDir && path.Base(p) == ".git" },
	})
	if err != nil {
		log.Printf("watch %s: %v", root, err)
		return
	}
	defer w.Close()
	for {
		events, err := w.Next(ctx)
		if err != nil {
			if ctx.Err() == nil && !errors.Is(err, io.EOF) {
				log.Printf("watch %s: %v", root, err)
			}
			return
		}
		dirs := make([]string, len(events))
		for i, e := range events {
			dirs[i] = e.Path
		}
		win.Update(func() { changed(dirs) })
	}
}
