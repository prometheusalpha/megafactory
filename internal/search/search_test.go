package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestTextKinds(t *testing.T) {
	for _, c := range []struct {
		query, text string
		kind        Kind
		idx         []int
	}{
		{"pom.xml", "pom.xml", Exact, []int{0, 1, 2, 3, 4, 5, 6}},
		{"osij", "OrderServiceImpl.java", Initials, []int{0, 5, 12, 17}},
		{"osi", "OrderServiceImpl.java", WordPrefix, []int{0, 5, 12}},
		{"ord", "OrderServiceImpl.java", Prefix, []int{0, 1, 2}},
		{"ordimp", "OrderServiceImpl.java", WordPrefix, []int{0, 1, 2, 12, 13, 14}},
		{"vice", "OrderServiceImpl.java", Substring, []int{8, 9, 10, 11}},
		{"evl", "OrderServiceImpl.java", Fuzzy, nil},
	} {
		m, ok := Text(c.query, c.text)
		if !ok || m.Kind != c.kind {
			t.Errorf("%q in %q: %+v ok=%v, want kind %v", c.query, c.text, m, ok, c.kind)
		} else if c.idx != nil && !slices.Equal(m.Indices, c.idx) {
			t.Errorf("%q in %q: indices %v, want %v", c.query, c.text, m.Indices, c.idx)
		}
	}
	if _, ok := Text("zzz", "OrderServiceImpl.java"); ok {
		t.Error("zzz matched")
	}
	// One letter only matches from the start, not anywhere in order.
	if _, ok := Text("z", "OrderServiceImpl.java"); ok {
		t.Error("single letter matched as fuzzy")
	}
}

func TestFindRanksNameBeforePath(t *testing.T) {
	files := []string{"src/order/Readme.md", "src/OrderService.java", "Order.java", "docs/order.md"}
	var got []string
	for _, h := range Find(files, "order") {
		got = append(got, h.Path)
	}
	// Prefix hits by the shorter name, then the one only its folder matches.
	want := []string{"docs/order.md", "Order.java", "src/OrderService.java", "src/order/Readme.md"}
	if !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFilesWalksOutsideGit(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"a/B.java", "node_modules/x/y.js", "target/c.class"} {
		os.MkdirAll(filepath.Join(root, filepath.Dir(p)), 0o755)
		os.WriteFile(filepath.Join(root, p), nil, 0o644)
	}
	if got := Files(root); !slices.Equal(got, []string{"a/B.java"}) {
		t.Errorf("got %q", got)
	}
}

func writeTree(t *testing.T, files map[string]string) (root string, names []string) {
	t.Helper()
	root = t.TempDir()
	for name, text := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(text), 0o644); err != nil {
			t.Fatal(err)
		}
		names = append(names, name)
	}
	slices.Sort(names)
	return root, names
}

func TestContent(t *testing.T) {
	root, files := writeTree(t, map[string]string{
		"a/One.java": "class One {\n\tString Foo = \"foo\";\r\n\tint foobar;\n}\n",
		"b/two.txt":  "no match here\nFOO and foo_bar\n",
		"c/bin.dat":  "foo\x00foo",
	})
	for _, c := range []struct {
		name string
		q    Query
		want []string // path:line:col, in order
	}{
		{"fold case", Query{Text: "foo"}, []string{"a/One.java:1:8", "a/One.java:1:15", "a/One.java:2:5", "b/two.txt:1:0", "b/two.txt:1:8"}},
		{"match case", Query{Text: "foo", MatchCase: true}, []string{"a/One.java:1:15", "a/One.java:2:5", "b/two.txt:1:8"}},
		{"whole word", Query{Text: "foo", WholeWord: true}, []string{"a/One.java:1:8", "a/One.java:1:15", "b/two.txt:1:0"}},
		{"regex", Query{Text: `fo+\w*b\w+`, Regex: true}, []string{"a/One.java:2:5", "b/two.txt:1:8"}},
		{"symbol is literal", Query{Text: "(", WholeWord: true}, nil},
	} {
		res, err := Content(context.Background(), root, files, c.q)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		var got []string
		for _, m := range res.Matches {
			got = append(got, fmt.Sprintf("%s:%d:%d", m.Path, m.Line, m.Col))
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestContentSnippetAndFiles(t *testing.T) {
	root, files := writeTree(t, map[string]string{"x.go": "\t\t\tfunc needle() {}\r\n" + strings.Repeat("y", 500) + " needle " + strings.Repeat("z", 500) + "\n"})
	res, _ := Content(context.Background(), root, files, Query{Text: "needle"})
	if len(res.Matches) != 2 || res.Files != 1 {
		t.Fatalf("matches %d, files %d", len(res.Matches), res.Files)
	}
	m := res.Matches[0]
	if m.Text != "func needle() {}" || m.Text[m.From:m.To] != "needle" {
		t.Errorf("snippet %q [%d:%d]", m.Text, m.From, m.To)
	}
	long := res.Matches[1]
	if len(long.Text) > snippetBytes+len("needle") || long.Text[long.From:long.To] != "needle" {
		t.Errorf("long line: %d bytes, match %q", len(long.Text), long.Text[long.From:long.To])
	}
	if long.Col != 500+1 {
		t.Errorf("col %d keeps the line's offset", long.Col)
	}
}

func TestContentBadRegexAndCancel(t *testing.T) {
	root, files := writeTree(t, map[string]string{"a.txt": "x"})
	if _, err := Content(context.Background(), root, files, Query{Text: "(", Regex: true}); err == nil {
		t.Error("an invalid regular expression was no error")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if res, err := Content(ctx, root, files, Query{Text: "x"}); err != nil || len(res.Matches) != 0 {
		t.Errorf("canceled: %v, %d matches", err, len(res.Matches))
	}
}

func TestContentCapsMatches(t *testing.T) {
	text := strings.Repeat("hit\n", 90)
	m := map[string]string{}
	for i := range 10 {
		m[fmt.Sprintf("f%02d.txt", i)] = text
	}
	root, files := writeTree(t, m)
	res, _ := Content(context.Background(), root, files, Query{Text: "hit"})
	if len(res.Matches) != MaxMatches || !res.More || res.Matches[0].Path != "f00.txt" {
		t.Errorf("%d matches, more %v, first %s", len(res.Matches), res.More, res.Matches[0].Path)
	}
}
