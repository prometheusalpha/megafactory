package app

import (
	"megafactory/internal/keymap"

	"github.com/egoist/mygo/ui"
)

// keyActions is what each keymap.Action does. An action not here is bound
// but does nothing yet.
func (a *App) keyActions() map[string]func() {
	return map[string]func(){
		"closeTab":       a.CloseActive,
		"closeOtherTabs": a.CloseOtherTabs,
		"nextTab":        func() { a.stepTab(1) },
		"prevTab":        func() { a.stepTab(-1) },
		"findFile":       a.OpenFilePalette,
	}
}

// keyShortcuts declares the window's shortcut of each action as the keymap
// binds it now.
func (a *App) keyShortcuts(c *ui.Context) {
	do := a.keyActions()
	for _, act := range keymap.Actions {
		if b := a.keys.Binding(act.ID); !b.Zero() && do[act.ID] != nil {
			c.OnShortcut(b.Mods, b.Key, do[act.ID])
		}
	}
}

// Keymap is the shortcuts, for the window's menu bar to show.
func (a *App) Keymap() *keymap.Keymap { return a.keys }

// OnKeymapChange has fn run when the settings page rebinds an action.
func (a *App) OnKeymapChange(fn func()) { a.keysChanged = fn }

// rebind gives an action a new key, kept in settings.json. It reports the
// action that holds the key already, in which case nothing changes.
func (a *App) rebind(id string, b keymap.Binding) (conflict string) {
	if conflict = a.keys.Set(id, b); conflict == "" {
		a.keysSaved()
	}
	return conflict
}

func (a *App) resetBinding(id string) (conflict string) {
	if conflict = a.keys.Reset(id); conflict == "" {
		a.keysSaved()
	}
	return conflict
}

func (a *App) keysSaved() {
	prefs.Keymap = a.keys.Overrides()
	saveSettings()
	a.keysChanged()
}

// shortcutOf shows an action's key on a menu item, if it has one.
func (a *App) shortcutOf(it *ui.MenuItem, id string) *ui.MenuItem {
	if b := a.keys.Binding(id); !b.Zero() {
		return it.Shortcut(b.Mods, b.Key)
	}
	return it
}
