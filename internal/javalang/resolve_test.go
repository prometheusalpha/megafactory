package javalang

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestResolveMainClassesSendsCommandAndDecodesResult(t *testing.T) {
	h := newHarness(t, `{}`)
	h.fake.respond(func(method string, params json.RawMessage) any {
		return []MainClass{{MainClass: "com.example.Demo", ProjectName: "demo", FilePath: "/tmp/Demo.java"}}
	})
	got, err := h.session.ResolveMainClasses()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].MainClass != "com.example.Demo" || got[0].ProjectName != "demo" {
		t.Fatalf("main classes %v", got)
	}
	n := h.next(t)
	if n.method != "workspace/executeCommand" {
		t.Fatalf("method %q", n.method)
	}
	var p struct {
		Command   string          `json:"command"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(n.params, &p); err != nil {
		t.Fatal(err)
	}
	if p.Command != "vscode.java.resolveMainClass" || string(p.Arguments) != "[]" {
		t.Errorf("command %q arguments %s", p.Command, p.Arguments)
	}
}

func TestResolveClasspathSendsArgumentsAndSplitsPaths(t *testing.T) {
	h := newHarness(t, `{}`)
	h.fake.respond(func(method string, params json.RawMessage) any {
		return [][]string{{"mods"}, {"classes", "lib/a.jar"}}
	})
	mods, classes, err := h.session.ResolveClasspath("com.example.Demo", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 1 || mods[0] != "mods" || len(classes) != 2 || classes[1] != "lib/a.jar" {
		t.Fatalf("mods %v classes %v", mods, classes)
	}
	n := h.next(t)
	var p struct {
		Command   string   `json:"command"`
		Arguments []string `json:"arguments"`
	}
	if err := json.Unmarshal(n.params, &p); err != nil {
		t.Fatal(err)
	}
	if p.Command != "vscode.java.resolveClasspath" || len(p.Arguments) != 2 ||
		p.Arguments[0] != "com.example.Demo" || p.Arguments[1] != "demo" {
		t.Errorf("command %q arguments %v", p.Command, p.Arguments)
	}
}

func TestResolveClasspathToleratesShortResult(t *testing.T) {
	h := newHarness(t, `{}`)
	h.fake.respond(func(method string, params json.RawMessage) any {
		return []any{nil}
	})
	mods, classes, err := h.session.ResolveClasspath("A", "p")
	if err != nil {
		t.Fatal(err)
	}
	if len(mods) != 0 || len(classes) != 0 {
		t.Errorf("mods %v classes %v", mods, classes)
	}
}

func TestResolveWithoutServerReturnsErrNotStarted(t *testing.T) {
	s := New()
	if _, err := s.ResolveMainClasses(); !errors.Is(err, ErrNotStarted) {
		t.Errorf("main classes: %v", err)
	}
	if _, _, err := s.ResolveClasspath("A", "p"); !errors.Is(err, ErrNotStarted) {
		t.Errorf("classpath: %v", err)
	}
}
