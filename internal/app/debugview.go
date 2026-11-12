package app

import (
	"github.com/egoist/mygo/ui"
)

// debugPage is the bottom panel's Debug page: the toolbar that drives the
// session, where it stopped, and tabs of what it printed and what it holds.
func (a *App) debugPage(c *ui.Context, k *colors) ui.Element {
	d := &a.debug
	e := ui.Column(c).Grow(1).MinHeight(0).FillWidth()
	e.Children(func() {
		a.debugToolbar(c, k, d)
		a.debugTabs(c, k, d)
	})
	return e
}

// debugTabs are the Output and Watch tabs, the output with the run
// console's tools while it shows.
func (a *App) debugTabs(c *ui.Context, k *colors, d *debugService) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).FillWidth().Children(func() {
		ui.Row(c).Height(30).Padding(0, 8, 0, 6).Gap(8).AlignItems(ui.Center).Children(func() {
			tabs := ui.TabsBase(c, &d.tab, 2)
			track(k, tabs.List).Children(func() {
				for i, label := range []string{"Output", "Watch"} {
					trackTab(k, tabs.Tab(i), i == d.tab).Padding(0, 10).Children(func() {
						ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(k.text).SingleLine()
					})
				}
			})
			ui.Spacer(c)
			if d.tab == 0 && d.out != nil {
				a.consoleTools(c, k, d.out)
			}
		})
		switch d.tab {
		case 0:
			if d.out == nil {
				placeholder(c, k, "Run a configuration under the debugger to see its output here.")
				return
			}
			a.consoleEditor(c, k, d.out)
		case 1:
			a.debugWatchPage(c, k, d)
		}
	})
}

// debugToolbar is the page's row of session commands, with the phase and
// the session's last error at its right.
func (a *App) debugToolbar(c *ui.Context, k *colors, d *debugService) {
	ui.Row(c).Height(30).Padding(0, 8, 0, 6).Gap(6).AlignItems(ui.Center).Children(func() {
		stopped := d.stopped()
		if ui.Button(c, "Continue").Disabled(!stopped).Clicked() {
			a.debugContinue()
		}
		if ui.Button(c, "Step Over").Disabled(!stopped).Clicked() {
			a.debugStepOver()
		}
		if ui.Button(c, "Step Into").Disabled(!stopped).Clicked() {
			a.debugStepInto()
		}
		if ui.Button(c, "Step Out").Disabled(!stopped).Clicked() {
			a.debugStepOut()
		}
		if ui.Button(c, "Pause").Disabled(d.phase != debugRunning || d.conn == nil).Clicked() {
			a.debugPause()
		}
		// Not "Stop", which is the running process's button in the title
		// bar, and would be the same name for two different things.
		canStop := d.conn != nil || d.phase == debugStopped || d.phase == debugFailed || d.phase == debugEnded
		if ui.Button(c, "Stop Debugging").Disabled(!canStop).Clicked() {
			a.debugStop()
		}
		if ui.Button(c, "Restart").Disabled(d.cfg == "").Clicked() {
			a.debugRestart()
		}
		ui.Spacer(c)
		ui.Text(c, debugStatus(d)).FontSize(12).TextColor(k.textMuted).SingleLine()
		if msg := d.debugErr(); msg != "" {
			ui.Text(c, msg).FontSize(12).TextColor(c.Theme().Danger).SingleLine().Ellipsis("…")
		}
	})
}

// debugWatchPage is the watches, what was evaluated, and the selected
// frame's scopes and their variables, which open in place as the user asks
// for their children. Its input adds a watch or evaluates once.
func (a *App) debugWatchPage(c *ui.Context, k *colors, d *debugService) {
	ui.Row(c).Height(32).Padding(0, 8, 0, 6).Gap(6).AlignItems(ui.Center).Children(func() {
		in := ui.TextInputBase(c, &d.input).Label("Expression").FontSize(12).TextColor(k.text).FillWidth()
		if in.Submitted() {
			a.debugAddWatch()
		}
		if ui.Button(c, "Evaluate").Clicked() {
			a.debugRunConsole()
		}
		if ui.Button(c, "Add Watch").Clicked() {
			a.debugAddWatch()
		}
	})
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Padding(0, 4, 6, 4).Gap(1).FillWidth().Children(func() {
			for i, w := range d.watch {
				debugWatchRow(c, k, w, "Remove watch", func() {
					// The expression is the row's input, so that a
					// wrong one is corrected where it is written.
					expr := ui.TextInputBase(c, &d.watch[i].expr).Label(w.expr).FontSize(12).TextColor(k.text).Shrink(1).MinWidth(0)
					if expr.Submitted() {
						a.debugEvaluateAll()
					}
				}, func() { a.debugRemoveWatch(i) })
			}
			for i, e := range d.evals {
				debugWatchRow(c, k, e, "Remove evaluation", func() {
					ui.Text(c, e.expr).FontSize(12).TextColor(k.text).SingleLine()
				}, func() { a.debugRemoveEval(i) })
			}
			if len(d.scopes) == 0 {
				note := "No variables."
				if !d.stopped() {
					note = "The program is not stopped."
				}
				buildNote(c, note, k.textMuted)
				return
			}
			for _, s := range d.scopes {
				ui.Text(c, s.Name).FontSize(11.5).TextColor(k.textMuted).SingleLine().Padding(0, 6)
				a.debugVarRows(c, k, d, s.VariablesReference, 0)
			}
		})
	})
}

// debugWatchRow is a watch or an evaluation: its expression, drawn by expr,
// what it was or why not, and the button that removes it.
func debugWatchRow(c *ui.Context, k *colors, w debugWatch, remove string, expr func(), onRemove func()) {
	ui.Row(c).Height(24).Gap(6).Padding(0, 6).AlignItems(ui.Center).Children(func() {
		expr()
		ui.Text(c, "=").FontSize(12).TextColor(k.textFaint).SingleLine()
		if w.err != "" {
			ui.Text(c, w.err).FontSize(12).TextColor(c.Theme().Danger).SingleLine().Ellipsis("…")
		} else {
			ui.Text(c, w.value).FontSize(12).TextColor(k.textMuted).SingleLine().Ellipsis("…")
		}
		ui.Spacer(c)
		if iconButton(c, k, "x", remove, 20, 12).Clicked() {
			onRemove()
		}
	})
}

// debugVarRows is one reference's variables, each row one step deeper than
// its parent's, and a row's children under it while it is open.
func (a *App) debugVarRows(c *ui.Context, k *colors, d *debugService, ref, depth int) {
	for _, v := range d.vars[ref] {
		open := v.VariablesReference > 0 && d.expanded[v.VariablesReference]
		row := ui.Row(c).Height(22).Gap(6).AlignItems(ui.Center).Radius(6).
			Padding(0, 6).PaddingStart(6 + 12*float32(depth))
		if v.VariablesReference > 0 {
			row.Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(v.Name)
			if row.Hovered() {
				row.Background(k.hover)
			}
			row.OnClick(func() { a.debugExpand(v.VariablesReference) })
		}
		row.Children(func() {
			if v.VariablesReference > 0 {
				if open {
					ui.Icon(c, icon("chevron-right")).Size(12, 12).TextColor(k.iconMuted).Rotate(90)
				} else {
					ui.Icon(c, icon("chevron-right")).Size(12, 12).TextColor(k.iconMuted)
				}
			} else {
				ui.Box(c).Size(12, 12)
			}
			ui.Text(c, v.Name).FontSize(12).TextColor(k.text).SingleLine()
			ui.Text(c, v.Value).FontSize(12).TextColor(k.textMuted).SingleLine().Ellipsis("…")
			if v.Type != "" {
				ui.Text(c, v.Type).FontSize(11).TextColor(k.textFaint).SingleLine().Ellipsis("…")
			}
		})
		if open {
			a.debugVarRows(c, k, d, v.VariablesReference, depth+1)
		}
	}
}
