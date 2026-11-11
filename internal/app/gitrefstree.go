package app

import (
	"slices"
	"strings"

	"megafactory/internal/git"
)

type itemKind uint8

const (
	itemRef itemKind = iota
	itemFolder
	itemSection
	itemAction
)

// pickerItem is a selectable row of the branch picker. key identifies a row
// across frames and is what a folder or section toggles; depth indents it.
type pickerItem struct {
	kind   itemKind
	key    string
	depth  int
	open   bool // folders and sections: whether their rows are listed
	count  int  // sections: refs under it
	ref    git.Ref
	action string // itemAction: "create" or "revision"
	label  string
}

// refNode is one path segment of a ref name. Segments match exactly, so on a
// case-sensitive repo Foo/x and foo/x are two folders, as git has them.
type refNode struct {
	name string
	ref  *git.Ref
	kids []*refNode
	idx  map[string]*refNode
}

func (n *refNode) child(name string) *refNode {
	if k, ok := n.idx[name]; ok {
		return k
	}
	if n.idx == nil {
		n.idx = map[string]*refNode{}
	}
	k := &refNode{name: name}
	n.idx[name] = k
	n.kids = append(n.kids, k)
	return k
}

// insert files ref under its short name's segments.
func (n *refNode) insert(short string, ref git.Ref) {
	for _, seg := range strings.Split(short, "/") {
		n = n.child(seg)
	}
	n.ref = &ref
}

// flatten lists the rows under n: folders before branches, each by name, a
// folder's rows following it only while open says so.
func (n *refNode) flatten(section, path string, depth int, open func(key string) bool, out *[]pickerItem) {
	kids := slices.Clone(n.kids)
	slices.SortFunc(kids, func(a, b *refNode) int {
		if af, bf := len(a.kids) > 0, len(b.kids) > 0; af != bf {
			if af {
				return -1
			}
			return 1
		}
		if c := strings.Compare(strings.ToLower(a.name), strings.ToLower(b.name)); c != 0 {
			return c
		}
		return strings.Compare(a.name, b.name)
	})
	for _, k := range kids {
		p := path + k.name
		if k.ref != nil {
			*out = append(*out, pickerItem{kind: itemRef, key: "ref:" + k.ref.Full, depth: depth, ref: *k.ref, label: k.name})
		}
		if len(k.kids) == 0 {
			continue
		}
		key := "dir:" + section + "\x00" + p
		isOpen := open(key)
		if k.ref == nil {
			*out = append(*out, pickerItem{kind: itemFolder, key: key, depth: depth, open: isOpen, label: k.name})
		}
		if isOpen {
			k.flatten(section, p+"/", depth+1, open, out)
		}
	}
}

// openByDefault is the state of a folder or section nobody toggled: all
// collapsed, except Recent.
func openByDefault(key string) bool { return key == "sec:recent" }

// pickerItems is the picker's visible rows, in order. A query lists every
// match with the folders above it open, and leaves Recent out, since those
// branches are listed again under Local.
func (a *App) pickerItems(p *branchPicker) []pickerItem {
	q := strings.ToLower(p.query)
	searching := q != ""
	match := func(s string) bool {
		return !searching || strings.Contains(strings.ToLower(s), q)
	}
	open := func(key string) bool {
		return searching || p.toggled[key] != openByDefault(key)
	}
	var items []pickerItem
	section := func(key, title string, count int) bool {
		isOpen := open(key)
		items = append(items, pickerItem{kind: itemSection, key: key, open: isOpen, count: count, label: title})
		return isOpen
	}

	if !searching {
		var recent []pickerItem
		for _, name := range p.recent {
			if name == a.branch || len(recent) == recentLimit {
				continue
			}
			for _, r := range a.git.refs {
				if r.Kind == git.LocalBranch && r.Name == name {
					recent = append(recent, pickerItem{kind: itemRef, key: "recent:" + r.Full, depth: 1, ref: r, label: r.Name})
					break
				}
			}
		}
		if len(recent) > 0 && section("sec:recent", "Recent", len(recent)) {
			items = append(items, recent...)
		}
	}

	// Matching refs go into one tree per section: local and tags have one
	// each, remote branches one per remote.
	type group struct {
		key, title string
		tree       refNode
		count      int
	}
	var local, tags group
	local.key, local.title = "sec:local", "Local Branches"
	tags.key, tags.title = "sec:tags", "Tags"
	var remotes []*group
	for _, r := range a.git.refs {
		if !match(r.Name) {
			continue
		}
		var g *group
		short := r.Name
		switch r.Kind {
		case git.LocalBranch:
			g = &local
		case git.Tag:
			g = &tags
		case git.RemoteBranch:
			// The remote is the first segment; its own section drops it.
			remote, rest, _ := strings.Cut(r.Name, "/")
			short = rest
			for _, rg := range remotes {
				if rg.key == "sec:remote:"+remote {
					g = rg
				}
			}
			if g == nil {
				g = &group{key: "sec:remote:" + remote, title: "Remote Branches · " + remote}
				remotes = append(remotes, g)
			}
		}
		g.tree.insert(short, r)
		g.count++
	}
	slices.SortFunc(remotes, func(a, b *group) int { return strings.Compare(a.key, b.key) })
	for _, g := range append([]*group{&local}, append(remotes, &tags)...) {
		if g.count == 0 {
			continue
		}
		if section(g.key, g.title, g.count) {
			g.tree.flatten(g.key, "", 1, open, &items)
		}
	}

	if !searching || match("Create new branch from HEAD") {
		items = append(items, pickerItem{kind: itemAction, action: "create", key: "action:create", label: "Create new branch from HEAD…"})
	}
	if searching {
		items = append(items, pickerItem{kind: itemAction, action: "revision", key: "action:revision", label: "Checkout revision " + p.query + "…"})
	}
	return items
}

// caseClash returns the local branch that name differs from only in letter
// case, in the whole name or in a folder of it, or "". Git keeps loose refs
// as files under .git/refs/heads, so on a case-insensitive file system, the
// macOS default, such a pair can fail to create or overwrite each other.
// Remote refs are left out: only branches the user is about to create matter.
func caseClash(name string, refs []git.Ref) string {
	parts := strings.Split(name, "/")
	for _, r := range refs {
		if r.Kind != git.LocalBranch {
			continue
		}
		other := strings.Split(r.Name, "/")
		for i := 0; i < min(len(parts), len(other)); i++ {
			if parts[i] == other[i] {
				continue
			}
			if strings.EqualFold(parts[i], other[i]) {
				return r.Name
			}
			break
		}
	}
	return ""
}
