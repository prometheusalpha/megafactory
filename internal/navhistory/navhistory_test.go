package navhistory

import "testing"

func TestBackAndForward(t *testing.T) {
	var h History
	a, b, c := Place{"A.java", 10, 2}, Place{"B.java", 40, 0}, Place{"A.java", 200, 4}
	h.Push(a)
	h.Push(b)
	if p, ok := h.Back(c); !ok || p != b {
		t.Fatalf("Back = %v %v, want %v", p, ok, b)
	}
	if p, ok := h.Back(b); !ok || p != a {
		t.Fatalf("second Back = %v %v, want %v", p, ok, a)
	}
	if _, ok := h.Back(a); ok {
		t.Fatal("Back past the first place")
	}
	if p, ok := h.Forward(a); !ok || p != b {
		t.Fatalf("Forward = %v %v, want %v", p, ok, b)
	}
	if p, ok := h.Forward(b); !ok || p != c {
		t.Fatalf("second Forward = %v %v, want %v", p, ok, c)
	}
}

func TestPushForgetsForward(t *testing.T) {
	var h History
	h.Push(Place{"A.java", 0, 0})
	h.Back(Place{"B.java", 0, 0})
	h.Push(Place{"C.java", 0, 0})
	if _, ok := h.Forward(Place{"D.java", 0, 0}); ok {
		t.Error("Forward after a new place")
	}
}

func TestNearPlacesAreOne(t *testing.T) {
	var h History
	h.Push(Place{"A.java", 100, 0})
	h.Push(Place{"A.java", 105, 3})
	if p, _ := h.Back(Place{"B.java", 0, 0}); p != (Place{"A.java", 105, 3}) {
		t.Errorf("Back = %v, want the later of two near places", p)
	}
	if _, ok := h.Back(Place{"A.java", 105, 3}); ok {
		t.Error("two near places kept as two")
	}
}

func TestBackPassesOverWhereTheCaretIs(t *testing.T) {
	var h History
	h.Push(Place{"A.java", 10, 0})
	h.Push(Place{"A.java", 300, 0})
	if p, ok := h.Back(Place{"A.java", 302, 0}); !ok || p.Line != 10 {
		t.Errorf("Back = %v %v, want line 10", p, ok)
	}
}

func TestKeepsMax(t *testing.T) {
	var h History
	for i := range Max + 5 {
		h.Push(Place{"A.java", i * Near, 0})
	}
	n := 0
	cur := Place{"B.java", 0, 0}
	for {
		p, ok := h.Back(cur)
		if !ok {
			break
		}
		n++
		cur = p
	}
	if n != Max || cur.Line != 5*Near {
		t.Errorf("went back %d places to line %d, want %d to line %d", n, cur.Line, Max, 5*Near)
	}
}
