package search

import (
	"os"
	"path/filepath"
	"slices"
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
