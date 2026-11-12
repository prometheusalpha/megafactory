// Package mybatis ties a MyBatis mapper interface's methods to the
// statements of its XML: the mapper's namespace is the interface's name,
// and a statement's id is the method's.
package mybatis

import (
	"bytes"
	"encoding/xml"
	"io"
	"path"
	"regexp"
	"strings"
)

// Mapper is a mapper XML: its namespace and statements, each with the
// byte span of its attribute's value, where a go to lands and a Cmd+click
// is from.
type Mapper struct {
	Namespace  string
	Start, End int
	Statements []Statement
}

// Statement is a mapper's select, insert, update or delete, by its id.
type Statement struct {
	ID         string
	Start, End int
}

// statements are the elements a mapper method runs; sql and resultMap are
// pieces of them, which no method names.
var statements = map[string]bool{"select": true, "insert": true, "update": true, "delete": true}

// Parse reads a mapper XML, reporting false when data is not one. A
// decoder, not a pattern, so that a commented out statement is not found.
func Parse(data []byte) (Mapper, bool) {
	var m Mapper
	d := xml.NewDecoder(bytes.NewReader(data))
	// The DTD is a URL a strict decoder would not fetch anyway, and a
	// mapper half typed is still one to go from.
	d.Strict = false
	// Mappers declaring ISO-8859-1 are common, and the decoder refuses
	// any but UTF-8 unconverted; converted, offsets would no longer be
	// the file's, and the names wanted are ASCII anyway.
	d.CharsetReader = func(_ string, r io.Reader) (io.Reader, error) { return r, nil }
	depth := 0
	found := false
	for {
		at := int(d.InputOffset())
		tok, err := d.Token()
		if err != nil {
			break
		}
		switch e := tok.(type) {
		case xml.StartElement:
			depth++
			raw := data[at:min(int(d.InputOffset()), len(data))]
			switch {
			case depth == 1 && e.Name.Local == "mapper":
				if s, z, ok := attr(raw, "namespace"); ok {
					found = true
					m.Namespace, m.Start, m.End = string(raw[s:z]), at+s, at+z
				}
			case depth == 2 && found && statements[e.Name.Local]:
				if s, z, ok := attr(raw, "id"); ok {
					m.Statements = append(m.Statements, Statement{ID: string(raw[s:z]), Start: at + s, End: at + z})
				}
			}
		case xml.EndElement:
			depth--
		}
	}
	return m, found
}

// attr is the span of the value of the attribute name in a start tag's
// text, between its quotes.
func attr(tag []byte, name string) (int, int, bool) {
	re := regexp.MustCompile(`\s` + name + `\s*=\s*("([^"]*)"|'([^']*)')`)
	loc := re.FindSubmatchIndex(tag)
	if loc == nil {
		return 0, 0, false
	}
	if loc[4] >= 0 {
		return loc[4], loc[5], true
	}
	return loc[6], loc[7], true
}

// Statement returns the statement of id.
func (m Mapper) Statement(id string) (Statement, bool) {
	for _, s := range m.Statements {
		if s.ID == id {
			return s, true
		}
	}
	return Statement{}, false
}

var packageRe = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*;`)

// TypeName is the qualified name of the top-level type a Java file
// declares, which its file is named after.
func TypeName(file string, src []byte) string {
	name := strings.TrimSuffix(path.Base(strings.ReplaceAll(file, "\\", "/")), ".java")
	if m := packageRe.FindSubmatch(src); m != nil {
		return string(m[1]) + "." + name
	}
	return name
}

// JavaFile is which of files, relative with slashes, declares the type of
// namespace: the one its package's folders end in.
func JavaFile(files []string, namespace string) (string, bool) {
	want := strings.ReplaceAll(namespace, ".", "/") + ".java"
	for _, f := range files {
		if f == want || strings.HasSuffix(f, "/"+want) {
			return f, true
		}
	}
	return "", false
}

// Method is the offset of the name of method name's declaration in a Java
// source, -1 when it has none: the name after a type and before '(', so
// that a call, after '.' or '=', is not it.
func Method(src []byte, name string) int {
	re := regexp.MustCompile(`[\w>\]]\s+(` + regexp.QuoteMeta(name) + `)\s*\(`)
	if loc := re.FindSubmatchIndex(code(src)); loc != nil {
		return loc[2]
	}
	return -1
}

// code is src with its comments and strings blanked, their offsets kept,
// so that a Javadoc's "Calls name(" is not the declaration.
func code(src []byte) []byte {
	out := bytes.Clone(src)
	blank := func(i, j int) {
		for k := i; k < j; k++ {
			if out[k] != '\n' {
				out[k] = ' '
			}
		}
	}
	for i := 0; i < len(src); i++ {
		switch {
		case bytes.HasPrefix(src[i:], []byte("//")):
			j := bytes.IndexByte(src[i:], '\n')
			if j < 0 {
				j = len(src) - i
			}
			blank(i, i+j)
			i += j
		case bytes.HasPrefix(src[i:], []byte("/*")):
			j := bytes.Index(src[i+2:], []byte("*/"))
			end := len(src)
			if j >= 0 {
				end = i + 2 + j + 2
			}
			blank(i, end)
			i = end - 1
		case src[i] == '"' || src[i] == '\'':
			j := i + 1
			for j < len(src) && src[j] != src[i] && src[j] != '\n' {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			blank(i+1, min(j, len(src)))
			i = j
		}
	}
	return out
}

// Type is the offset of the name of type name's declaration in a Java
// source, -1 when it has none.
func Type(src []byte, name string) int {
	re := regexp.MustCompile(`\b(?:interface|class)\s+(` + regexp.QuoteMeta(name) + `)\b`)
	if loc := re.FindSubmatchIndex(src); loc != nil {
		return loc[2]
	}
	return -1
}
