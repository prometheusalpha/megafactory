// Package keymap names the app's shortcuts as actions, each with a default
// binding that settings.json may replace.
package keymap

import (
	"runtime"
	"strings"

	"github.com/egoist/mygo/ui"
)

// Binding is a key with the modifiers held down.
type Binding struct {
	Mods ui.Modifiers
	Key  ui.Key
}

// Zero reports whether b binds nothing.
func (b Binding) Zero() bool { return b.Key == ui.KeyUnknown }

// Action is something a shortcut does. Its ID is what settings.json keeps
// a binding under, so it stays as it is even when Name is reworded.
type Action struct {
	ID      string
	Name    string
	Default Binding
}

// Actions are the shortcuts that can be rebound, in the order the settings
// page lists them.
var Actions = []Action{
	{"closeTab", "Close Tab", Binding{ui.Cmd, ui.KeyW}},
	{"closeOtherTabs", "Close Other Tabs", Binding{ui.Cmd | ui.Shift, ui.KeyW}},
	{"nextTab", "Next Tab", Binding{ui.Ctrl, ui.KeyTab}},
	{"prevTab", "Previous Tab", Binding{ui.Ctrl | ui.Shift, ui.KeyTab}},
	{"findFile", "Go to File", Binding{ui.Cmd, ui.KeyP}},
	{"findInFiles", "Find in Files", Binding{ui.Cmd | ui.Shift, ui.KeyF}},
	{"recentProjects", "Recent Projects", Binding{ui.Cmd | ui.Alt, ui.KeyR}},
	{"togglePanel", "Toggle Bottom Panel", Binding{ui.Ctrl, ui.KeyBackquote}},
	{"goToImplementation", "Go to Implementation", Binding{ui.Cmd | ui.Shift, ui.KeyI}},
	{"run", "Run / Rerun", Binding{ui.Ctrl, ui.KeyR}},
	{"debug", "Debug", Binding{ui.Ctrl, ui.KeyD}},
	{"stop", "Stop", Binding{ui.Ctrl, ui.KeyS}},
}

// Fixed are the shortcuts the app keeps as they are, which no action may
// take: they are not in Actions to rebind, but they are as much in use.
var Fixed = []struct {
	Name string
	Binding
}{
	{"Save", Binding{ui.Cmd, ui.KeyS}},
	{"Open Folder", Binding{ui.Cmd, ui.KeyO}},
	{"Toggle Sidebar", Binding{ui.Cmd, ui.KeyB}},
	{"Toggle Panel", Binding{ui.Cmd, ui.KeyJ}},
	{"Settings", Binding{ui.Cmd, ui.KeyComma}},
	{"Cut", Binding{ui.Cmd, ui.KeyX}},
	{"Copy", Binding{ui.Cmd, ui.KeyC}},
	{"Paste", Binding{ui.Cmd, ui.KeyV}},
	{"Select All", Binding{ui.Cmd, ui.KeyA}},
	{"Undo", Binding{ui.Cmd, ui.KeyZ}},
	{"Redo", Binding{ui.Cmd | ui.Shift, ui.KeyZ}},
}

// Usable reports whether b may be bound: a key that types text, as a
// letter alone or with Shift, would take it from the editor.
func Usable(b Binding) bool {
	if b.Zero() {
		return false
	}
	return b.Mods&^ui.Shift != 0 || (b.Key >= ui.KeyF1 && b.Key <= ui.KeyF12)
}

// NameOf is what an action, or a fixed shortcut, is called, given the ID
// Conflict returned.
func NameOf(id string) string {
	for _, a := range Actions {
		if a.ID == id {
			return a.Name
		}
	}
	return strings.TrimPrefix(id, fixedPrefix)
}

const fixedPrefix = "fixed:"

// Keymap is the binding of each action. Overrides holds only the ones the
// user changed, so a default a later version moves moves for everyone who
// kept it; an empty string there is an action left unbound.
type Keymap struct {
	overrides map[string]string
}

// New reads the overrides of settings.json. An unknown ID or a binding
// that does not parse is ignored, as is a binding another action holds
// already, so the keymap never has two actions on one key.
func New(overrides map[string]string) *Keymap {
	k := &Keymap{overrides: map[string]string{}}
	for _, a := range Actions {
		s, ok := overrides[a.ID]
		if !ok {
			continue
		}
		b, ok := Parse(s)
		if s != "" && !ok {
			continue
		}
		if !b.Zero() && k.Conflict(a.ID, b) != "" {
			continue
		}
		k.overrides[a.ID] = s
	}
	return k
}

// Overrides is what settings.json keeps.
func (k *Keymap) Overrides() map[string]string {
	if len(k.overrides) == 0 {
		return nil
	}
	out := make(map[string]string, len(k.overrides))
	for id, s := range k.overrides {
		out[id] = s
	}
	return out
}

// Binding is what id is bound to now; the zero Binding if nothing.
func (k *Keymap) Binding(id string) Binding {
	if s, ok := k.overrides[id]; ok {
		b, _ := Parse(s)
		return b
	}
	for _, a := range Actions {
		if a.ID == id {
			return a.Default
		}
	}
	return Binding{}
}

// Conflict is the ID of the other action that holds b, or "". A fixed
// shortcut has an ID NameOf reads too.
func (k *Keymap) Conflict(id string, b Binding) string {
	for _, f := range Fixed {
		if f.Binding == b {
			return fixedPrefix + f.Name
		}
	}
	for _, a := range Actions {
		if a.ID != id && k.Binding(a.ID) == b {
			return a.ID
		}
	}
	return ""
}

// Set rebinds id, and reports the ID of the action that holds b already,
// in which case nothing changes. The zero Binding unbinds id.
func (k *Keymap) Set(id string, b Binding) (conflict string) {
	if !b.Zero() {
		if c := k.Conflict(id, b); c != "" {
			return c
		}
	}
	for _, a := range Actions {
		if a.ID != id {
			continue
		}
		if b == a.Default {
			delete(k.overrides, id)
		} else {
			k.overrides[id] = b.String()
		}
	}
	return ""
}

// Reset gives id its default back, unless another action took it, in which
// case that one's ID is reported and nothing changes.
func (k *Keymap) Reset(id string) (conflict string) {
	for _, a := range Actions {
		if a.ID == id {
			return k.Set(id, a.Default)
		}
	}
	return ""
}

// Label is how b reads in a menu or on the settings page: ⌃⇧Tab on macOS,
// Ctrl+Shift+Tab elsewhere.
func (b Binding) Label() string {
	if b.Zero() {
		return ""
	}
	name := keyNames[b.Key]
	if runtime.GOOS != "darwin" {
		var parts []string
		for _, m := range modOrder {
			if b.Mods&m.mod != 0 {
				parts = append(parts, m.word)
			}
		}
		return strings.Join(append(parts, name), "+")
	}
	var sb strings.Builder
	for _, m := range modOrder {
		if b.Mods&m.mod != 0 {
			sb.WriteString(m.glyph)
		}
	}
	return sb.String() + name
}

// String is b as settings.json keeps it, "cmd+shift+w": what Parse reads,
// the same on every platform. "cmd" is ui.Cmd, so a binding kept on a Mac
// is Control elsewhere.
func (b Binding) String() string {
	if b.Zero() {
		return ""
	}
	var parts []string
	for _, m := range modOrder {
		if b.Mods&m.mod != 0 {
			parts = append(parts, m.word2)
		}
	}
	return strings.Join(append(parts, strings.ToLower(keyNames[b.Key])), "+")
}

var modOrder = []struct {
	mod   ui.Modifiers
	word  string // as Label spells it off macOS
	word2 string // as String spells it
	glyph string
}{
	{ui.Ctrl, "Ctrl", "ctrl", "⌃"},
	{ui.Alt, "Alt", "alt", "⌥"},
	{ui.Shift, "Shift", "shift", "⇧"},
	{ui.Super, "Win", "super", "⌘"},
}

// Parse reads what String writes. "cmd" is the platform's ui.Cmd.
func Parse(s string) (Binding, bool) {
	var b Binding
	parts := strings.Split(strings.ToLower(strings.TrimSpace(s)), "+")
	for i, p := range parts {
		if i == len(parts)-1 {
			key, ok := keysByName[p]
			if !ok {
				return Binding{}, false
			}
			b.Key = key
			return b, true
		}
		switch p {
		case "cmd":
			b.Mods |= ui.Cmd
		case "ctrl":
			b.Mods |= ui.Ctrl
		case "alt":
			b.Mods |= ui.Alt
		case "shift":
			b.Mods |= ui.Shift
		case "super":
			b.Mods |= ui.Super
		default:
			return Binding{}, false
		}
	}
	return Binding{}, false
}

// Accelerator is b as a native menu item spells it, "CmdOrCtrl+Shift+W".
func (b Binding) Accelerator() string {
	if b.Zero() {
		return ""
	}
	var parts []string
	mods := b.Mods
	if mods&ui.Cmd != 0 {
		parts = append(parts, "CmdOrCtrl")
		mods &^= ui.Cmd
	}
	for _, m := range modOrder {
		if mods&m.mod != 0 {
			parts = append(parts, strings.ToUpper(m.word2[:1])+m.word2[1:])
		}
	}
	return strings.Join(append(parts, keyNames[b.Key]), "+")
}
