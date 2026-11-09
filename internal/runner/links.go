package runner

import (
	"regexp"
	"strconv"
)

// Link is a File.java:123 reference found in a line of output. From and To
// are byte offsets into the line's Text. Line is 1-based, Col is 1-based and
// 0 when the reference has no column.
type Link struct {
	Path      string
	Line, Col int
	From, To  int
}

var linkRE = regexp.MustCompile(`([\w.$/\-]+\.java):(\d+)(?::(\d+))?`)

// Links finds Java source references in one line. A bare path without a
// line number is not a reference, so URLs and Windows drive letters do not
// match on their own.
func Links(s string) []Link {
	var out []Link
	for _, m := range linkRE.FindAllStringSubmatchIndex(s, -1) {
		l := Link{Path: s[m[2]:m[3]], From: m[0], To: m[1]}
		l.Line, _ = strconv.Atoi(s[m[4]:m[5]])
		if m[6] >= 0 {
			l.Col, _ = strconv.Atoi(s[m[6]:m[7]])
		}
		out = append(out, l)
	}
	return out
}
