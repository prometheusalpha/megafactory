package app

import (
	"megafactory/internal/keymap"

	"github.com/egoist/mygo/ui"
)

// recording is the action whose key the settings page waits to be pressed,
// with what to tell about the last one pressed, if it was refused.
type recording struct {
	id, problem string
}

// Recording reports whether the settings page waits for a key. The window
// keeps its own shortcuts, and the menu bar its keys, out of the way until
// it has one: they would take it first.
func (a *App) Recording() bool { return a.recording != nil }

func (a *App) startRecording(id string) {
	a.recording = &recording{id: id}
	a.keysChanged()
}

func (a *App) stopRecording() {
	a.recording = nil
	a.keysChanged()
}

// keymapSection lists the actions with the key of each, to rebind by
// clicking it and pressing the new one.
func (a *App) keymapSection(c *ui.Context, k *colors) {
	section(c, k, "Keyboard shortcuts", "Click a shortcut, then press the keys you want. Esc cancels.", func() {
		ui.Column(c).Gap(2).Children(func() {
			for _, act := range keymap.Actions {
				a.keymapRow(c, k, act)
			}
		})
	})
}

func (a *App) keymapRow(c *ui.Context, k *colors, act keymap.Action) {
	c.Key(act.ID)
	rec := a.recording != nil && a.recording.id == act.ID
	b := a.keys.Binding(act.ID)
	ui.Row(c).Height(40).Padding(0, 10).Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Text(c, act.Name).FontSize(13).TextColor(k.text).Grow(1).SingleLine()
		if rec && a.recording.problem != "" {
			ui.Text(c, a.recording.problem).FontSize(12).TextColor(k.textMuted).SingleLine()
		}
		a.keyCap(c, k, act, b, rec)
		if b != act.Default {
			if chip(c, k, "refresh-cw", "Reset").Clicked() {
				a.resetOrReport(act.ID)
			}
		}
	})
}

// keyCap is an action's key as a button that, clicked, takes the next key
// pressed as the action's new one.
func (a *App) keyCap(c *ui.Context, k *colors, act keymap.Action, b keymap.Binding, rec bool) {
	label := b.Label()
	switch {
	case rec:
		label = "Press keys…"
	case b.Zero():
		label = "Not set"
	}
	e := ui.Row(c).Height(28).MinWidth(96).Padding(0, 12).Radius(8).Center().Shrink(0).Cursor(ui.CursorPointer).
		Role(ui.RoleButton).Label(act.Name+" shortcut").Focusable().Background(k.track).Border(0.5, k.trackBorder)
	switch {
	case rec:
		e.Background(k.selected).AutoFocus()
	case e.Hovered():
		e.Background(k.hover)
	}
	e.Children(func() {
		ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine()
	})
	if e.Clicked() {
		if rec {
			a.stopRecording()
		} else {
			a.startRecording(act.ID)
		}
	}
	if rec {
		e.HandleInput(func(ev ui.InputEvent) bool { return a.recordKey(ev) })
	}
}

// recordKey takes the keys pressed while an action waits for one.
func (a *App) recordKey(ev ui.InputEvent) bool {
	if a.recording == nil || ev.Kind != ui.InputKeyDown {
		return false
	}
	key := ev.Key
	if key == ui.KeyUnknown {
		key = ev.PhysicalKey
	}
	switch {
	case key == ui.KeyEscape && ev.Mods == 0:
		a.stopRecording()
	case key == ui.KeyUnknown:
		// A modifier on its own: the rest of the keys are to come.
	case !keymap.Usable(keymap.Binding{Mods: ev.Mods, Key: key}):
		a.recording.problem = "Hold ⌘, ⌃ or ⌥ too"
	default:
		b := keymap.Binding{Mods: ev.Mods, Key: key}
		if c := a.rebind(a.recording.id, b); c != "" {
			a.recording.problem = "Used by " + keymap.NameOf(c)
		} else {
			a.stopRecording()
		}
	}
	return true
}

func (a *App) resetOrReport(id string) {
	if c := a.resetBinding(id); c != "" {
		a.recording = &recording{id: id, problem: "Used by " + keymap.NameOf(c)}
	}
}
