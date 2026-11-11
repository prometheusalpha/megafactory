package app

import (
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
)

// The delay the settings page accepts, in seconds.
const autoSaveMin, autoSaveMax = 0.1, 600

// autoSaveFrame schedules the save of each tab whose text changed since
// the last frame. The save waits out the delay, and goes only if no later
// edit moved the text on, so it runs once typing stops.
func (a *App) autoSaveFrame() {
	if !prefs.AutoSave {
		return
	}
	for _, t := range a.tabs {
		if t.ed == nil || !t.ed.Buffer.Modified() {
			continue
		}
		v := t.ed.Buffer.Version()
		if v == t.autoVer {
			continue
		}
		t.autoVer = v
		delay := time.Duration(prefs.AutoSaveDelay * float32(time.Second))
		a.host.After(delay, func() { a.autoSaveDue(t, v) })
	}
}

func (a *App) autoSaveDue(t *tab, version uint64) {
	// The tab may be closed by now, or the setting turned off.
	if !prefs.AutoSave || !a.hasTab(t) || t.ed == nil || !t.ed.Buffer.Modified() || t.ed.Buffer.Version() != version {
		return
	}
	a.save(t, nil)
}

func (a *App) hasTab(t *tab) bool {
	for _, o := range a.tabs {
		if o == t {
			return true
		}
	}
	return false
}

// autoSavePicker is the switch, and the pause before a save as a number
// of seconds to type, shown while the switch is on.
func (a *App) autoSavePicker(c *ui.Context, k *colors) {
	ui.Column(c).Gap(10).Children(func() {
		if ui.Checkbox(c, &prefs.AutoSave, "Save files automatically").Changed() {
			saveSettings()
		}
		if !prefs.AutoSave {
			return
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "Wait").FontSize(13).TextColor(k.text)
			in := ui.Row(c).Width(72).Height(28).Padding(0, 10).Radius(8).AlignItems(ui.Center).
				Background(k.track).Border(0.5, k.trackBorder)
			var field ui.Element
			in.Children(func() {
				field = ui.TextInputBase(c, &a.autoSaveText).Label("Auto save delay in seconds").
					FontSize(12.5).TextColor(k.text).Grow(1)
			})
			ui.Text(c, "seconds after the last edit").FontSize(13).TextColor(k.textMuted)
			switch {
			case field.Changed():
				// Half-typed or out of range text keeps the last delay.
				if d, err := strconv.ParseFloat(strings.TrimSpace(a.autoSaveText), 32); err == nil && d >= autoSaveMin && d <= autoSaveMax {
					prefs.AutoSaveDelay = float32(d)
					saveSettings()
				}
			case !field.Focused():
				a.autoSaveText = strconv.FormatFloat(float64(prefs.AutoSaveDelay), 'f', -1, 32)
			}
		})
	})
}
