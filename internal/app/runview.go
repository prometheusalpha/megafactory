package app

import (
	"bytes"
	"strconv"

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

	// id keys the editor, so a rerun starts a fresh one.
	id int
	// ed shows the lines as one read-only text, so a selection can span
	// them. Its tokenizer tells stderr lines from stdout by line number.
	ed *editor.State
	// wrap is soft wrap, on until the Wrap button turns it off.
	wrap bool
	// follow keeps the view on the last line, every frame, until the user
	// scrolls up; scrolling back to the end, or Scroll to End, resumes it.
	// It is a mode rather than a check as each line comes: soft-wrapped
	// lines are measured only after the scroll, which leaves the view short
	// of the end, and that would read as the user having scrolled away.
	follow bool
	// pinned is where following last put the view, to tell a scroll up
	// from it. Measuring wraps only ever moves the view down.
	pinned float64
}

func newRunConsole(name string, id int) *runConsoleState {
	r := &runConsoleState{name: name, id: id, follow: true, wrap: true}
	lang := &editor.Language{Name: "console", Tokenizer: editor.TokenizerFunc(r.tokenize)}
	buf := editor.NewBuffer("")
	buf.SetLanguage(lang)
	r.ed = &editor.State{
		Buffer: buf, ReadOnly: true, NoFolding: true, NoActiveLine: true, NoIndentGuides: true,
		NoScrollPastEnd: true, LineNumbers: editor.LineNumbersOff,
		Label: "Run output",
	}
	return r
}

// tokenize colors a line by its pipe and marks its file references. The
// state is the number of the line, as the highlighter walks them in order.
func (r *runConsoleState) tokenize(line string, state uint64, toks []editor.Token) ([]editor.Token, uint64) {
	base := editor.TokenText
	if int(state) < len(r.lines) && r.lines[state].stderr {
		base = editor.TokenInvalid
	}
	at := 0
	for _, lk := range runner.Links(line) {
		if lk.From > at {
			toks = append(toks, editor.Token{End: lk.From, Kind: base})
		}
		toks = append(toks, editor.Token{End: lk.To, Kind: editor.TokenLink})
		at = lk.To
	}
	if at < len(line) {
		toks = append(toks, editor.Token{End: len(line), Kind: base})
	}
	return toks, state + 1
}

// add appends a line; consoleEditor scrolls to it while following.
func (r *runConsoleState) add(text string, stderr bool) {
	r.lines = append(r.lines, consoleLine{text: text, stderr: stderr})
	if len(r.lines) > 1 {
		text = "\n" + text
	}
	n := r.ed.Buffer.Len()
	r.ed.Buffer.Edit(editor.Edit{Start: n, End: n, Text: text})
}

// clear drops the output so far; the process, if live, goes on and its next
// lines start the text again.
func (r *runConsoleState) clear() {
	r.lines = nil
	r.ed.Buffer.Reset("")
	r.followEnd()
}

// followEnd follows the output again from wherever the view is now.
func (r *runConsoleState) followEnd() {
	r.follow, r.pinned = true, 0
}

// atEnd is whether the last frame showed the last line.
func (r *runConsoleState) atEnd() bool {
	end := r.ed.LineTop(r.ed.Buffer.LineCount())
	return r.ed.ScrollY()+r.ed.ViewportHeight() >= end-4
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
			if r := a.runConsole; r != nil {
				a.consoleTools(c, k, r)
			}
			if iconButton(c, k, "x", "Stop", 24, 13).Clicked() {
				a.stopRun()
			}
		})
		r := a.runConsole
		if r == nil {
			buildNote(c, "Run a configuration to see its output.", k.textMuted)
			return
		}
		a.consoleEditor(c, k, r)
		if r.done {
			buildNote(c, "Process "+r.exit, k.textMuted)
		}
	})
}

// consoleTools are the output's buttons: soft wrap, clear, and jump to the
// last line, which follows the output again.
func (a *App) consoleTools(c *ui.Context, k *colors, r *runConsoleState) {
	on := ui.Box(c).Radius(24 / 2.6)
	if r.wrap {
		on.Background(k.selected)
	}
	on.Children(func() {
		if iconButton(c, k, "wrap-text", "Soft Wrap", 24, 13).Clicked() {
			r.wrap = !r.wrap
		}
	})
	if iconButton(c, k, "trash-2", "Clear Output", 24, 13).Clicked() {
		r.clear()
	}
	if iconButton(c, k, "arrow-down-to-line", "Scroll to End", 24, 13).Clicked() {
		r.followEnd()
	}
}

// consoleEditor draws the output: monospace, selectable across lines, with a
// Cmd+click on a File.java:123 reference opening it.
func (a *App) consoleEditor(c *ui.Context, k *colors, r *runConsoleState) {
	th := *activeEditorTheme(c, k)
	th.Tokens[editor.TokenInvalid].Color = c.Theme().Danger
	th.Tokens[editor.TokenLink].Color = c.Theme().Accent
	r.ed.Theme = &th
	r.ed.SoftWrap = r.wrap
	r.ed.Font, r.ed.LineHeight = code.editorFont(), prefs.EditorLineHeight
	r.ed.OnDefinition = func(offset int) {
		p := r.ed.Buffer.Pos(offset)
		line := r.ed.Buffer.Line(p.Line)
		for _, lk := range runner.Links(line) {
			if p.Col >= lk.From && p.Col <= lk.To {
				a.openStackLink(line[lk.From:lk.To])
				return
			}
		}
	}
	c.Key("run console:" + strconv.Itoa(r.id))
	editor.View(c, r.ed).Grow(1).MinHeight(0).FillWidth()
	// The view has taken this frame's wheel and drags by now, so a scroll
	// up shows against where following left it.
	if r.follow && r.ed.ScrollY() < r.pinned-0.5 {
		r.follow = false
	} else if !r.follow && r.ed.ViewportHeight() > 0 && r.atEnd() {
		r.follow = true
	}
	if r.follow {
		r.ed.SetScrollY(max(r.ed.LineTop(r.ed.Buffer.LineCount())-r.ed.ViewportHeight(), 0))
		r.pinned = r.ed.ScrollY()
	}
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
