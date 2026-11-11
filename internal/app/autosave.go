package app

import (
	"strconv"
	"time"

	"github.com/egoist/mygo/ui"
)

// autoSaveDelays are the pauses the settings page offers, in seconds.
var autoSaveDelays = []float32{0.5, 1, 2, 5}

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

// autoSavePicker is the switch, and the pause before a save as a segmented
// control, as the appearance's, shown while the switch is on.
func (a *App) autoSavePicker(c *ui.Context, k *colors) {
	ui.Column(c).Gap(10).Children(func() {
		if ui.Checkbox(c, &prefs.AutoSave, "Save files automatically").Changed() {
			saveSettings()
		}
		if !prefs.AutoSave {
			return
		}
		track(k, ui.Row(c)).AlignSelf(ui.Start).Role(ui.RoleRadioGroup).Label("Auto save delay").Children(func() {
			for _, d := range autoSaveDelays {
				label := strconv.FormatFloat(float64(d), 'f', -1, 32) + " s"
				c.Key(label)
				on := prefs.AutoSaveDelay == d
				b := trackTab(k, ui.Row(c), on).Padding(0, 14).Cursor(ui.CursorPointer).Role(ui.RoleRadio).Label(label).Checked(on)
				b.Children(func() {
					ui.Text(c, label).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine()
				})
				b.OnClick(func() {
					prefs.AutoSaveDelay = d
					saveSettings()
				})
			}
		})
	})
}
