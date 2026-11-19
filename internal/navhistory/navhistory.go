// Package navhistory keeps the places the caret left, for Back and Forward
// to go between them, as IntelliJ's Navigate Back and Forward.
package navhistory

// Max is how many places each way keeps; the oldest go first.
const Max = 50

// Near is how many lines apart two places of a file must be to count as
// two: a caret that moves less has not left its place, and a place that
// near the last one replaces it.
const Near = 10

// Place is a caret's spot in a project file. Line and Col count from 0.
type Place struct {
	Path      string
	Line, Col int
}

// near reports whether p and q are one place.
func (p Place) near(q Place) bool {
	d := p.Line - q.Line
	return p.Path == q.Path && -Near < d && d < Near
}

// Jumped reports whether the caret going from p to q left p.
func Jumped(p, q Place) bool { return !p.near(q) }

// History is the places behind the caret and, after Back, ahead of it.
type History struct {
	back, forward []Place
}

// Push records p as a place the caret left. Going somewhere new forgets
// what Back had gone back from.
func (h *History) Push(p Place) {
	h.forward = nil
	if n := len(h.back); n > 0 && h.back[n-1].near(p) {
		h.back[n-1] = p
		return
	}
	h.back = push(h.back, p)
}

// Back is the place before cur, which Forward then comes back to. A place
// near cur is passed over, as going there would not move.
func (h *History) Back(cur Place) (Place, bool) {
	return step(&h.back, &h.forward, cur)
}

// Forward undoes a Back.
func (h *History) Forward(cur Place) (Place, bool) {
	return step(&h.forward, &h.back, cur)
}

func step(from, to *[]Place, cur Place) (Place, bool) {
	for len(*from) > 0 {
		n := len(*from) - 1
		p := (*from)[n]
		*from = (*from)[:n]
		if !p.near(cur) {
			*to = push(*to, cur)
			return p, true
		}
	}
	return Place{}, false
}

func push(list []Place, p Place) []Place {
	list = append(list, p)
	if len(list) > Max {
		list = append(list[:0], list[len(list)-Max:]...)
	}
	return list
}

// Clear forgets every place, as when another project opens.
func (h *History) Clear() { *h = History{} }
