package javalang

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestActionsReadEditsOfBothShapes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "A.java")
	if err := os.WriteFile(path, []byte("package a;\n\nclass A { List<String> l; }\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	uri := fileURI(path)
	edit := `{"changes":{"` + uri + `":[{"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":0}},"newText":"import java.util.List;\n"}]}}`
	raw := `[
		{"title":"Import 'List' (java.util)","kind":"quickfix","edit":` + edit + `},
		{"title":"Wrapped","command":{"title":"Wrapped","command":"java.apply.workspaceEdit","arguments":[` + edit + `]}},
		{"title":"Bare","command":"java.apply.workspaceEdit","arguments":[` + edit + `]},
		{"title":"Other command","command":{"title":"x","command":"java.edit.stringFormatting"}},
		{"title":"Creates a file","edit":{"documentChanges":[{"kind":"create","uri":"` + uri + `"}]}},
		{"title":"Off","kind":"quickfix","edit":` + edit + `,"disabled":{"reason":"no"}}
	]`
	var items []codeActionJSON
	if err := json.Unmarshal([]byte(raw), &items); err != nil {
		t.Fatal(err)
	}
	s := New()
	var got []string
	for _, it := range items {
		a, ok := s.action(it)
		if !ok {
			continue
		}
		got = append(got, a.Title)
		if len(a.Edits) != 1 || a.Edits[0].Path != path || len(a.Edits[0].Edits) != 1 {
			t.Fatalf("%s: edits = %+v", a.Title, a.Edits)
		}
		if e := a.Edits[0].Edits[0]; e.Start != 11 || e.End != 11 || e.NewText != "import java.util.List;\n" {
			t.Errorf("%s: edit = %+v, want an insert at byte 11", a.Title, e)
		}
	}
	want := []string{"Import 'List' (java.util)", "Wrapped", "Bare"}
	if len(got) != len(want) {
		t.Fatalf("actions = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("actions = %v, want %v", got, want)
		}
	}
}

func TestLspCodeKeepsNumbers(t *testing.T) {
	if n, ok := lspCode("16777218").(int); !ok || n != 16777218 {
		t.Errorf("lspCode(16777218) = %v", lspCode("16777218"))
	}
	if s, ok := lspCode("compiler.err").(string); !ok || s != "compiler.err" {
		t.Errorf("lspCode(string) = %v", lspCode("compiler.err"))
	}
	if lspCode("") != nil {
		t.Error("an empty code should be left out")
	}
}
