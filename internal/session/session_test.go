package session

import "testing"

func TestWithKeepsOthersAndDropsEmpty(t *testing.T) {
	var s Store
	s = s.With("/a", Project{Files: []string{"x.go"}, Active: "x.go"})
	s = s.With("/b", Project{Files: []string{"y.go"}})
	if len(s) != 2 {
		t.Fatalf("store %v", s)
	}
	s = s.With("/a", Project{})
	if _, ok := s["/a"]; ok || len(s) != 1 {
		t.Errorf("empty session kept: %v", s)
	}
}

func TestEqual(t *testing.T) {
	a := Project{Files: []string{"x", "y"}, Active: "y"}
	if !a.Equal(Project{Files: []string{"x", "y"}, Active: "y"}) {
		t.Error("same sessions differ")
	}
	if a.Equal(Project{Files: []string{"y", "x"}, Active: "y"}) || a.Equal(Project{Files: a.Files}) {
		t.Error("different sessions equal")
	}
}
