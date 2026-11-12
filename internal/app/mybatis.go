package app

import (
	"bytes"
	"os"
	"path"
	"strings"

	"megafactory/internal/javalang"
	"megafactory/internal/mybatis"
	"megafactory/internal/picker"
	"megafactory/internal/search"
)

// isXMLPath reports whether a path may be a mapper, which Cmd+click goes
// from to its interface.
func isXMLPath(rel string) bool { return path.Ext(rel) == ".xml" }

// mapperStatement is the statement of the mapper method declared at loc,
// a method with no body, in data, its file's text: the mapper XML whose
// namespace is its interface's. The project's XMLs are read on each go,
// not kept, as there are some hundred small ones at most.
func (a *App) mapperStatement(loc javalang.Location, data []byte) (picker.Item, bool) {
	start := min(max(loc.Start, 0), len(data))
	end := start
	for end < len(data) && (data[end] == '_' || data[end] == '$' || isAlnum(data[end])) {
		end++
	}
	if end == start {
		return picker.Item{}, false
	}
	namespace, id := mybatis.TypeName(loc.Path, data), string(data[start:end])
	for _, rel := range search.Files(a.project) {
		if !isXMLPath(rel) {
			continue
		}
		xml, err := os.ReadFile(a.abs(rel))
		if err != nil || !bytes.Contains(xml, []byte(namespace)) {
			continue
		}
		m, ok := mybatis.Parse(xml)
		if !ok || m.Namespace != namespace {
			continue
		}
		if s, ok := m.Statement(id); ok {
			return placeAt(rel, xml, s.Start), true
		}
	}
	return picker.Item{}, false
}

func isAlnum(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// mapperDefinition goes from a mapper XML's statement id to its method in
// the mapper interface, and from its namespace to the interface.
func (a *App) mapperDefinition(t *tab, offset int) {
	if t.ed == nil || !isXMLPath(t.path) {
		return
	}
	text := []byte(t.ed.Buffer.String())
	version := t.ed.Buffer.Version()
	a.host.Run(func() func() {
		m, ok := mybatis.Parse(text)
		if !ok {
			return func() {}
		}
		on := func(s, e int) bool { return s <= offset && offset <= e }
		// method is the statement's id clicked; none is the namespace.
		method := ""
		if !on(m.Start, m.End) {
			for _, s := range m.Statements {
				if on(s.Start, s.End) {
					method = s.ID
				}
			}
			if method == "" {
				return func() {}
			}
		}
		rel, ok := mybatis.JavaFile(search.Files(a.project), m.Namespace)
		if !ok {
			return func() {}
		}
		src, err := os.ReadFile(a.abs(rel))
		if err != nil {
			return func() {}
		}
		at := mybatis.Type(src, m.Namespace[strings.LastIndexByte(m.Namespace, '.')+1:])
		if method != "" {
			at = mybatis.Method(src, method)
		}
		if at < 0 {
			return func() {}
		}
		it := placeAt(rel, src, at)
		return func() {
			if a.tabActive(t) && t.ed.Buffer.Version() == version {
				a.goToItem(it)
			}
		}
	})
}

// placeAt is the place of offset in data, a file of the project at rel,
// with its line's text, as a list of usages shows it.
func placeAt(rel string, data []byte, offset int) picker.Item {
	lineStart := bytes.LastIndexByte(data[:offset], '\n') + 1
	lineEnd := bytes.IndexByte(data[offset:], '\n')
	if lineEnd < 0 {
		lineEnd = len(data)
	} else {
		lineEnd += offset
	}
	return picker.Item{
		Rel: rel, Line: bytes.Count(data[:offset], []byte("\n")), Col: offset - lineStart,
		Text: strings.TrimSpace(string(data[lineStart:lineEnd])),
	}
}
