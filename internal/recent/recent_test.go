package recent

import (
	"fmt"
	"slices"
	"testing"
)

func TestAdd(t *testing.T) {
	l := Add(Add(Add(nil, "/a"), "/b"), "/a")
	if !slices.Equal(l, []string{"/a", "/b"}) {
		t.Errorf("%q", l)
	}
	for i := range Max + 5 {
		l = Add(l, fmt.Sprint("/p", i))
	}
	if len(l) != Max || l[0] != fmt.Sprint("/p", Max+4) {
		t.Errorf("kept %d, first %q", len(l), l[0])
	}
}

func TestMatch(t *testing.T) {
	l := []string{"/code/vtt/order-service", "/code/personal/megafactory", "/code/personal/notes"}
	for q, want := range map[string][]string{
		"":         l,
		"mfac":     {"/code/personal/megafactory"},
		"ORD":      {"/code/vtt/order-service"},
		"personal": {"/code/personal/megafactory", "/code/personal/notes"},
		// A name's match comes before a path's.
		"notes": {"/code/personal/notes"},
		"e":     {"/code/vtt/order-service", "/code/personal/megafactory", "/code/personal/notes"},
		"zzz":   nil,
	} {
		if got := Match(l, q); !slices.Equal(got, want) {
			t.Errorf("%q: %q, want %q", q, got, want)
		}
	}
	if got := Match([]string{"/x/vtt-app", "/vtt/other"}, "vtt"); !slices.Equal(got, []string{"/x/vtt-app", "/vtt/other"}) {
		t.Errorf("name before path: %q", got)
	}
}
