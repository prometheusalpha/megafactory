package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"megafactory/internal/javalang"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// Cmd+click goes from a mapper method to its statement in the mapper XML,
// and from the statement's id, or the namespace, back to the interface.
func TestMapperGoTo(t *testing.T) {
	dir := sampleProject(t)
	a, f, tt := openDemo(t, dir)

	repoRel := "src/main/java/com/example/demo/PinRepo.java"
	xmlRel := "src/main/resources/sql/PinRepo.xml"
	repoSrc := "package com.example.demo;\n\npublic interface PinRepo {\n    int upsertPin(String id);\n    int countPins();\n}\n"
	xmlSrc := "<?xml version=\"1.0\" encoding=\"UTF-8\" ?>\n<mapper namespace=\"com.example.demo.PinRepo\">\n" +
		"    <insert id=\"upsertPin\">\n        INSERT INTO pins VALUES (#{id})\n    </insert>\n</mapper>\n"
	repo := filepath.Join(dir, filepath.FromSlash(repoRel))
	xml := filepath.Join(dir, filepath.FromSlash(xmlRel))
	os.MkdirAll(filepath.Dir(xml), 0o755)
	os.WriteFile(repo, []byte(repoSrc), 0o644)
	os.WriteFile(xml, []byte(xmlSrc), 0o644)

	for _, c := range []struct {
		name   string
		method string
		want   string
		pos    editor.Pos
	}{
		{"a statement", "upsertPin", xmlRel, editor.Pos{Line: 2, Col: strings.Index(`    <insert id="upsertPin`, "upsertPin")}},
		// No statement: the interface's declaration, as any method's.
		{"none", "countPins", repoRel, editor.Pos{Line: 4, Col: 8}},
	} {
		a.openAt(demoRel, 0)
		tt.Frame()
		f.define = func(string, int) ([]javalang.Location, error) {
			return []javalang.Location{{Path: repo, Start: strings.Index(repoSrc, c.method)}}, nil
		}
		a.tabs[a.active].ed.OnDefinition(0)
		tt.Frame()
		if got := a.tabs[a.active].path; got != c.want {
			t.Fatalf("%s: opened %q, want %q", c.name, got, c.want)
		}
		if got := a.tabs[a.active].ed.CursorPos(); got != c.pos {
			t.Errorf("%s: caret at %+v, want %+v", c.name, got, c.pos)
		}
	}

	for _, c := range []struct {
		name string
		on   string
		pos  editor.Pos
	}{
		{"an id", `"upsertPin"`, editor.Pos{Line: 3, Col: 8}},
		{"the namespace", `"com.example.demo.PinRepo"`, editor.Pos{Line: 2, Col: 17}},
	} {
		a.openAt(xmlRel, 0)
		tt.Frame()
		a.tabs[a.active].ed.OnDefinition(strings.Index(xmlSrc, c.on) + 3)
		tt.Frame()
		if got := a.tabs[a.active].path; got != repoRel {
			t.Fatalf("%s: opened %q, want %q", c.name, got, repoRel)
		}
		if got := a.tabs[a.active].ed.CursorPos(); got != c.pos {
			t.Errorf("%s: caret at %+v, want %+v", c.name, got, c.pos)
		}
	}

	// On the method's declaration, its statement is the first of its
	// usages: the only one goes there, as one usage does.
	decl := strings.Index(repoSrc, "upsertPin")
	f.define = func(string, int) ([]javalang.Location, error) {
		return []javalang.Location{{Path: repo, Start: decl, End: decl + len("upsertPin")}}, nil
	}
	usages := func(refs ...javalang.Location) *tab {
		t.Helper()
		a.openAt(repoRel, 0)
		tt.Frame()
		tb := a.tabs[a.active]
		f.refer = func(string, int) ([]javalang.Location, error) { return refs, nil }
		tb.ed.OnDefinition(decl + 2)
		tt.Frame()
		tt.Frame()
		return tb
	}
	usages()
	if got := a.tabs[a.active].path; got != xmlRel {
		t.Fatalf("declaration, no other usage: opened %q, want %q", got, xmlRel)
	}
	call := strings.Index(repoSrc, "countPins")
	tb := usages(javalang.Location{Path: repo, Start: call, End: call + len("countPins")})
	if a.tabs[a.active] != tb || !tt.HasText("Usages  2") || !tt.HasText(`<insert id="upsertPin">`) {
		t.Fatalf("declaration: no statement among the usages, %q", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := a.tabs[a.active].path; got != xmlRel {
		t.Errorf("declaration: Enter on the first usage opened %q", got)
	}

	// Anywhere else in the XML stays.
	a.openAt(xmlRel, 0)
	tt.Frame()
	a.tabs[a.active].ed.OnDefinition(strings.Index(xmlSrc, "INSERT"))
	tt.Frame()
	if got := a.tabs[a.active].path; got != xmlRel {
		t.Errorf("SQL: opened %q", got)
	}
}
