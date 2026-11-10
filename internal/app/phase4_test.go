package app

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"megafactory/internal/javalang"
	"megafactory/internal/runcfg"
	"megafactory/internal/runner"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
)

// fakeTerm is a terminal a test shows instead of libghostty's.
type fakeTerm struct{ closed bool }

func (f *fakeTerm) View(c *ui.Context) ui.Element {
	return ui.Box(c).Fill().Label("Fake Terminal")
}
func (f *fakeTerm) Title() string { return "" }
func (f *fakeTerm) Close() error {
	f.closed = true
	return nil
}

// fakeProc delivers lines without starting a process. It returns no
// *runner.Process, which the app treats as an already-finished run.
type fakeProc struct {
	spec  runner.Spec
	lines []runner.Line
}

func (p *fakeProc) start(spec runner.Spec, onLine func(runner.Line), onExit func(error)) (*runner.Process, error) {
	p.spec = spec
	for _, l := range p.lines {
		onLine(l)
	}
	onExit(nil)
	return nil, nil
}

func phase4App(t *testing.T) (*App, *ui.Tester, *fakeProc) {
	t.Helper()
	a := newApp(defaultState)
	a.branch = "dev"
	a.host.NewTerm = func(terminal.Options) (TermHandle, error) { return &fakeTerm{}, nil }
	a.host.ShowError = func(string, string) {}
	proc := &fakeProc{}
	a.host.StartProc = proc.start
	a.OpenProject(sampleProject(t))
	return a, ui.NewTester(a.View, 1280, 800), proc
}

// 4.1 The Terminal page starts one terminal whose cwd is the project root.
func TestTerminalStartsAtProjectRoot(t *testing.T) {
	a := newApp(defaultState)
	var dirs []string
	a.host.NewTerm = func(o terminal.Options) (TermHandle, error) {
		dirs = append(dirs, o.Dir)
		return &fakeTerm{}, nil
	}
	a.OpenProject(sampleProject(t))
	a.bottomTab = 2
	tt := ui.NewTester(a.View, 1280, 800)
	tt.Frame()
	if len(a.terms) != 1 {
		t.Fatalf("terminals %d", len(a.terms))
	}
	if len(dirs) != 1 || dirs[0] != a.project {
		t.Errorf("terminal cwd %v, want %q", dirs, a.project)
	}
	if !tt.HasText("Fake Terminal") {
		t.Errorf("terminal view not shown: %v", tt.Texts())
	}
}

// 4.2 Terminal tabs open, close, and rename.
func TestTerminalTabsOpenCloseRename(t *testing.T) {
	a, tt, _ := phase4App(t)
	a.bottomTab = 2
	tt.Frame()
	click(t, tt, "New Terminal")
	tt.Frame()
	if len(a.terms) != 2 {
		t.Fatalf("terminals %d after add", len(a.terms))
	}
	click(t, tt, "Close Terminal 1")
	tt.Frame()
	if len(a.terms) != 1 {
		t.Fatalf("terminals %d after close", len(a.terms))
	}
	menu(t, tt, "Terminal 2", "Rename…")
	tt.Type("Build shell")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if len(a.terms) != 1 || !a.terms[0].renamed || a.terms[0].name != "Build shell" {
		t.Errorf("rename: %+v", a.terms)
	}
}

// 4.3 A config saved through the dialog lands in .megafactory/run.json.
func TestRunConfigDialogPersists(t *testing.T) {
	a, tt, _ := phase4App(t)
	a.editConfigurations()
	tt.Frame()
	click(t, tt, "Add", "Shell")
	tt.Frame()
	if !a.runEdit.editing {
		t.Fatal("the form did not open")
	}
	a.runEdit.draft.Name = "Say hi"
	a.runEdit.draft.Command = "echo hi"
	click(t, tt, "Save")
	tt.Frame()
	if len(a.runStore.All()) != 1 {
		t.Fatalf("configs %v", a.runStore.All())
	}
	data, err := os.ReadFile(a.runPath())
	if err != nil {
		t.Fatal(err)
	}
	var f struct {
		Configs []runcfg.Config `json:"configs"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		t.Fatal(err)
	}
	if len(f.Configs) != 1 || f.Configs[0].Name != "Say hi" || f.Configs[0].Command != "echo hi" {
		t.Errorf("run.json %v", f.Configs)
	}
	back, err := runcfg.Load(a.runPath())
	if err != nil || len(back.All()) != 1 {
		t.Errorf("reload: %v %v", back.All(), err)
	}
}

// 4.7 Duplicate and Delete change the store and the file.
func TestRunConfigDuplicateAndDelete(t *testing.T) {
	a, tt, _ := phase4App(t)
	cfg, _ := a.runStore.Upsert(runcfg.Config{Name: "App", Kind: runcfg.Shell, Command: "echo a"})
	a.runConfig = cfg.ID
	a.saveRunConfigs()
	a.runEdit = &runEditState{open: true, sel: cfg.ID}
	tt.Frame()
	click(t, tt, "Duplicate")
	tt.Frame()
	if len(a.runStore.All()) != 2 {
		t.Fatalf("configs after duplicate %v", a.runStore.All())
	}
	var copyID string
	for _, c := range a.runStore.All() {
		if c.Name == "App (copy)" {
			copyID = c.ID
		}
	}
	if copyID == "" {
		t.Fatal("no copied config")
	}
	a.runEdit.sel = copyID
	tt.Frame()
	click(t, tt, "Delete")
	tt.Frame()
	if len(a.runStore.All()) != 1 {
		t.Errorf("configs after delete %v", a.runStore.All())
	}
	data, _ := os.ReadFile(a.runPath())
	if strings.Contains(string(data), "App (copy)") {
		t.Error("deleted config still in run.json")
	}
}

// 4.10 The console keeps stdout and stderr apart, ends the run, and a
// File.java:123 link opens that file at the right line. Rerun replaces it.
func TestRunConsoleStreamsAndStackLink(t *testing.T) {
	a, tt, proc := phase4App(t)
	cfg, _ := a.runStore.Upsert(runcfg.Config{Name: "Demo", Kind: runcfg.Shell, Command: "echo demo"})
	a.runConfig = cfg.ID
	demoRel := "src/main/java/com/example/demo/DemoApplication.java"
	proc.lines = []runner.Line{
		{Text: "running", Stream: runner.Stdout},
		{Text: "at com.example.demo.DemoApplication.main(DemoApplication.java:12)", Stream: runner.Stderr},
	}
	click(t, tt, "Run")
	tt.Frame()
	if a.runConsole == nil || len(a.runConsole.lines) != 2 {
		t.Fatalf("console %+v", a.runConsole)
	}
	if a.runConsole.lines[0].stderr || !a.runConsole.lines[1].stderr {
		t.Errorf("streams mixed: %+v", a.runConsole.lines)
	}
	if !a.runConsole.done {
		t.Error("the run did not end")
	}
	if !tt.HasText("running") {
		t.Errorf("console text missing: %v", tt.Texts())
	}
	click(t, tt, "DemoApplication.java:12")
	tt.Frame()
	if len(a.tabs) != 1 || a.tabs[0].path != demoRel {
		t.Fatalf("stack link did not open the file: %v", a.tabs)
	}
	if pos := a.tabs[0].ed.CursorPos(); pos.Line != 11 {
		t.Errorf("caret on line %d, want 11", pos.Line)
	}
	click(t, tt, "Rerun")
	tt.Frame()
	if a.runGen != 2 || len(a.runConsole.lines) != 2 {
		t.Errorf("rerun: gen %d lines %v", a.runGen, a.runConsole.lines)
	}
}

// The dropdown is the way into Edit Configurations as well as the
// configurations themselves, and says so when there are none.
func TestRunConfigDropdownOpensTheDialog(t *testing.T) {
	a, tt, _ := phase4App(t)
	tt.Frame()
	click(t, tt, "Run Configuration")
	tt.Frame()
	if !tt.HasText("No run configurations") || !tt.HasText("Edit Configurations…") {
		t.Fatalf("popup shows %v", tt.Texts())
	}
	click(t, tt, "Edit Configurations…")
	tt.Frame()
	if a.runEdit == nil || !a.runEdit.open {
		t.Fatal("the dialog did not open")
	}
	if a.runConfig != "" {
		t.Errorf("selection became %q", a.runConfig)
	}
	if !tt.HasText("Run Configurations") {
		t.Error("the dialog is not shown")
	}
}

// Choosing it leaves the configuration the toolbar had.
func TestRunConfigDropdownKeepsTheChoice(t *testing.T) {
	a, tt, _ := phase4App(t)
	cfg, err := a.runStore.Upsert(runcfg.Config{Name: "Demo", Kind: runcfg.SpringBoot, MainClass: "com.example.demo.DemoApplication"})
	if err != nil {
		t.Fatal(err)
	}
	a.selectRunConfig(cfg.ID)
	tt.Frame()
	click(t, tt, "Run Configuration")
	tt.Frame()
	click(t, tt, "Edit Configurations…")
	tt.Frame()
	if a.runConfig != cfg.ID {
		t.Errorf("selection became %q, want %q", a.runConfig, cfg.ID)
	}
	if a.runStore.Selected() != cfg.ID {
		t.Errorf("saved selection %q", a.runStore.Selected())
	}
}

// 4.8 The app's resolver asks java-debug for the classpath with the
// project name resolveMainClass returned.
func TestRunConfigResolvesClasspathThroughJavaDebug(t *testing.T) {
	a, _, _ := phase4App(t)
	f := &fakeJava{
		mainClasses: []javalang.MainClass{{MainClass: "com.example.demo.DemoApplication", ProjectName: "demo"}},
		classPaths:  [][]string{{"/mods"}, {"/classes", "/lib/a.jar"}},
	}
	r := appResolver{app: a, project: a.project, srv: f}
	project, mods, classes, err := r.Classpath("com.example.demo.DemoApplication")
	if err != nil {
		t.Fatal(err)
	}
	if f.resolved != "com.example.demo.DemoApplication/demo" || project != "demo" {
		t.Errorf("classpath asked for %q, project %q", f.resolved, project)
	}
	if len(mods) != 1 || mods[0] != "/mods" || len(classes) != 2 {
		t.Errorf("mods %v classes %v", mods, classes)
	}
}

// 4.9 A runnable class gets a gutter mark and a Run action.
func TestRunnableClassGetsGutterRunAction(t *testing.T) {
	a, tt, _ := phase4App(t)
	click(t, tt, "src", "main", "java", "com", "example", "demo", "DemoApplication.java")
	tt.Frame()
	active := a.tabs[a.active]
	if active.ed == nil || len(active.ed.GutterMarks) != 1 {
		t.Fatalf("gutter marks %v", active.ed.GutterMarks)
	}
	if active.ed.GutterMarks[0].Line != 5 {
		t.Errorf("mark on line %d, want 5", active.ed.GutterMarks[0].Line)
	}
	if active.ed.OnGutterClick == nil || active.ed.ContextMenu == nil {
		t.Fatal("no run action wired")
	}
	active.ed.OnGutterClick(active.ed.GutterMarks[0].Line)
	if len(a.runStore.All()) != 1 {
		t.Fatalf("no config made: %v", a.runStore.All())
	}
	got := a.runStore.All()[0]
	if got.Kind != runcfg.SpringBoot || got.MainClass != "com.example.demo.DemoApplication" {
		t.Errorf("config %+v", got)
	}
}

// A line or exit delivered after a Stop or a replacement must not touch the
// live console. The fake keeps the callbacks so the test can fire them late.
type lateProc struct {
	line  func(runner.Line)
	exit  func(error)
	lines []runner.Line
}

func (p *lateProc) start(_ runner.Spec, onLine func(runner.Line), onExit func(error)) (*runner.Process, error) {
	p.line, p.exit = onLine, onExit
	return nil, nil
}

func TestConsoleDropsLateLinesAfterStop(t *testing.T) {
	a, _, _ := phase4App(t)
	cfg, _ := a.runStore.Upsert(runcfg.Config{Name: "Demo", Kind: runcfg.Shell, Command: "echo demo"})
	a.runConfig = cfg.ID
	late := &lateProc{}
	a.host.StartProc = late.start
	a.startRun(cfg)
	if a.runConsole == nil || late.line == nil {
		t.Fatal("run did not start")
	}
	gen := a.runGen
	a.stopRun()
	if a.runGen == gen {
		t.Fatal("Stop did not invalidate the generation")
	}
	late.line(runner.Line{Text: "too late", Stream: runner.Stderr})
	late.exit(nil)
	if len(a.runConsole.lines) != 0 || a.runConsole.done {
		t.Errorf("late output mutated the console: %+v", a.runConsole)
	}
}

func TestProcessStopAfterExitDoesNotSignalReapedGroup(t *testing.T) {
	done := make(chan struct{})
	p, err := runner.Start(runner.Spec{Argv: []string{"/bin/sh", "-c", "true"}},
		func(runner.Line) {}, func(error) { close(done) })
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the process never ended")
	}
	// A Rerun of a completed run calls Stop again. It must be a no-op,
	// not a SIGKILL to a reused process-group id.
	p.Stop()
	p.Stop()
}

func TestSelectingRunConfigPersistsIt(t *testing.T) {
	a, _, _ := phase4App(t)
	one, _ := a.runStore.Upsert(runcfg.Config{Name: "One", Kind: runcfg.Shell, Command: "echo 1"})
	two, _ := a.runStore.Upsert(runcfg.Config{Name: "Two", Kind: runcfg.Shell, Command: "echo 2"})
	a.selectRunConfig(one.ID)
	a.selectRunConfig(two.ID)
	if a.runConfig != two.ID {
		t.Fatalf("selection %q", a.runConfig)
	}
	back, err := runcfg.Load(a.runPath())
	if err != nil {
		t.Fatal(err)
	}
	if back.Selected() != two.ID {
		t.Errorf("run.json selected %q, want %q", back.Selected(), two.ID)
	}
}

func TestDeletingSelectedConfigReselects(t *testing.T) {
	a, tt, _ := phase4App(t)
	one, _ := a.runStore.Upsert(runcfg.Config{Name: "One", Kind: runcfg.Shell, Command: "echo 1"})
	two, _ := a.runStore.Upsert(runcfg.Config{Name: "Two", Kind: runcfg.Shell, Command: "echo 2"})
	a.runConfig = one.ID
	a.runStore.Select(one.ID)
	a.saveRunConfigs()
	a.runEdit = &runEditState{open: true, sel: one.ID}
	tt.Frame()
	click(t, tt, "Delete")
	tt.Frame()
	if a.runConfig == one.ID {
		t.Errorf("selection still points at the deleted config")
	}
	back, _ := runcfg.Load(a.runPath())
	if back.Selected() != two.ID {
		t.Errorf("run.json selected %q, want the survivor %q", back.Selected(), two.ID)
	}
}

func TestLateTerminalSpawnDoesNotJoinNewProject(t *testing.T) {
	a, _, _ := phase4App(t)
	var apply func()
	a.host.Run = func(job func() func()) { apply = job() }
	a.host.NewTerm = func(terminal.Options) (TermHandle, error) { return &fakeTerm{}, nil }
	a.newTerm()
	// The project switches while the shell is starting.
	a.project = "/somewhere/else"
	a.closeTerms()
	apply()
	if len(a.terms) != 0 {
		t.Errorf("a terminal of the old project joined the new one: %v", a.terms)
	}
}
