package app

import (
	"path/filepath"
	"strconv"

	"github.com/egoist/mygo/ui"
)

// debugPage is the bottom panel's Debug page: the toolbar that drives the
// session, where it stopped, and what it printed.
func (a *App) debugPage(c *ui.Context, k *colors) ui.Element {
	d := &a.debug
	e := ui.Column(c).Grow(1).MinHeight(0).FillWidth()
	e.Children(func() {
		a.debugToolbar(c, k, d)
		ui.Row(c).Grow(1).MinHeight(0).FillWidth().Gap(10).Children(func() {
			a.debugStackPage(c, k, d)
			a.debugVarsPage(c, k, d)
			a.debugConsolePage(c, k, d)
		})
	})
	return e
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

// debugLabel is the title of one of the page's three columns.
func debugLabel(c *ui.Context, k *colors, s string) {
	ui.Text(c, s).FontSize(11.5).TextColor(k.textMuted).SingleLine().Padding(0, 6, 2, 6)
}

// debugStackPage is the stopped thread's frames, with the threads to
// switch between and the watches to read at the selected frame.
func (a *App) debugStackPage(c *ui.Context, k *colors, d *debugService) {
	ui.Column(c).Width(230).Shrink(0).MinHeight(0).Children(func() {
		debugLabel(c, k, "Call Stack")
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).Padding(0, 4, 6, 4).Gap(1).FillWidth().Children(func() {
				if len(d.threads) > 1 {
					for _, t := range d.threads {
						row := ui.Row(c).Height(22).Gap(6).Padding(0, 6).AlignItems(ui.Center).Radius(6).
							Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(t.Name)
						if t.ID == d.thread {
							row.Background(k.selected)
						} else if row.Hovered() {
							row.Background(k.hover)
						}
						id := t.ID
						row.OnClick(func() { a.debugSelectThread(id) })
						row.Children(func() {
							ui.Text(c, t.Name).FontSize(12).TextColor(k.text).SingleLine().Ellipsis("…")
						})
					}
				}
				for i, f := range d.frames {
					row := ui.Row(c).Height(22).Gap(6).Padding(0, 6).AlignItems(ui.Center).Radius(6).
						Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(f.Name)
					if i == d.frame {
						row.Background(k.selected)
					} else if row.Hovered() {
						row.Background(k.hover)
					}
					row.OnClick(func() { a.debugSelectFrame(i) })
					row.Children(func() {
						ui.Text(c, f.Name).FontSize(12).TextColor(k.text).SingleLine()
						at := filepath.Base(f.Source.Path) + ":" + strconv.Itoa(f.Line)
						ui.Text(c, at).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…")
					})
				}
			})
		})
		debugLabel(c, k, "Watch")
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).Padding(0, 4, 6, 4).Gap(1).FillWidth().Children(func() {
				for i, w := range d.watch {
					ui.Row(c).Height(24).Gap(6).Padding(0, 6).AlignItems(ui.Center).Children(func() {
						// The expression is the row's input, so that a
						// wrong one is corrected where it is written.
						expr := ui.TextInputBase(c, &d.watch[i].expr).Label(w.expr).FontSize(12).TextColor(k.text).Shrink(1).MinWidth(0)
						if expr.Submitted() {
							a.debugEvaluateAll()
						}
						if w.err != "" {
							ui.Text(c, w.err).FontSize(12).TextColor(c.Theme().Danger).SingleLine().Ellipsis("…")
						} else {
							ui.Text(c, w.value).FontSize(12).TextColor(k.textMuted).SingleLine().Ellipsis("…")
						}
						ui.Spacer(c)
						if iconButton(c, k, "x", "Remove watch", 20, 12).Clicked() {
							a.debugRemoveWatch(i)
						}
					})
				}
			})
		})
	})
}

// debugVarsPage is the selected frame's scopes and their variables, which
// open in place as the user asks for their children.
func (a *App) debugVarsPage(c *ui.Context, k *colors, d *debugService) {
	ui.Column(c).Grow(1).MinWidth(0).MinHeight(0).Children(func() {
		debugLabel(c, k, "Variables")
		if d.phase == debugOff && len(d.lines) == 0 && len(d.frames) == 0 {
			placeholder(c, k, "Run a configuration under the debugger to see it here.")
			return
		}
		if len(d.scopes) == 0 {
			note := "No variables."
			if !d.stopped() {
				note = "The program is not stopped."
			}
			buildNote(c, note, k.textMuted)
			return
		}
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).Padding(0, 4, 6, 4).Gap(1).FillWidth().Children(func() {
				for _, s := range d.scopes {
					ui.Text(c, s.Name).FontSize(11.5).TextColor(k.textMuted).SingleLine().Padding(0, 6)
					a.debugVarRows(c, k, d, s.VariablesReference, 0)
				}
			})
		})
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

// debugConsolePage is what the program printed and evaluated, with the
// prompt that evaluates one more expression.
func (a *App) debugConsolePage(c *ui.Context, k *colors, d *debugService) {
	ui.Column(c).Width(330).Shrink(0).MinHeight(0).Children(func() {
		debugLabel(c, k, "Debug Console")
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).Padding(0, 6, 6, 6).Gap(1).FillWidth().Children(func() {
				for _, l := range d.lines {
					col := k.text
					switch l.level {
					case debugError:
						col = c.Theme().Danger
					case debugNote:
						col = k.textMuted
					}
					ui.Text(c, l.text).FontSize(12).TextColor(col).Selectable()
				}
			})
		})
		ui.Row(c).Height(32).Padding(0, 6, 0, 6).Gap(6).AlignItems(ui.Center).Children(func() {
			in := ui.TextInputBase(c, &d.input).Label("Debug console").FontSize(12).TextColor(k.text).FillWidth()
			if in.Submitted() {
				a.debugRunConsole()
			}
			if ui.Button(c, "Evaluate").Clicked() {
				a.debugRunConsole()
			}
			if ui.Button(c, "Add Watch").Clicked() {
				a.debugAddWatch()
			}
		})
	})
}
