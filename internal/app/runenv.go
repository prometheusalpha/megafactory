package app

import (
	"strconv"

	"megafactory/internal/runcfg"

	"github.com/egoist/mygo/ui"
)

const envNameW = 190

// runEnvField is the Environment row of the form: one input that takes a
// pasted A=1;B=2, and a button for the table. The input's text is derived
// from the draft too, and only re-derived when the draft changed elsewhere,
// so typing in it is not reformatted under the cursor.
func (a *App) runEnvField(c *ui.Context, k *colors, e *runEditState) {
	if e.envTextSrc != e.draft.Env {
		e.envText, e.envTextSrc = runcfg.EnvSemicolons(runcfg.ParseEnv(e.draft.Env)), e.draft.Env
	}
	ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		in := ui.TextInputBase(c, &e.envText).Label("Environment").Placeholder("Paste A=1;B=2").
			FontSize(12.5).TextColor(k.text).Grow(1).Height(26).Padding(0, 8)
		fieldLook(k, in)
		if in.Changed() {
			e.draft.Env = runcfg.JoinEnv(runcfg.ParseEnvText(e.envText))
			e.envTextSrc = e.draft.Env
		}
		if textButton(c, k, "Edit…").Clicked() {
			e.envOpen = true
		}
	})
}

// runEnvDialog is the table in a modal over the Run Configurations dialog.
func (a *App) runEnvDialog(c *ui.Context, k *colors, e *runEditState) {
	if !e.envOpen {
		return
	}
	ui.DialogBase(c.Key("run-env"), &e.envOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		panel.Width(620).Height(440).Clip().Padding(12).Gap(10).Radius(14).
			Background(k.dialog).Border(1, k.panelBorder).Shadow(0, 12, 40, 0, k.shadowFocused).
			Label("Environment variables")
		panel.Children(func() {
			ui.Row(c).Height(30).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Environment variables").FontSize(14).FontWeight(600).TextColor(k.text)
				ui.Spacer(c)
				if iconButton(c, k, "x", "Close", 26, 15).Clicked() {
					e.envOpen = false
				}
			})
			ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
				a.runEnvTable(c, k, e, &e.draft.Env)
			})
			ui.Row(c).Height(30).Shrink(0).AlignItems(ui.Center).Children(func() {
				ui.Spacer(c)
				if textButton(c, k, "Done").Clicked() {
					e.envOpen = false
				}
			})
		})
	})
}

// runEnvTable is the Environment as a name/value table. The rows are
// derived from the draft's Env text and written back to it on every edit, so
// the draft stays the one source: switching config or Cancel just changes
// the text, and the rows follow.
func (a *App) runEnvTable(c *ui.Context, k *colors, e *runEditState, text *string) {
	if e.envSrc != *text {
		e.envRows, e.envSrc = runcfg.ParseEnv(*text), *text
	}
	drop := -1
	// One bordered surface: a header, hairline-separated rows whose inputs
	// have no chrome of their own, and a toolbar. Editing happens in place.
	ui.Column(c).FillWidth().Radius(8).Clip().Border(0.5, k.panelBorder).Children(func() {
		ui.Row(c).FillWidth().Height(24).Padding(0, 8).AlignItems(ui.Center).Background(k.hover).Children(func() {
			ui.Text(c, "NAME").FontSize(10.5).FontWeight(600).TextColor(k.textFaint).Width(envNameW + 9)
			ui.Text(c, "VALUE").FontSize(10.5).FontWeight(600).TextColor(k.textFaint)
		})
		if len(e.envRows) == 0 {
			ui.Row(c).FillWidth().Height(34).Padding(0, 10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "No variables. Add one, or paste A=1;B=2.").FontSize(12).TextColor(k.textFaint)
			})
		}
		for i := range e.envRows {
			c.Key(strconv.Itoa(i))
			ui.Box(c).FillWidth().Height(1).Background(k.panelBorder)
			row := ui.Row(c).FillWidth().Height(30).Padding(0, 4, 0, 8).AlignItems(ui.Center)
			if row.Hovered() {
				row.Background(k.hover)
			}
			row.Children(func() {
				ui.TextInputBase(c, &e.envRows[i].Name).Label("Variable name").Placeholder("NAME").
					FontSize(12.5).TextColor(k.text).Width(envNameW).Height(26)
				ui.Box(c).Width(1).Height(16).Background(k.panelBorder).Margin(0, 8, 0, 0)
				ui.TextInputBase(c, &e.envRows[i].Value).Label("Variable value").Placeholder("value").
					FontSize(12.5).TextColor(k.text).Grow(1).Height(26)
				if iconButton(c, k, "x", "Remove variable", 22, 12).Clicked() {
					drop = i
				}
			})
		}
		ui.Box(c).FillWidth().Height(1).Background(k.panelBorder)
		ui.Row(c).FillWidth().Height(32).Padding(0, 4).Gap(2).AlignItems(ui.Center).Children(func() {
			if textButton(c, k, "+ Add variable").Clicked() {
				e.envRows = append(e.envRows, runcfg.EnvVar{})
			}
			ui.Spacer(c)
			if textButton(c, k, "Paste").Clicked() {
				e.envRows = runcfg.MergeEnv(e.envRows, runcfg.ParseEnvText(c.ReadClipboard()))
			}
			if textButton(c, k, "Copy as A=1;B=2").Clicked() {
				c.WriteClipboard(runcfg.EnvSemicolons(e.envRows))
			}
		})
	})
	if drop >= 0 {
		e.envRows = append(e.envRows[:drop], e.envRows[drop+1:]...)
	}
	*text = runcfg.JoinEnv(e.envRows)
	e.envSrc = *text
}
