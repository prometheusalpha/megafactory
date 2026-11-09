package jdtls

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui/editor"

	"megafactory/internal/javalang"
	"megafactory/internal/jdk"
	"megafactory/internal/setup"
)

// waitUntil polls ok for a bounded time, which is how a real server's
// asynchronous answers are awaited without assuming an order.
func waitUntil(t *testing.T, what string, timeout time.Duration, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("%s did not happen within %s", what, timeout)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Run with MEGAFACTORY_JDTLS=1 to drive a javalang.Session against the real
// jdtls this machine has installed: the session must bring a Maven project
// of Java 21 up, read its error diagnostics against the editor's own text,
// and answer completion, hover and definition in that text's byte offsets.
func TestRealJavalangSession(t *testing.T) {
	if os.Getenv("MEGAFACTORY_JDTLS") == "" {
		t.Skip("MEGAFACTORY_JDTLS not set")
	}
	config, err := os.UserConfigDir()
	if err != nil {
		t.Fatal(err)
	}
	servers := filepath.Join(config, "megafactory", "servers")
	for _, a := range []setup.Artifact{setup.JDTLS, setup.JavaDebug} {
		if _, err := os.Stat(a.Dir(servers)); err != nil {
			t.Skipf("%s is not installed in %s: run the app's setup first: %v", a.Name, servers, err)
		}
	}

	// The JDK installed for jdtls lives beside the servers, not in the
	// machine's usual Java folders, so the finder looks there too. The
	// runtime of the project's level 21 is the machine's own.
	finder := jdk.System
	finder.Roots = append(append([]string{}, finder.Roots...), servers)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	serverJDK, err := finder.Find(ctx)
	if err != nil {
		t.Fatal(err)
	}
	runtimes := finder.Installed()
	has21 := false
	for _, j := range runtimes {
		if j.Major == 21 {
			has21 = true
		}
	}
	if !has21 {
		t.Fatalf("no JDK 21 among the runtimes %+v, which a project of level 21 needs", runtimes)
	}
	t.Logf("jdtls on JDK %s, runtimes %+v", serverJDK.Version, runtimes)

	// jdtls names files by their real path, which a temp folder's link hides.
	project, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	javaDir := filepath.Join(project, "src", "main", "java", "com", "example")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "pom.xml"), []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId><artifactId>javalang-check</artifactId><version>1</version>
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
	const source = `package com.example;

public class A {
    public static void main(String[] args) {
        System.out.println("hi");
        int x = "bad";
        Helper h = null;
        System.out.println(h);
    }
}

class Helper {}
`
	javaPath := filepath.Join(javaDir, "A.java")
	if err := os.WriteFile(javaPath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	// Status and diagnostics arrive on the connection's reading goroutine,
	// so the handlers hand them over and never wait for the test.
	type statusEvent struct {
		status javalang.Status
		msg    string
	}
	type diagEvent struct {
		path string
		ds   []javalang.Diagnostic
	}
	statuses := make(chan statusEvent, 64)
	diags := make(chan diagEvent, 256)
	session := javalang.New()
	session.SetHandlers(
		func(path string, version uint64, ds []javalang.Diagnostic) {
			select {
			case diags <- diagEvent{path, ds}:
			default:
			}
		},
		func(status javalang.Status, msg string) {
			select {
			case statuses <- statusEvent{status, msg}:
			default:
			}
		},
	)

	s, err := Start(ctx, Config{
		JDK: serverJDK, Runtimes: runtimes, Handler: session,
		Servers:    servers,
		Project:    project,
		Workspaces: t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	session.Attach(s.Conn, s.Capabilities)

	snap := editor.NewBuffer(source).Snapshot()
	if err := session.Open(javaPath, "java", snap); err != nil {
		t.Fatal(err)
	}

	var (
		lastStatus  javalang.Status
		lastMessage string
		ready       bool
	)
	waitUntil(t, "the project to be ready", 3*time.Minute, func() bool {
		for !ready {
			select {
			case ev := <-statuses:
				lastStatus, lastMessage = ev.status, ev.msg
				if ev.status == javalang.StatusError {
					t.Fatalf("jdtls reports an error: %s", ev.msg)
				}
				if ev.status == javalang.StatusReady {
					ready = true
				}
			default:
				return ready
			}
		}
		return ready
	})
	t.Logf("project ready (last status %d %q)", lastStatus, lastMessage)

	badLine := snap.LineOf(strings.Index(source, `int x = "bad";`))
	var bad javalang.Diagnostic
	found := false
	waitUntil(t, "an error diagnostic on the int x line", 3*time.Minute, func() bool {
		for !found {
			select {
			case ev := <-diags:
				if filepath.Clean(ev.path) != filepath.Clean(javaPath) {
					continue
				}
				for _, d := range ev.ds {
					msg := strings.ToLower(d.Message)
					mismatch := strings.Contains(msg, "type mismatch") ||
						strings.Contains(msg, "string") && strings.Contains(msg, "int")
					if d.Severity == 0 && mismatch && snap.LineOf(d.Start) == badLine {
						bad, found = d, true
						break
					}
				}
			default:
				return found
			}
		}
		return found
	})
	t.Logf("diagnostic at line %d: %s", badLine, bad.Message)

	// Complete after the dot the source's first System. ends at.
	afterSystem := strings.Index(source, "System.") + len("System.")
	var items []javalang.Item
	waitUntil(t, "completion after System.", 3*time.Minute, func() bool {
		_, got, err := session.Complete(javaPath, afterSystem)
		if err != nil {
			return false
		}
		items = got
		for _, it := range got {
			if it.Label == "out" || it.Insert == "out" || strings.HasPrefix(it.Insert, "out") {
				return true
			}
		}
		return false
	})
	t.Logf("%d completion items after System.", len(items))

	// Hover over the System of the first println.
	systemAt := strings.Index(source, "System")
	waitUntil(t, "hover text for System", 3*time.Minute, func() bool {
		_, _, text, err := session.Hover(javaPath, systemAt)
		return err == nil && strings.TrimSpace(text) != ""
	})

	// Definition of the Helper type at its use goes to the class at the
	// bottom of the same file.
	helperLine := snap.LineOf(strings.Index(source, "class Helper"))
	helperAt := strings.Index(source, "Helper h")
	var defined javalang.Location
	found = false
	waitUntil(t, "definition of Helper at its use", 3*time.Minute, func() bool {
		locs, err := session.Definition(javaPath, helperAt)
		if err != nil {
			return false
		}
		for _, l := range locs {
			if filepath.Clean(l.Path) == filepath.Clean(javaPath) && snap.LineOf(l.Start) == helperLine {
				defined, found = l, true
				return true
			}
		}
		return false
	})
	t.Logf("Helper defined at %s:%d", filepath.Base(defined.Path), helperLine)

	if err := session.Save(javaPath); err != nil {
		t.Errorf("save: %v", err)
	}
	if err := session.Close(javaPath); err != nil {
		t.Errorf("close: %v", err)
	}
}
