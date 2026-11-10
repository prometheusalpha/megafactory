package app

import (
	"bytes"

	"megafactory/internal/javalang"
	"megafactory/internal/runner"

	"github.com/egoist/mygo/ui"
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

// runnableIn is the class a tab offers to run: the line of its main method,
// its qualified name, and whether the class is a Spring Boot application.
// ok is false for a tab with no runnable class.
func runnableIn(t *tab) (line int, fqcn string, spring, ok bool) {
	if t == nil || t.ed == nil {
		return 0, "", false, false
	}
	var buf bytes.Buffer
	if _, err := t.ed.Buffer.Snapshot().WriteTo(&buf); err != nil {
		return 0, "", false, false
	}
	src := buf.String()
	line, fqcn, ok = javalang.Runnable(t.path, src)
	if !ok {
		return 0, "", false, false
	}
	return line, fqcn, javalang.SpringBoot(t.path, src), true
}
