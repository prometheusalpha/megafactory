package app

import (
	"bytes"
	"path"

	"megafactory/internal/javalang"
	"megafactory/internal/runner"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// consoleLine is one process line with which pipe it came from, so stdout
// and stderr keep different colors.
type consoleLine struct {
	text   string
	stderr bool
}

// runConsoleState is the output of the current run. The app's runGen,
// captured by the launch closures, drops lines of a superseded run.
type runConsoleState struct {
	name  string
	lines []consoleLine
	done  bool
	exit  string
}

func (a *App) runConsolePage(c *ui.Context, k *colors) {
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Row(c).Height(30).Padding(0, 8, 0, 6).Gap(8).AlignItems(ui.Center).Children(func() {
			name := "No process is running"
			if a.runConsole != nil {
				name = a.runConsole.name
			}
			ui.Text(c, name).FontSize(12).FontWeight(600).TextColor(k.textMuted).SingleLine()
			ui.Spacer(c)
			if iconButton(c, k, "x", "Stop", 24, 13).Clicked() {
				a.stopRun()
			}
			if iconButton(c, k, "refresh-cw", "Rerun", 24, 13).Clicked() {
				a.rerun()
			}
			if iconButton(c, k, "settings-2", "Edit Configurations", 24, 13).Clicked() {
				a.editConfigurations()
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).Padding(0, 8, 8, 8).FillWidth().Children(func() {
				if a.runConsole == nil {
					buildNote(c, "Run a configuration to see its output.", k.textMuted)
					return
				}
				for _, l := range a.runConsole.lines {
					a.consoleLineView(c, k, l)
				}
				if a.runConsole.done {
					buildNote(c, "Process "+a.runConsole.exit, k.textMuted)
				}
			})
		})
	})
}

// consoleLineView colors a line by its stream and makes a File.java:123
// reference clickable, reusing the terminal's one link opener.
func (a *App) consoleLineView(c *ui.Context, k *colors, l consoleLine) {
	col := k.text
	if l.stderr {
		col = c.Theme().Danger
	}
	links := runner.Links(l.text)
	if len(links) == 0 {
		ui.Text(c, l.text).FontSize(12).TextColor(col).Selectable()
		return
	}
	ui.RichText(c).Children(func() {
		at := 0
		for _, lk := range links {
			if lk.From > at {
				ui.Text(c, l.text[at:lk.From]).TextColor(col)
			}
			label := l.text[lk.From:lk.To]
			link := ui.Text(c, label).TextColor(c.Theme().Accent).Label(label)
			if link.Clicked() {
				a.openStackLink(label)
			}
			at = lk.To
		}
		if at < len(l.text) {
			ui.Text(c, l.text[at:]).TextColor(col)
		}
	})
}

// wireRunGutter marks a runnable class and offers Run from the gutter and
// the editor's context menu. It runs every frame, as the editor copies its
// State into the view it builds.
func (a *App) wireRunGutter(c *ui.Context, ed *editor.State, t *tab) {
	if t == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	var buf bytes.Buffer
	if _, err := t.ed.Buffer.Snapshot().WriteTo(&buf); err != nil {
		return
	}
	src := buf.String()
	line, fqcn, ok := javalang.Runnable(t.path, src)
	if !ok {
		return
	}
	spring := javalang.SpringBoot(t.path, src)
	short := path.Base(fqcn)
	ed.GutterMarks = []editor.GutterMark{{Line: line, Color: c.Theme().Success}}
	ed.OnGutterClick = func(l int) {
		if l == line {
			a.runMain(t.path, fqcn, spring)
		}
	}
	ed.ContextMenu = func(m *ui.Menu) {
		if m.Item("Run '" + short + "'").Chosen() {
			a.runMain(t.path, fqcn, spring)
		}
	}
}
