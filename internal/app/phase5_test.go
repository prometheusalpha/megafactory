package app

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"megafactory/internal/dap"
	"megafactory/internal/javalang"
	"megafactory/internal/jdk"
	"megafactory/internal/runcfg"
	"megafactory/internal/setup"

	"github.com/egoist/mygo/ui"
)

// fakeAdapter is a debug adapter on a loopback port: it answers the app's
// requests, records them, and sends the events a test asks for. It stands in
// for java-debug, which is a JVM away.
type fakeAdapter struct {
	t    *testing.T
	ln   net.Listener
	port int

	mu   sync.Mutex
	conn net.Conn
	seq  int
	got  map[string][]json.RawMessage

	// answer is what a request's answer carries; nil answers with no body.
	answer func(command string, args json.RawMessage) any
}

func newFakeAdapter(t *testing.T) *fakeAdapter {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAdapter{t: t, ln: ln, got: map[string][]json.RawMessage{}, port: ln.Addr().(*net.TCPAddr).Port}
	go f.serve()
	t.Cleanup(func() { ln.Close() })
	return f
}

func (f *fakeAdapter) serve() {
	conn, err := f.ln.Accept()
	if err != nil {
		return
	}
	f.mu.Lock()
	f.conn = conn
	f.mu.Unlock()
	r := bufio.NewReader(conn)
	for {
		body, err := readTestFrame(r)
		if err != nil {
			return
		}
		var m struct {
			Seq       int
			Type      string
			Command   string
			Arguments json.RawMessage
		}
		if json.Unmarshal(body, &m) != nil || m.Type != "request" {
			continue
		}
		f.mu.Lock()
		f.got[m.Command] = append(f.got[m.Command], m.Arguments)
		f.mu.Unlock()
		var out any
		if f.answer != nil {
			out = f.answer(m.Command, m.Arguments)
		}
		f.reply(m.Seq, m.Command, out)
	}
}

func (f *fakeAdapter) reply(requestSeq int, command string, body any) {
	f.mu.Lock()
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	msg := map[string]any{"seq": seq, "type": "response", "request_seq": requestSeq, "command": command, "success": true}
	if body != nil {
		msg["body"] = body
	}
	f.write(msg)
}

// event sends one, as a running adapter does when the program stops or
// writes.
func (f *fakeAdapter) event(name string, body any) {
	f.mu.Lock()
	f.seq++
	seq := f.seq
	f.mu.Unlock()
	f.write(map[string]any{"seq": seq, "type": "event", "event": name, "body": body})
}

func (f *fakeAdapter) write(v any) {
	f.mu.Lock()
	conn := f.conn
	f.mu.Unlock()
	if conn == nil {
		return
	}
	body, err := json.Marshal(v)
	if err != nil {
		f.t.Errorf("encode: %v", err)
		return
	}
	conn.Write([]byte("Content-Length: " + strconv.Itoa(len(body)) + "\r\n\r\n" + string(body)))
}

// requests is what the app sent for a command, in order.
func (f *fakeAdapter) requests(command string) []json.RawMessage {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.got[command])
}

// sent reports whether a command arrived at all.
func (f *fakeAdapter) sent(command string) bool { return len(f.requests(command)) > 0 }

// args is the i-th request's arguments, decoded.
func (f *fakeAdapter) args(t *testing.T, command string, i int) map[string]any {
	t.Helper()
	got := f.requests(command)
	if i >= len(got) {
		t.Fatalf("%s sent %d times, want %d", command, len(got), i+1)
	}
	var m map[string]any
	if err := json.Unmarshal(got[i], &m); err != nil {
		t.Fatalf("%s arguments: %v", command, err)
	}
	return m
}

// readTestFrame reads one protocol message, its headers then its body.
func readTestFrame(r *bufio.Reader) ([]byte, error) {
	n := -1
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if v, ok := strings.CutPrefix(strings.ToLower(line), "content-length:"); ok {
			if n, err = strconv.Atoi(strings.TrimSpace(v)); err != nil {
				return nil, err
			}
		}
	}
	if n < 0 {
		return nil, io.ErrUnexpectedEOF
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}

// eventually waits for what an event's handling sets, which arrives on the
// adapter's goroutine.
func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

// debugApp opens the sample with a fake language server and a fake adapter,
// and the demo class's editor. The JDK is a fixed one, so no machine's is
// needed to launch.
func debugApp(t *testing.T) (*App, *fakeAdapter, *ui.Tester) {
	t.Helper()
	adapter := newFakeAdapter(t)
	a := newApp(defaultState)
	a.branch = "dev"
	a.host.ShowError = func(title, msg string) { t.Logf("error shown: %s: %s", title, msg) }
	a.host.JavaJDK = func(context.Context, func(setup.Progress)) (jdk.JDK, []jdk.JDK, error) {
		return jdk.JDK{Home: "/jdk"}, nil, nil
	}
	f := newFakeJava()
	f.debugPort = adapter.port
	f.mainClasses = []javalang.MainClass{{MainClass: "com.example.demo.DemoApplication", ProjectName: "demo"}}
	f.classPaths = [][]string{nil, {"/classes"}}
	a.java.syncQueue = true
	a.java.starter = f.starter
	a.OpenProject(sampleProject(t))
	tt := ui.NewTester(a.View, 1280, 800)
	click(t, tt, "src", "main", "java", "com", "example", "demo", "DemoApplication.java")
	cfg, err := a.runStore.Upsert(runcfg.Config{
		Name: "DemoApplication", Kind: runcfg.SpringBoot, MainClass: "com.example.demo.DemoApplication",
	})
	if err != nil {
		t.Fatal(err)
	}
	a.runConfig = cfg.ID
	tt.Frame()
	adapter.answer = answers(t, filepath.Join(a.project, filepath.FromSlash(demoRel)))
	// A request made while handling an event must not wait on the
	// adapter's own goroutine, which is delivering that event: jobs run
	// aside, as main's window runs them.
	a.host.Run = func(job func() func()) {
		go func() {
			if apply := job(); apply != nil {
				apply()
			}
		}()
	}
	return a, adapter, tt
}

// answers is what java-debug answers once a thread stops: one frame in the
// demo class, one local variable.
func answers(t *testing.T, framePath string) func(string, json.RawMessage) any {
	return func(command string, _ json.RawMessage) any {
		switch command {
		case "threads":
			return map[string]any{"threads": []map[string]any{{"id": 1, "name": "main"}}}
		case "stackTrace":
			return map[string]any{"stackFrames": []map[string]any{
				{"id": 100, "name": "main", "line": 7, "source": map[string]any{"path": framePath}},
			}}
		case "scopes":
			return map[string]any{"scopes": []map[string]any{{"name": "Local", "variablesReference": 5}}}
		case "variables":
			return map[string]any{"variables": []map[string]any{{"name": "i", "value": "3", "type": "int"}}}
		case "evaluate":
			return map[string]any{"result": "3"}
		}
		return nil
	}
}

// startSampleDebug begins a session on the demo configuration and waits for
// its breakpoints and configurationDone to have gone out.
func startSampleDebug(t *testing.T, a *App, adapter *fakeAdapter) {
	t.Helper()
	a.startDebug()
	eventually(t, "the launch", func() bool { return a.debug.phase == debugRunning })
	if !adapter.sent("configurationDone") {
		t.Fatal("configurationDone not sent")
	}
}

// 5.1 A click on a line's number sets a breakpoint there, which the project
// file keeps.
func TestBreakpointTogglesOnGutterClickAndPersists(t *testing.T) {
	a, _, tt := debugApp(t)
	active := a.tabs[a.active]
	active.ed.OnGutterClick(3)
	tt.Frame()
	if got := a.debug.bps.Lines(demoRel); !slices.Equal(got, []int{4}) {
		t.Fatalf("breakpoints %v", got)
	}
	// The run mark on the class's main line, and the breakpoint below it.
	if len(active.ed.GutterMarks) != 2 || active.ed.GutterMarks[1].Line != 3 {
		t.Errorf("gutter marks %+v", active.ed.GutterMarks)
	}
	on, err := dap.LoadBreakpoints(filepath.Join(a.project, ".megafactory", "breakpoints.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := on.Lines(demoRel); !slices.Equal(got, []int{4}) {
		t.Errorf("saved %v", got)
	}
	// A second click clears it.
	active.ed.OnGutterClick(3)
	tt.Frame()
	if len(a.debug.bps.Lines(demoRel)) != 0 {
		t.Error("breakpoint not cleared")
	}
}

// 5.1 Breakpoints stay on their line as text is inserted above them.
func TestBreakpointsFollowEdits(t *testing.T) {
	a, _, tt := debugApp(t)
	active := a.tabs[a.active]
	active.ed.OnGutterClick(9)
	tt.Frame()
	active.ed.Insert("// note\n")
	tt.Frame()
	active.ed.Insert("// another\n")
	tt.Frame()
	if got := a.debug.bps.Lines(demoRel); !slices.Equal(got, []int{12}) {
		t.Fatalf("breakpoints %v, want the line pushed down twice", got)
	}
}

// 5.2 Debugging a configuration hands java-debug the program, then the
// breakpoints, then configurationDone.
func TestDebugLaunchesThroughJavaDebug(t *testing.T) {
	a, adapter, _ := debugApp(t)
	a.tabs[a.active].ed.OnGutterClick(9)
	startSampleDebug(t, a, adapter)
	if !adapter.sent("initialize") || !adapter.sent("launch") {
		t.Fatalf("requests %v", adapter.got)
	}
	launch := adapter.args(t, "launch", 0)
	if launch["mainClass"] != "com.example.demo.DemoApplication" || launch["projectName"] != "demo" {
		t.Errorf("launch %v", launch)
	}
	if launch["javaExec"] != "/jdk/bin/java" || launch["cwd"] != a.project {
		t.Errorf("launch %v", launch)
	}
	if cp, _ := launch["classPaths"].([]any); len(cp) != 1 || cp[0] != "/classes" {
		t.Errorf("classPaths %v", launch["classPaths"])
	}
	file := filepath.Join(a.project, filepath.FromSlash(demoRel))
	bps := adapter.args(t, "setBreakpoints", 0)
	source, _ := bps["source"].(map[string]any)
	if source["path"] != file {
		t.Errorf("setBreakpoints %v", bps)
	}
	if lines, _ := bps["lines"].([]any); len(lines) != 1 || lines[0] != float64(10) {
		t.Errorf("lines %v", bps["lines"])
	}
	if a.debug.phase != debugRunning {
		t.Errorf("phase %d, want running", a.debug.phase)
	}
}

// 5.3 A stopped thread opens its file at the line and paints it, and 5.6
// reads the frame's variables.
func TestStopOpensFileAndReadsVariables(t *testing.T) {
	a, adapter, tt := debugApp(t)
	startSampleDebug(t, a, adapter)
	adapter.event("stopped", map[string]any{"reason": "breakpoint", "threadId": 1})
	eventually(t, "the stop", func() bool { return len(a.debug.scopes) > 0 })
	if a.debug.phase != debugStopped || len(a.debug.frames) != 1 {
		t.Fatalf("phase %d frames %v", a.debug.phase, a.debug.frames)
	}
	if a.debug.file != demoRel || a.debug.line != 7 {
		t.Errorf("stopped at %s:%d", a.debug.file, a.debug.line)
	}
	vars := a.debug.vars[5]
	if len(vars) != 1 || vars[0].Name != "i" || vars[0].Value != "3" {
		t.Errorf("variables %+v", vars)
	}
	tt.Frame()
	if got := a.tabs[a.active].ed.LineBackgrounds; len(got) != 1 || got[0].First != 6 || got[0].Last != 6 {
		t.Errorf("line backgrounds %+v", got)
	}
	if pos := a.tabs[a.active].ed.CursorPos(); pos.Line != 6 {
		t.Errorf("caret on line %d, want 6", pos.Line)
	}
}

// 5.4 The toolbar continues, steps, pauses and stops the session, and the
// stop's paint goes as it resumes.
func TestToolbarStepsAndContinues(t *testing.T) {
	a, adapter, tt := debugApp(t)
	startSampleDebug(t, a, adapter)
	// A thread starting is enough to interrupt, before any breakpoint.
	adapter.event("thread", map[string]any{"reason": "started", "threadId": 1})
	eventually(t, "the thread", func() bool { return a.debug.thread == 1 })
	tt.Frame()
	click(t, tt, "Pause")
	if args := adapter.args(t, "pause", 0); args["threadId"] != float64(1) {
		t.Errorf("pause %v", args)
	}
	stop := func() {
		adapter.event("stopped", map[string]any{"reason": "breakpoint", "threadId": 1})
		eventually(t, "the stop", func() bool { return len(a.debug.frames) == 1 })
		tt.Frame()
	}
	stop()
	click(t, tt, "Step Over")
	if args := adapter.args(t, "next", 0); args["threadId"] != float64(1) {
		t.Errorf("next %v", args)
	}
	if a.debug.phase != debugRunning || len(a.debug.frames) != 0 {
		t.Fatalf("after step: phase %d frames %v", a.debug.phase, a.debug.frames)
	}
	tt.Frame()
	if got := a.tabs[a.active].ed.LineBackgrounds; len(got) != 0 {
		t.Errorf("stop line still painted %+v", got)
	}
	for _, step := range []struct{ button, command string }{
		{"Continue", "continue"},
		{"Step Into", "stepIn"},
		{"Step Out", "stepOut"},
	} {
		stop()
		click(t, tt, step.button)
		if !adapter.sent(step.command) {
			t.Errorf("%s not sent", step.button)
		}
	}
	stop()
	click(t, tt, "Stop Debugging")
	if !adapter.sent("disconnect") || a.debug.phase != debugOff || a.debug.conn != nil {
		t.Errorf("stop: requests %v phase %d", adapter.got, a.debug.phase)
	}
}

// 5.7 A watch expression is evaluated where the program stopped.
func TestWatchEvaluatesAtTheFrame(t *testing.T) {
	a, adapter, tt := debugApp(t)
	startSampleDebug(t, a, adapter)
	adapter.event("stopped", map[string]any{"reason": "breakpoint", "threadId": 1})
	eventually(t, "the stop", func() bool { return len(a.debug.scopes) > 0 })
	tt.Frame()
	a.debug.input = "i + 1"
	tt.Frame()
	click(t, tt, "Add Watch")
	eventually(t, "the watch", func() bool { return len(a.debug.watch) == 1 && a.debug.watch[0].value != "" })
	_ = tt
	if w := a.debug.watch[0]; w.expr != "i + 1" || w.value != "3" {
		t.Errorf("watch %+v", w)
	}
	evaluate := adapter.args(t, "evaluate", 0)
	if evaluate["context"] != "watch" || evaluate["frameId"] != float64(100) {
		t.Errorf("evaluate %v", evaluate)
	}
	if !tt.HasText("i + 1") {
		t.Errorf("watch not shown: %v", tt.Texts())
	}
	// Editing the expression re-reads it, Enter being what the row's
	// input submits.
	a.debug.watch[0].expr = "a + 1"
	a.debugEvaluateAll()
	eventually(t, "the edited watch", func() bool { return len(adapter.requests("evaluate")) == 2 })
	if evaluate := adapter.args(t, "evaluate", 1); evaluate["expression"] != "a + 1" {
		t.Errorf("evaluate %v", evaluate)
	}
	tt.Frame()
	click(t, tt, "Remove watch")
	if len(a.debug.watch) != 0 {
		t.Errorf("watch not removed: %+v", a.debug.watch)
	}
}

// 5.8 The console shows what the program wrote and evaluates at the frame.
func TestDebugConsoleOutputAndEvaluate(t *testing.T) {
	a, adapter, tt := debugApp(t)
	startSampleDebug(t, a, adapter)
	adapter.event("output", map[string]any{"category": "stdout", "output": "hello\n"})
	eventually(t, "the output", func() bool { return len(a.debug.lines) > 0 })
	if a.debug.lines[0].text != "hello" || a.debug.lines[0].level != debugOutput {
		t.Fatalf("console %+v", a.debug.lines)
	}
	adapter.event("stopped", map[string]any{"reason": "breakpoint", "threadId": 1})
	eventually(t, "the stop", func() bool { return len(a.debug.scopes) > 0 })
	tt.Frame()
	a.debug.input = "i * 2"
	tt.Frame()
	click(t, tt, "Evaluate")
	eventually(t, "the answer", func() bool { return len(a.debug.lines) >= 3 })
	last := a.debug.lines[len(a.debug.lines)-1]
	if last.text != "3" || last.level != debugOutput {
		t.Errorf("answer %+v", a.debug.lines)
	}
	if evaluate := adapter.args(t, "evaluate", 0); evaluate["context"] != "repl" || evaluate["frameId"] != float64(100) {
		t.Errorf("evaluate %v", evaluate)
	}
	if !tt.HasText("hello") {
		t.Errorf("console not shown: %v", tt.Texts())
	}
}

// 5.5 The call stack lists the frames, and choosing one moves the variables
// and the console's frame to it.
func TestCallStackChoosesFrame(t *testing.T) {
	a, adapter, tt := debugApp(t)
	adapter.answer = func(command string, _ json.RawMessage) any {
		switch command {
		case "threads":
			return map[string]any{"threads": []map[string]any{{"id": 1, "name": "main"}}}
		case "stackTrace":
			return map[string]any{"stackFrames": []map[string]any{
				{"id": 100, "name": "hello", "line": 14, "source": map[string]any{"path": filepath.Join(a.project, "src/main/java/com/example/demo/HelloController.java")}},
				{"id": 200, "name": "runDemo", "line": 11, "source": map[string]any{"path": filepath.Join(a.project, filepath.FromSlash(demoRel))}},
			}}
		case "scopes":
			return map[string]any{"scopes": []map[string]any{{"name": "Local", "variablesReference": 5}}}
		case "evaluate":
			return map[string]any{"result": "2"}
		}
		return nil
	}
	startSampleDebug(t, a, adapter)
	adapter.event("stopped", map[string]any{"reason": "breakpoint", "threadId": 1})
	eventually(t, "the stop", func() bool { return len(a.debug.frames) == 2 })
	if a.debug.file != "src/main/java/com/example/demo/HelloController.java" {
		t.Fatalf("opened %q", a.debug.file)
	}
	tt.Frame()
	tt.Frame()
	click(t, tt, "runDemo")
	if a.debug.frame != 1 || a.debug.file != demoRel || a.debug.line != 11 {
		t.Fatalf("frame %d at %s:%d", a.debug.frame, a.debug.file, a.debug.line)
	}
	eventually(t, "the frame's scopes", func() bool { return len(a.debug.scopes) > 0 })
	tt.Frame()
	a.debug.input = "x"
	tt.Frame()
	click(t, tt, "Evaluate")
	if evaluate := adapter.args(t, "evaluate", 0); evaluate["frameId"] != float64(200) {
		t.Errorf("evaluate %v, want the chosen frame", evaluate)
	}
}

// 5.2 The adapter ending the program ends the session, keeping its frames.
func TestTerminatedEndsSession(t *testing.T) {
	a, adapter, tt := debugApp(t)
	startSampleDebug(t, a, adapter)
	adapter.event("terminated", nil)
	eventually(t, "the end", func() bool { return a.debug.phase == debugEnded })
	if a.debug.conn != nil {
		t.Error("connection kept")
	}
	tt.Frame()
	if !tt.HasText("The program ended") {
		t.Errorf("panel does not say so: %v", tt.Texts())
	}
}

// A Maven or Shell configuration has no Java program to debug.
func TestShellConfigurationCannotBeDebugged(t *testing.T) {
	a, adapter, _ := debugApp(t)
	cfg, _ := a.runStore.Upsert(runcfg.Config{Name: "Say hi", Kind: runcfg.Shell, Command: "echo hi"})
	a.runConfig = cfg.ID
	var shown string
	a.host.ShowError = func(title, msg string) { shown = title + ": " + msg }
	a.startDebug()
	if shown == "" {
		t.Fatal("no error shown")
	}
	if a.debug.phase != debugOff || adapter.sent("initialize") {
		t.Errorf("a session started: phase %d", a.debug.phase)
	}
}
