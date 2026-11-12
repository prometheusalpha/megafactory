package app

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"megafactory/internal/build"
	"megafactory/internal/dap"
	"megafactory/internal/runcfg"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Where a debug session is. One field says it, so no pair of flags can
// disagree about a session that is both starting and ended.
const (
	debugOff = iota
	// debugStarting asks jdtls for the adapter's port and dials it.
	debugStarting
	// debugLaunching has sent initialize and launch, and waits for the
	// adapter to say it is ready for breakpoints.
	debugLaunching
	debugRunning
	debugStopped
	debugEnded
	debugFailed
)

// debugStatus names a phase for the panel's header.
func debugStatus(d *debugService) string {
	switch d.phase {
	case debugOff:
		return "Not debugging"
	case debugStarting:
		return "Starting the debugger…"
	case debugLaunching:
		return "Launching…"
	case debugRunning:
		return "Running"
	case debugStopped:
		if d.frame < len(d.frames) {
			f := d.frames[d.frame]
			return fmt.Sprintf("Paused at %s:%d", shortPath(f.Source.Path), f.Line)
		}
		return "Paused"
	case debugEnded:
		return "The program ended"
	case debugFailed:
		return "The debugger failed"
	}
	return ""
}

func shortPath(p string) string {
	return filepath.Base(p)
}

// debugWatch is a watch expression and what it was where the program last
// stopped. An evaluation is one too, read once and not again at the next
// stop.
type debugWatch struct {
	expr  string
	value string
	err   string
}

// debugText is a file's text as its breakpoints were set: the buffer
// version it was, and the lines, which say what an edit moved.
type debugText struct {
	version uint64
	lines   []string
}

// debugService is the debugger's state: the adapter session, the
// breakpoints, and what the panels show. The app holds it whole and the
// panels read it; nothing else in the app keeps debug state.
type debugService struct {
	phase int
	err   error
	// gen names the session, so that a stopped one's events are dropped.
	gen int
	// cfg is the run configuration the session debugged, which Restart
	// starts again.
	cfg  string
	conn *dap.Conn
	// dialing is the connection of a session still starting, which conn
	// is not yet: the launch may have started the program by then, so a
	// closing app hangs it up too. The starting job sets it, off the UI.
	dialing atomic.Pointer[dap.Conn]

	bps *dap.Breakpoints
	// last is each file's text as its breakpoints moved last.
	last map[string]debugText

	threads []dap.Thread
	thread  int
	frames  []dap.StackFrame
	frame   int
	scopes  []dap.Scope

	// vars holds the children of each variables reference: a scope's, or a
	// variable's, read when it first opens.
	vars     map[int][]dap.Variable
	expanded map[int]bool

	watch []debugWatch
	evals []debugWatch
	input string
	// out is what the program printed, and the debugger's errors, as the
	// run console shows a run's. It is nil before the first session.
	out *runConsoleState
	// tab is the page's Output or Watch tab.
	tab int

	// file and line are where the session stopped, which its editor opens
	// and paints.
	file string
	line int
}

func newDebugService() debugService {
	return debugService{
		bps:      dap.NewBreakpoints(),
		last:     map[string]debugText{},
		vars:     map[int][]dap.Variable{},
		expanded: map[int]bool{},
	}
}

// clearSession forgets a session's threads, frames and variables, keeping
// what a session does not own: its breakpoints and the console.
func (d *debugService) clearSession() {
	d.conn = nil
	d.threads, d.thread = nil, 0
	d.frames, d.frame = nil, 0
	d.scopes = nil
	d.vars = map[int][]dap.Variable{}
	d.expanded = map[int]bool{}
	d.file, d.line = "", 0
	for i := range d.watch {
		d.watch[i].value, d.watch[i].err = "", ""
	}
}

// frameID is the selected frame's id, 0 when there is none.
func (d *debugService) frameID() int {
	if d.frame < len(d.frames) {
		return d.frames[d.frame].ID
	}
	return 0
}

// stopped reports whether the program is paused, which the toolbar's
// stepping and the console's prompt need it to be.
func (d *debugService) stopped() bool { return d.phase == debugStopped && d.conn != nil }

// live is whether a session is starting or has a program to stop.
func (d *debugService) live() bool { return d.conn != nil || d.phase == debugStarting }

func (a *App) debugPath() string {
	return filepath.Join(a.project, ".idea", "megafactory_breakpoints.json")
}

// loadBreakpoints reads the project's breakpoints, as a project opening
// does.
func (a *App) loadBreakpoints() {
	b, err := dap.LoadBreakpoints(a.debugPath())
	if err != nil {
		a.host.ShowError("Couldn't read megafactory_breakpoints.json", err.Error())
	}
	a.debug.bps = b
	a.debug.last = map[string]debugText{}
}

func (a *App) saveBreakpoints() {
	if a.project == "" {
		return
	}
	if err := a.debug.bps.Save(a.debugPath()); err != nil {
		a.host.ShowError("Couldn't save megafactory_breakpoints.json", err.Error())
	}
}

// toggleBreakpoint sets or clears a line of a file, and gives a live
// session the file's new set.
func (a *App) toggleBreakpoint(rel string, line int) {
	if line < 1 || !isJavaPath(rel) {
		return
	}
	a.debug.bps.Toggle(rel, line)
	a.saveBreakpoints()
	a.debugSyncBreakpoints(rel)
}

// debugSyncBreakpoints sends a file's whole set to the adapter, whose
// protocol replaces it.
func (a *App) debugSyncBreakpoints(rel string) {
	d := &a.debug
	conn, gen := d.conn, d.gen
	if conn == nil {
		return
	}
	abs, lines := a.abs(rel), d.bps.Lines(rel)
	a.host.Run(func() func() {
		_, err := conn.SetBreakpoints(abs, lines)
		return func() {
			if d.gen == gen && err != nil {
				d.err = err
			}
		}
	})
}

// debugFrame keeps the open file's breakpoints on their lines as its text
// is edited: the buffer's version says whether any line moved.
func (a *App) debugFrame() {
	d := &a.debug
	if d.bps.Empty() || a.active >= len(a.tabs) {
		return
	}
	t := a.tabs[a.active]
	if t.ed == nil || !isJavaPath(t.path) {
		return
	}
	lines := d.bps.Lines(t.path)
	if len(lines) == 0 {
		return
	}
	snap := t.ed.Buffer.Snapshot()
	cur := debugText{version: snap.Version(), lines: snapshotLines(snap)}
	prev, seen := d.last[t.path]
	d.last[t.path] = cur
	if !seen || prev.version == cur.version {
		return
	}
	moved := dap.Remap(prev.lines, cur.lines, lines)
	if slices.Equal(moved, lines) {
		return
	}
	d.bps.Set(t.path, moved)
	a.saveBreakpoints()
	a.debugSyncBreakpoints(t.path)
}

// snapshotLines is a snapshot's text as its lines.
func snapshotLines(s editor.Snapshot) []string {
	var buf bytes.Buffer
	if _, err := s.WriteTo(&buf); err != nil {
		return nil
	}
	return strings.Split(buf.String(), "\n")
}

// startDebug debugs the configuration the toolbar selected.
func (a *App) startDebug() {
	cfg, ok := a.runStore.ByID(a.runConfig)
	if !ok {
		a.host.ShowError("No run configuration", "Add one in Edit Configurations.")
		return
	}
	a.startDebugConfig(cfg)
}

// startDebugConfig launches cfg under java-debug: jdtls hands out the
// adapter's port, and the adapter launches the JVM itself.
func (a *App) startDebugConfig(cfg runcfg.Config) {
	if !cfg.Kind.Debuggable() {
		a.host.ShowError("Can't debug "+cfg.Name, "Only Application and Spring Boot configurations run under the debugger.")
		return
	}
	srv := a.java.srv
	if srv == nil {
		a.host.ShowError("Can't debug "+cfg.Name, "The Java language server is not ready.")
		return
	}
	d := &a.debug
	if d.conn != nil {
		a.debugStop()
	}
	d.gen++
	gen := d.gen
	d.clearSession()
	d.phase, d.err, d.cfg = debugStarting, nil, cfg.ID
	d.out, d.evals = newRunConsole(cfg.Name, gen), nil
	d.out.key, d.out.ed.Label = "debug console:"+strconv.Itoa(gen), "Debug output"
	a.bottomTab, a.showBottom = 1, true
	bp, _ := build.Detect(a.project)
	r := appResolver{app: a, project: a.project, srv: srv, build: bp}
	a.host.Run(func() func() {
		fail := func(err error) func() {
			return func() {
				if d.gen == gen {
					a.debugFail(err)
				}
			}
		}
		spec, err := runcfg.DebugSpec(cfg, r)
		if err != nil {
			return fail(err)
		}
		port, err := srv.StartDebugSession()
		if err != nil {
			return fail(err)
		}
		conn, err := dap.Dial(context.Background(), fmt.Sprintf("127.0.0.1:%d", port), func(ev dap.Event) {
			a.host.Update(func() { a.debugEvent(gen, ev) })
		})
		if err != nil {
			return fail(err)
		}
		d.dialing.Store(conn)
		if err := debugHandshake(conn, cfg, spec); err != nil {
			d.dialing.CompareAndSwap(conn, nil)
			conn.Close()
			return fail(err)
		}
		return func() {
			d.dialing.CompareAndSwap(conn, nil)
			// Stopped while starting: the launch has run the program,
			// which a closed connection alone leaves running.
			if d.gen != gen {
				a.host.Run(func() func() { conn.Disconnect(true); conn.Close(); return func() {} })
				return
			}
			d.conn, d.phase = conn, debugLaunching
			// The breakpoints go now, so that the events they cause and
			// the ones the launch causes find a session to land in.
			a.debugConfigure(gen)
		}
	})
}

// debugHandshake asks the adapter to start the program: its own initialize,
// then the launch java-debug runs java with. The breakpoints go after the
// adapter says it is initialized.
func debugHandshake(conn *dap.Conn, cfg runcfg.Config, spec runcfg.Debug) error {
	if err := conn.Initialize(); err != nil {
		return err
	}
	return conn.Launch(dap.LaunchArgs{
		Type: "java", Name: cfg.Name, Request: "launch",
		MainClass:   spec.MainClass,
		ProjectName: spec.ProjectName,
		Cwd:         spec.Cwd,
		Args:        spec.Args,
		VMArgs:      spec.VMArgs,
		ModulePaths: spec.ModulePaths,
		ClassPaths:  spec.ClassPaths,
		JavaExec:    spec.Java,
		Env:         spec.Env,
		// The console shows what the program writes, in the panel, rather
		// than a terminal the adapter would ask the app to open.
		Console: "internalConsole",
	})
}

// debugEvent applies one event of the session's generation.
func (a *App) debugEvent(gen int, ev dap.Event) {
	d := &a.debug
	if d.gen != gen {
		return
	}
	switch ev.Event {
	case "stopped":
		var s dap.StoppedEvent
		dap.Decode(ev.Body, &s)
		d.phase = debugStopped
		// A stop is to be read, so its variables and watches show.
		d.tab, a.bottomTab, a.showBottom = 1, 1, true
		if s.ThreadID != 0 {
			d.thread = s.ThreadID
		}
		a.debugLoadStacks()
	case "continued":
		d.phase = debugRunning
	case "thread":
		var t struct {
			Reason   string `json:"reason"`
			ThreadID int    `json:"threadId"`
		}
		dap.Decode(ev.Body, &t)
		a.debugThreadEvent(t.Reason, t.ThreadID)
	case "output":
		var o dap.OutputEvent
		dap.Decode(ev.Body, &o)
		d.addOutput(o)
	case "terminated":
		a.debugEnded()
	}
}

// debugConfigure sends the project's breakpoints and then configurationDone,
// which is what lets the adapter start the program.
func (a *App) debugConfigure(gen int) {
	d := &a.debug
	conn := d.conn
	if conn == nil {
		return
	}
	type file struct {
		path  string
		lines []int
	}
	files := make([]file, 0, len(d.bps.Files()))
	for _, rel := range d.bps.Files() {
		files = append(files, file{path: a.abs(rel), lines: d.bps.Lines(rel)})
	}
	a.host.Run(func() func() {
		var failed []string
		for _, f := range files {
			if _, err := conn.SetBreakpoints(f.path, f.lines); err != nil {
				failed = append(failed, filepath.Base(f.path)+": "+err.Error())
			}
		}
		err := conn.ConfigurationDone()
		return func() {
			if d.gen != gen {
				return
			}
			for _, msg := range failed {
				d.print(msg, true)
			}
			if err != nil {
				a.debugFail(err)
				return
			}
			d.phase = debugRunning
		}
	})
}

// debugThreadEvent keeps the thread list current as the program starts and
// ends threads, which is what Pause needs before the first stop.
func (a *App) debugThreadEvent(reason string, id int) {
	d := &a.debug
	if id == 0 {
		return
	}
	switch reason {
	case "started":
		if !slices.ContainsFunc(d.threads, func(t dap.Thread) bool { return t.ID == id }) {
			d.threads = append(d.threads, dap.Thread{ID: id, Name: "Thread " + strconv.Itoa(id)})
		}
		if d.thread == 0 {
			d.thread = id
		}
	case "exited":
		d.threads = slices.DeleteFunc(d.threads, func(t dap.Thread) bool { return t.ID == id })
		if d.thread == id {
			d.thread = 0
			if len(d.threads) > 0 {
				d.thread = d.threads[0].ID
			}
		}
	}
}

// debugLoadStacks reads the threads and the stopped thread's frames, then
// selects the innermost.
func (a *App) debugLoadStacks() {
	d := &a.debug
	conn, gen, thread := d.conn, d.gen, d.thread
	if conn == nil || thread == 0 {
		return
	}
	a.host.Run(func() func() {
		threads, terr := conn.Threads()
		frames, err := conn.StackTrace(thread)
		return func() {
			if d.gen != gen {
				return
			}
			if terr == nil && len(threads) > 0 {
				d.threads = threads
			}
			if err != nil {
				d.err = err
				return
			}
			d.frames, d.frame = frames, 0
			a.debugSelectFrame(0)
		}
	})
}

// debugSelectFrame makes a frame the one the variables and the watches read,
// and opens the file it is in at its line.
func (a *App) debugSelectFrame(i int) {
	d := &a.debug
	if i < 0 || i >= len(d.frames) {
		return
	}
	d.frame = i
	f := d.frames[i]
	if rel, ok := a.relative(f.Source.Path); ok && rel != "" {
		d.file, d.line = rel, f.Line
		// openAt counts lines from 0, as the editor does; the protocol
		// counts them from 1.
		a.openAt(rel, f.Line-1)
	}
	d.scopes = nil
	d.vars = map[int][]dap.Variable{}
	d.expanded = map[int]bool{}
	conn, gen := d.conn, d.gen
	if conn == nil {
		return
	}
	a.host.Run(func() func() {
		scopes, err := conn.Scopes(f.ID)
		vars := map[int][]dap.Variable{}
		if err == nil {
			for _, s := range scopes {
				if vs, verr := conn.Variables(s.VariablesReference); verr == nil {
					vars[s.VariablesReference] = vs
				}
			}
		}
		return func() {
			if d.gen != gen {
				return
			}
			if err != nil {
				d.err = err
				return
			}
			d.err = nil
			d.scopes, d.vars = scopes, vars
			a.debugEvaluateAll()
		}
	})
}

// debugExpand opens or folds a variable's children, reading them the first
// time.
func (a *App) debugExpand(ref int) {
	d := &a.debug
	if d.expanded[ref] {
		d.expanded[ref] = false
		return
	}
	d.expanded[ref] = true
	if _, ok := d.vars[ref]; ok {
		return
	}
	conn, gen := d.conn, d.gen
	if conn == nil {
		return
	}
	a.host.Run(func() func() {
		vs, err := conn.Variables(ref)
		return func() {
			if d.gen != gen {
				return
			}
			if err != nil {
				d.err = err
				return
			}
			d.vars[ref] = vs
		}
	})
}

// debugResume continues or steps the stopped thread, then forgets the stop
// it left: the next stopped event says where it is.
func (a *App) debugResume(do func(*dap.Conn, int) error) {
	d := &a.debug
	conn, gen, thread := d.conn, d.gen, d.thread
	if conn == nil || thread == 0 {
		return
	}
	a.host.Run(func() func() {
		err := do(conn, thread)
		return func() {
			if d.gen != gen {
				return
			}
			if err != nil {
				d.err = err
				return
			}
			d.err = nil
			d.phase = debugRunning
			d.frames, d.frame, d.scopes = nil, 0, nil
			d.vars, d.expanded = map[int][]dap.Variable{}, map[int]bool{}
			d.file, d.line = "", 0
		}
	})
}

func (a *App) debugContinue() {
	a.debugResume(func(c *dap.Conn, t int) error { return c.Continue(t) })
}

func (a *App) debugStepOver() {
	a.debugResume(func(c *dap.Conn, t int) error { return c.Next(t) })
}

func (a *App) debugStepInto() {
	a.debugResume(func(c *dap.Conn, t int) error { return c.StepIn(t) })
}

func (a *App) debugStepOut() {
	a.debugResume(func(c *dap.Conn, t int) error { return c.StepOut(t) })
}

// debugPause interrupts the running thread; its stopped event follows.
func (a *App) debugPause() {
	d := &a.debug
	conn, thread := d.conn, d.thread
	if conn == nil || thread == 0 {
		return
	}
	a.host.Run(func() func() { conn.Pause(thread); return func() {} })
}

// debugStop ends the session and kills the program, keeping the console.
func (a *App) debugStop() {
	if conn := a.debugEnd(); conn != nil {
		a.host.Run(func() func() { conn.Disconnect(true); conn.Close(); return func() {} })
	}
}

// debugKill is debugStop for a closing app, which would exit before a job
// sent the disconnect: the program, launched by jdtls, would outlive it and
// keep its ports. It waits for the adapter, but not past quitWait.
func (a *App) debugKill() {
	conn := a.debugEnd()
	if conn == nil {
		conn = a.debug.dialing.Swap(nil)
	}
	if conn == nil {
		return
	}
	done := make(chan struct{})
	go func() { conn.Disconnect(true); close(done) }()
	select {
	case <-done:
	case <-time.After(quitWait):
	}
	conn.Close()
}

// quitWait bounds how long closing the app waits for the debugger.
const quitWait = 2 * time.Second

// debugEnd forgets the session, returning its connection to hang up.
func (a *App) debugEnd() *dap.Conn {
	d := &a.debug
	conn := d.conn
	d.gen++
	d.phase = debugOff
	d.clearSession()
	return conn
}

// debugEnded handles the adapter's terminated event, which is the program's
// last: its frames stay, to be read, but nothing can step.
func (a *App) debugEnded() {
	d := &a.debug
	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}
	d.phase = debugEnded
	d.scopes = nil
	d.vars = map[int][]dap.Variable{}
	d.expanded = map[int]bool{}
	if d.file != "" {
		d.file, d.line = "", 0
	}
}

// debugFail ends a session that could not start.
func (a *App) debugFail(err error) {
	d := &a.debug
	if d.conn != nil {
		d.conn.Close()
		d.conn = nil
	}
	d.phase, d.err = debugFailed, err
	d.print(err.Error(), true)
	a.bottomTab, a.showBottom = 1, true
}

// debugRestart runs the session's configuration again.
func (a *App) debugRestart() {
	cfg := a.debug.cfg
	a.debugStop()
	if c, ok := a.runStore.ByID(cfg); ok {
		a.startDebugConfig(c)
	}
}

// debugMain debugs a class with main or @SpringBootApplication, as the
// gutter and the editor's context menu ask.
func (a *App) debugMain(rel, fqcn string, spring bool) {
	kind := runcfg.Application
	if spring {
		kind = runcfg.SpringBoot
	}
	cfg, err := a.runStore.Upsert(runcfg.Config{Name: filepath.Base(fqcn), Kind: kind, MainClass: fqcn})
	if err != nil {
		a.host.ShowError("Couldn't make a run configuration", err.Error())
		return
	}
	a.selectRunConfig(cfg.ID)
	a.startDebugConfig(cfg)
}

// debugEvaluateAll refreshes every watch expression at the selected frame.
func (a *App) debugEvaluateAll() {
	d := &a.debug
	conn, gen, frame := d.conn, d.gen, d.frameID()
	if conn == nil || frame == 0 {
		return
	}
	exprs := make([]string, len(d.watch))
	for i, w := range d.watch {
		exprs[i] = w.expr
	}
	a.host.Run(func() func() {
		values := make([]debugWatch, len(exprs))
		for i, expr := range exprs {
			values[i] = debugWatch{expr: expr}
			if v, err := conn.Evaluate(expr, frame, "watch"); err == nil {
				values[i].value = v.Value
			} else {
				values[i].err = err.Error()
			}
		}
		return func() {
			if d.gen != gen {
				return
			}
			// A watch added meanwhile keeps its place: only the ones the
			// job asked about are set.
			for i := range d.watch {
				if i < len(values) && d.watch[i].expr == values[i].expr {
					d.watch[i].value, d.watch[i].err = values[i].value, values[i].err
				}
			}
		}
	})
}

// debugAddWatch adds the console's input as a watch expression.
func (a *App) debugAddWatch() {
	d := &a.debug
	expr := strings.TrimSpace(d.input)
	if expr == "" {
		return
	}
	d.input = ""
	d.watch = append(d.watch, debugWatch{expr: expr})
	a.debugEvaluateAll()
}

func (a *App) debugRemoveWatch(i int) {
	d := &a.debug
	if i < 0 || i >= len(d.watch) {
		return
	}
	d.watch = slices.Delete(d.watch, i, i+1)
}

// debugRunConsole evaluates the console's input at the selected frame, as
// its prompt does.
func (a *App) debugRunConsole() {
	d := &a.debug
	expr := strings.TrimSpace(d.input)
	if expr == "" {
		return
	}
	d.input = ""
	conn, gen, frame := d.conn, d.gen, d.frameID()
	if conn == nil || frame == 0 {
		d.evals = append(d.evals, debugWatch{expr: expr, err: "the program is not stopped"})
		return
	}
	a.host.Run(func() func() {
		v, err := conn.Evaluate(expr, frame, "repl")
		return func() {
			if d.gen != gen {
				return
			}
			e := debugWatch{expr: expr}
			if err != nil {
				e.err = err.Error()
			} else {
				e.value = v.Value
			}
			d.evals = append(d.evals, e)
		}
	})
}

func (a *App) debugRemoveEval(i int) {
	d := &a.debug
	if i < 0 || i >= len(d.evals) {
		return
	}
	d.evals = slices.Delete(d.evals, i, i+1)
}

// addOutput appends what the program wrote, a line at a time, as the
// adapter sends it in chunks.
func (d *debugService) addOutput(o dap.OutputEvent) {
	for _, line := range strings.Split(o.Output, "\n") {
		if line = strings.TrimSuffix(line, "\r"); line != "" {
			d.print(line, o.Category == "stderr")
		}
	}
}

// print adds a line to the output, which a failure before any session has
// to make first.
func (d *debugService) print(text string, stderr bool) {
	if d.out == nil {
		d.out = newRunConsole("Debug", 0)
		d.out.key, d.out.ed.Label = "debug console:0", "Debug output"
	}
	d.out.add(text, stderr)
}

// wireGutter is what a tab's editor shows of running and debugging: the run
// mark of a runnable class, the breakpoints, and the line the session
// stopped on. It runs every frame, as the editor copies its State into the
// view it builds: a stale mark would stay otherwise.
func (a *App) wireGutter(c *ui.Context, ed *editor.State, t *tab) {
	if t == nil || t.ed == nil || !isJavaPath(t.path) {
		return
	}
	runLine, fqcn, spring, runnable := runnableIn(t)
	bps := a.debug.bps.Lines(t.path)
	ed.GutterMarks = nil
	if runnable {
		ed.GutterMarks = append(ed.GutterMarks, editor.GutterMark{Line: runLine, Color: c.Theme().Success})
	}
	for _, b := range bps {
		// Breakpoints count lines from 1, as the protocol does; the
		// editor's marks and clicks count them from 0.
		ed.GutterMarks = append(ed.GutterMarks, editor.GutterMark{Line: b - 1, Color: c.Theme().Danger})
	}
	ed.OnGutterClick = func(l int) {
		if runnable && l == runLine {
			a.runMain(t.path, fqcn, spring)
			return
		}
		a.toggleBreakpoint(t.path, l+1)
	}
	ed.ContextMenu = func(m *ui.Menu) {
		if runnable {
			short := filepath.Base(fqcn)
			if m.Item("Run '" + short + "'").Chosen() {
				a.runMain(t.path, fqcn, spring)
			}
			if m.Item("Debug '" + short + "'").Chosen() {
				a.debugMain(t.path, fqcn, spring)
			}
		}
		if m.Item("Toggle Breakpoint").Chosen() {
			a.toggleBreakpoint(t.path, t.ed.CursorPos().Line+1)
		}
	}
	if a.debug.stopped() && a.debug.file == t.path && a.debug.line > 0 {
		line := a.debug.line - 1
		ed.LineBackgrounds = []editor.LineBackground{{First: line, Last: line, Color: c.Theme().Accent.Alpha(0.18)}}
	} else {
		ed.LineBackgrounds = nil
	}
}

// debugErr is the session's last error, as the panel shows it.
func (d *debugService) debugErr() string {
	if d.err == nil {
		return ""
	}
	return d.err.Error()
}
