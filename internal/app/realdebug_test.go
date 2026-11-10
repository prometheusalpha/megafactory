package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"megafactory/internal/dap"
	"megafactory/internal/javalang"
	"megafactory/internal/jdk"
	"megafactory/internal/jdtls"
	"megafactory/internal/runcfg"
	"megafactory/internal/setup"
)

// realResolver is the port runcfg's debugger reads, answered by the real
// language server rather than by a fake.
type realResolver struct {
	project string
	srv     *javalang.Session
	java    string
}

func (r realResolver) Java(string) (string, error) { return r.java, nil }

func (r realResolver) Classpath(mainClass string) (string, []string, []string, error) {
	classes, err := r.srv.ResolveMainClasses()
	if err != nil {
		return "", nil, nil, err
	}
	project := ""
	for _, c := range classes {
		if c.MainClass == mainClass {
			project = c.ProjectName
			break
		}
	}
	modules, classpath, err := r.srv.ResolveClasspath(mainClass, project)
	return project, modules, classpath, err
}

func (r realResolver) Wrapper(string) ([]string, error) { return nil, nil }
func (r realResolver) ProjectRoot() string              { return r.project }
func (r realResolver) Build() runcfg.BuildSystem        { return runcfg.BuildMaven }

// TestRealDebugSession drives java-debug this machine has installed: it
// resolves a Maven project's classpath through jdtls, starts an adapter
// session, sets a breakpoint, and must see the program stop on it with its
// local variable readable. Run with MEGAFACTORY_JDTLS=1.
func TestRealDebugSession(t *testing.T) {
	if os.Getenv("MEGAFACTORY_JDTLS") == "" {
		t.Skip("MEGAFACTORY_JDTLS not set")
	}
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(config, "megafactory", "servers")
	for _, art := range []setup.Artifact{setup.JDTLS, setup.JavaDebug} {
		if _, err := os.Stat(art.Dir(real)); err != nil {
			t.Skipf("%s is not installed in %s: run the app's setup first: %v", art.Name, real, err)
		}
	}
	if err := os.RemoveAll(serversDir()); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(real, serversDir()); err != nil {
		t.Fatal(err)
	}

	// jdtls names files by their real path, which a temp folder's link hides.
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	javaDir := filepath.Join(project, "src", "main", "java", "demo")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	const source = `package demo;

public class App {
    public static void main(String[] args) {
        int a = 1;
        int b = 2;
        System.out.println(a + b);
    }
}
`
	sourcePath := filepath.Join(javaDir, "App.java")
	if err := os.WriteFile(sourcePath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "pom.xml"), []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>demo</groupId><artifactId>app</artifactId><version>1</version>
  <properties><maven.compiler.release>21</maven.compiler.release></properties>
  <build><plugins><plugin>
    <groupId>org.apache.maven.plugins</groupId>
    <artifactId>maven-compiler-plugin</artifactId>
    <version>3.13.0</version>
    <configuration><release>21</release></configuration>
  </plugin></plugins></build>
</project>
`), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	finder := jdk.System
	finder.Roots = append(finder.Roots, serversDir())
	home, err := finder.Find(ctx)
	if err != nil {
		t.Fatalf("no JDK 25 for jdtls: %v", err)
	}
	// The debugger runs the class from the folder Maven's build writes, so
	// compile it there with the JDK the servers came with.
	classes := filepath.Join(project, "target", "classes")
	if err := os.MkdirAll(classes, 0o755); err != nil {
		t.Fatal(err)
	}
	build := exec.Command(filepath.Join(home.Home, "bin", "javac"), "-d", classes, "-source", "21", "-target", "21", sourcePath)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("javac: %v: %s", err, out)
	}

	sess := javalang.New()
	statuses := make(chan javalang.Status, 64)
	sess.SetHandlers(func(string, uint64, []javalang.Diagnostic) {},
		func(s javalang.Status, msg string) {
			if s == javalang.StatusError {
				t.Logf("jdtls: %s", msg)
			}
			select {
			case statuses <- s:
			default:
			}
		})
	srv, err := jdtls.Start(ctx, jdtls.Config{
		JDK: home, Runtimes: finder.Installed(),
		Servers:    serversDir(),
		Project:    project,
		Workspaces: t.TempDir(),
		Handler:    sess,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	sess.Attach(srv.Conn, srv.Capabilities)

	waitFor := func(what string, ok func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Minute)
		for !ok() {
			if time.Now().After(deadline) {
				t.Fatalf("%s did not happen within 3m", what)
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	ready := false
	waitFor("the project to be ready", func() bool {
		for !ready {
			select {
			case s := <-statuses:
				ready = s == javalang.StatusReady
			case <-time.After(50 * time.Millisecond):
				return ready
			}
		}
		return ready
	})

	mainClass := "demo.App"
	spec, err := runcfg.DebugSpec(runcfg.Config{ID: "1", Name: "App", Kind: runcfg.Application, MainClass: mainClass},
		realResolver{project: project, srv: sess, java: home.Java()})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.ClassPaths) == 0 || spec.ProjectName == "" {
		t.Fatalf("classpath %v project %q", spec.ClassPaths, spec.ProjectName)
	}

	port, err := sess.StartDebugSession()
	if err != nil {
		t.Fatalf("startDebugSession: %v", err)
	}
	events := make(chan dap.Event, 64)
	conn, err := dap.Dial(ctx, "127.0.0.1:"+strconv.Itoa(port), func(ev dap.Event) {
		select {
		case events <- ev:
		default:
		}
	})
	if err != nil {
		t.Fatalf("connect to the adapter on %d: %v", port, err)
	}
	defer conn.Close()

	// The app's own handshake, so that what it sends is what java-debug
	// is judged by.
	cfg := runcfg.Config{ID: "1", Name: "App", Kind: runcfg.Application, MainClass: mainClass}
	if err := debugHandshake(conn, cfg, spec); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	const breakLine = 6
	if bps, err := conn.SetBreakpoints(sourcePath, []int{breakLine}); err != nil {
		t.Fatalf("setBreakpoints: %v", err)
	} else {
		t.Logf("breakpoints: %+v", bps)
	}
	if err := conn.ConfigurationDone(); err != nil {
		t.Fatalf("configurationDone: %v", err)
	}

	var stop dap.StoppedEvent
	waitFor("the breakpoint to be hit", func() bool {
		select {
		case ev := <-events:
			switch ev.Event {
			case "stopped":
				dap.Decode(ev.Body, &stop)
				return true
			case "output":
				var o dap.OutputEvent
				dap.Decode(ev.Body, &o)
				t.Logf("output: %q", o.Output)
			case "terminated", "exited":
				t.Fatalf("the program ended before the breakpoint: %+v", ev)
			}
			return false
		case <-time.After(50 * time.Millisecond):
			return false
		}
	})

	frames, err := conn.StackTrace(stop.ThreadID)
	if err != nil {
		t.Fatalf("stackTrace: %v", err)
	}
	if len(frames) == 0 || frames[0].Line != breakLine {
		t.Fatalf("frames %+v, want a stop on line %d", frames, breakLine)
	}
	scopes, err := conn.Scopes(frames[0].ID)
	if err != nil {
		t.Fatalf("scopes: %v", err)
	}
	var names []string
	values := map[string]string{}
	for _, s := range scopes {
		vars, err := conn.Variables(s.VariablesReference)
		if err != nil {
			t.Fatalf("variables of %s: %v", s.Name, err)
		}
		for _, v := range vars {
			names = append(names, v.Name)
			values[v.Name] = v.Value
		}
	}
	if values["a"] != "1" {
		t.Fatalf("variables %v, want a=1", values)
	}
	evaluated, err := conn.Evaluate("a + 1", frames[0].ID, "repl")
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if evaluated.Value != "2" {
		t.Errorf("a + 1 = %q, want 2", evaluated.Value)
	}
	if err := conn.Continue(stop.ThreadID); err != nil {
		t.Fatalf("continue: %v", err)
	}
	waitFor("the program to end", func() bool {
		select {
		case ev := <-events:
			return ev.Event == "terminated" || ev.Event == "exited"
		case <-time.After(50 * time.Millisecond):
			return false
		}
	})
	if err := conn.Disconnect(true); err != nil && !strings.Contains(err.Error(), "closed") {
		t.Logf("disconnect: %v", err)
	}
}
