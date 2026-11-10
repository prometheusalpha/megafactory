package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui/editor"
)

// spans renders a line's tokens as kind:text, for reading in failures.
func spans(line string, state uint64) (string, uint64) {
	toks, next := tokenizeProperties(line, state, nil)
	names := map[editor.TokenKind]string{
		editor.TokenProperty: "key", editor.TokenOperator: "op", editor.TokenString: "str",
		editor.TokenEscape: "esc", editor.TokenComment: "comment", editor.TokenConstant: "const", editor.TokenNumber: "num",
	}
	var b strings.Builder
	start := 0
	for _, t := range toks {
		if t.End <= start {
			continue
		}
		if t.Kind == editor.TokenText { // indentation
			start = t.End
			continue
		}
		b.WriteString(names[t.Kind] + ":" + line[start:t.End] + "|")
		start = t.End
	}
	return b.String(), next
}

func TestTokenizeProperties(t *testing.T) {
	for _, c := range []struct {
		line  string
		state uint64
		want  string
		next  uint64
	}{
		{"server.port=8080", 0, "key:server.port|op:=|num:8080|", 0},
		{"  spring.application.name : demo", 0, "key:spring.application.name|op: : |str:demo|", 0},
		{"debug true", 0, "key:debug|op: |const:true|", 0},
		{"# a comment", 0, "comment:# a comment|", 0},
		{"   ! another", 0, "comment:! another|", 0},
		{`url=jdbc:${DB_HOST:localhost}/app`, 0, "key:url|op:=|str:jdbc:|esc:${DB_HOST:localhost}|str:/app|", 0},
		{`greeting=Xin ch\u00e0o\n`, 0, `key:greeting|op:=|str:Xin ch|esc:\u00e0|str:o|esc:\n|`, 0},
		{`my\ key=v`, 0, `key:my|esc:\ |key:key|op:=|str:v|`, 0},
		{`list=a,\`, 0, `key:list|op:=|str:a,|esc:\|`, 1},
		{`    b,\`, 1, `str:b,|esc:\|`, 1},
		{`    c`, 1, `str:c|`, 0},
		// An even run of \ is an escaped \, not a continuation.
		{`path=C:\\`, 0, `key:path|op:=|str:C:|esc:\\|`, 0},
		// A continued value is a string, though it starts as a number.
		{`n=1\`, 0, `key:n|op:=|str:1|esc:\|`, 1},
		{"", 0, "", 0},
	} {
		got, next := spans(c.line, c.state)
		if got != c.want || next != c.next {
			t.Errorf("%q in %d:\n got %s → %d\nwant %s → %d", c.line, c.state, got, next, c.want, c.next)
		}
	}
}

func TestLanguageFor(t *testing.T) {
	for name, want := range map[string]string{
		"application.properties": "Properties", "App.java": "Java", "pom.xml": "XML",
		"config.json": "JSON", "README.md": "Markdown", "application.yml": "YAML",
	} {
		if l := languageFor(name); l == nil || l.Name != want {
			t.Errorf("%s: %v, want %s", name, l, want)
		}
	}
}

func TestOpenPropertiesHighlights(t *testing.T) {
	a, tt, _ := sampleWithMenu(t)
	click(t, tt, "resources", "application.properties")
	if l := a.tabs[a.active].ed.Buffer.Language(); l != propertiesLanguage || !tt.HasText("Properties") {
		t.Errorf("language %v", l)
	}
}
