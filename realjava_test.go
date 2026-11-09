package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"megafactory/internal/javalang"
	"megafactory/internal/setup"

	"github.com/egoist/mygo/ui/editor"
)

// TestRealLanguageServerStart drives the app's own starter against the
// jdtls this machine has installed: a Maven project must come up ready,
// and a type mismatch in it must reach the starter's diagnostics handler.
// Run with MEGAFACTORY_JDTLS=1.
func TestRealLanguageServerStart(t *testing.T) {
	if os.Getenv("MEGAFACTORY_JDTLS") == "" {
		t.Skip("MEGAFACTORY_JDTLS not set")
	}
	// TestMain keeps dataDir in a temp folder; the servers are the
	// machine's, so link them in rather than download them again.
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
	javaDir := filepath.Join(project, "src", "main", "java", "com", "example")
	if err := os.MkdirAll(javaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(project, "pom.xml"), []byte(`<project xmlns="http://maven.apache.org/POM/4.0.0">
  <modelVersion>4.0.0</modelVersion>
  <groupId>com.example</groupId><artifactId>starter-check</artifactId><version>1</version>
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
    }
}
`
	javaPath := filepath.Join(javaDir, "A.java")
	if err := os.WriteFile(javaPath, []byte(source), 0o644); err != nil {
		t.Fatal(err)
	}

	// The server publishes from its connection's reading goroutine, so
	// the handlers hand events over and never wait for the test.
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

	a := newApp(defaultState)
	a.servers = servers{artifacts: setup.Artifacts, client: http.DefaultClient}
	start := startLanguageServer(a)
	srv, stop, err := start(project,
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
	if err != nil {
		t.Fatal(err)
	}
	if stop == nil {
		t.Fatal("the starter returned no way to stop the server")
	}
	defer stop()
	if srv == nil {
		t.Fatal("the starter returned no session")
	}

	snap := editor.NewBuffer(source).Snapshot()
	if err := srv.Open(javaPath, "java", snap); err != nil {
		t.Fatal(err)
	}

	// A real server's answers are asynchronous, so wait for them within
	// a bound instead of assuming an order.
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
			case ev := <-statuses:
				if ev.status == javalang.StatusError {
					t.Fatalf("jdtls reports an error: %s", ev.msg)
				}
				ready = ev.status == javalang.StatusReady
			default:
				return ready
			}
		}
		return ready
	})

	badLine := snap.LineOf(strings.Index(source, `int x = "bad";`))
	var bad javalang.Diagnostic
	found := false
	waitFor("an error diagnostic on the int x line", func() bool {
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

	if err := srv.Save(javaPath); err != nil {
		t.Errorf("save: %v", err)
	}
	if err := srv.Close(javaPath); err != nil {
		t.Errorf("close: %v", err)
	}
}
