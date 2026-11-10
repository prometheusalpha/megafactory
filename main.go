// Command megafactory is the app's composition root: it reads the user's
// settings and window state, opens the window, and gives the app the
// window and the desktop it asks for. Everything the app does lives in
// internal/app, which runs without either, as the tests want.
package main

import (
	"context"
	"errors"
	"io"
	"log"
	"os"
	"path"
	"time"

	"megafactory/internal/app"
	"megafactory/internal/runner"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

func main() {
	app.RegisterFonts()
	app.LoadGhostty()
	prefs := app.LoadSettings()
	// Before Run, so the window opens in it.
	setAppearance(prefs.Appearance)
	st := app.LoadState()
	a := app.New(st, prefs)
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
			Content:              ui.View(a.View),
		})
		a.UseHost(windowHost(win))
		mygo.App.SetMenu(appMenu(a, win))
		a.OnKeymapChange(func() { mygo.App.SetMenu(appMenu(a, win)) })
		closing := false
		win.OnClose(func(e *mygo.CloseEvent) {
			if !closing && a.BeforeCloseWindow(func() { closing = true; win.Close() }) {
				e.PreventDefault()
			}
		})
		win.OnClosed(func() { app.SaveState(a.State()) })
		// A project moved or deleted since opens as none.
		if fi, err := os.Stat(st.Project); err == nil && fi.IsDir() {
			a.OpenProject(st.Project)
		}
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

func setAppearance(source string) { mygo.Theme.SetSource(mygo.ThemeSource(source)) }

// appMenu is macOS's default menu bar. Its File menu holds the keys of
// closing tabs, which it takes before the window's shortcuts do; they are
// the keymap's, so the menu is built again as one is rebound. Close Window
// has no key: ⌘⇧W closes the other tabs.
func appMenu(a *app.App, win *mygo.Window) *mygo.Menu {
	do := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) { win.Update(fn) }
	}
	// Its keys are held back while the settings page waits for a key, or
	// the menu would take the one pressed.
	key := func(accel string) string {
		if a.Recording() {
			return ""
		}
		return accel
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu},
		{Label: "File", Submenu: []*mygo.MenuItem{
			{Label: "Open Folder…", Accelerator: key("CmdOrCtrl+O"), Click: do(a.OpenFolder)},
			{Label: "Recent Projects…", Accelerator: key("CmdOrCtrl+Shift+O"), Click: do(a.OpenRecentPalette)},
			mygo.Separator(),
			{Label: "Save", Accelerator: key("CmdOrCtrl+S"), Click: do(a.SaveActive)},
			mygo.Separator(),
			{Label: "Close Tab", Accelerator: key(a.Keymap().Binding("closeTab").Accelerator()), Click: do(a.CloseActive)},
			{Label: "Close Other Tabs", Accelerator: key(a.Keymap().Binding("closeOtherTabs").Accelerator()), Click: do(a.CloseOtherTabs)},
			{Label: "Close Window", Click: do(win.Close)},
		}},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleViewMenu},
		{Role: mygo.RoleWindowMenu},
	})
}

// windowHost is the app's host as this window serves it.
func windowHost(win *mygo.Window) app.Host {
	return app.Host{
		Run: func(job func() func()) {
			go func() { win.Update(job()) }()
		},
		Update: win.Update,
		After: func(d time.Duration, fn func()) {
			time.AfterFunc(d, func() { win.Update(fn) })
		},
		PickFolder: func(done func(string)) {
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
		SetAppearance: setAppearance,
		Trash:         mygo.Shell.TrashItem,
		Reveal:        mygo.Shell.ShowItemInFolder,
		Ask: func(q app.Question, done func(int)) {
			go func() {
				opts := mygo.MessageOptions{
					Parent: win, Type: mygo.MessageWarning, Message: q.Message, Detail: q.Detail,
					Buttons: q.Buttons, DefaultButton: 0, CancelButton: len(q.Buttons) - 1,
				}
				if q.Destructive >= 0 {
					opts.DestructiveButtons = []int{q.Destructive}
				}
				r, err := mygo.Dialog.Message(opts)
				if err == nil && r.Button >= 0 {
					win.Update(func() { done(r.Button) })
				}
			}()
		},
		CloseWindow: win.Close,
		ShowError: func(title, message string) {
			go mygo.Dialog.Message(mygo.MessageOptions{Parent: win, Type: mygo.MessageError, Message: title, Detail: message})
		},
		Watch: func(root string, changed func([]string)) func() {
			ctx, stop := context.WithCancel(context.Background())
			go watchProject(ctx, win, root, changed)
			return stop
		},
		NewTerm: func(opts terminal.Options) (app.TermHandle, error) {
			t, err := terminal.New(opts)
			if err != nil {
				return nil, err
			}
			return app.LiveTerm{T: t}, nil
		},
		StartProc: runner.Start,
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
