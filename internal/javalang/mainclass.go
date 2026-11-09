package javalang

import (
	"path"
	"regexp"
	"strings"
)

var (
	mainMethod   = regexp.MustCompile(`public\s+static\s+void\s+main\s*\(`)
	springBoot   = regexp.MustCompile(`@(?:[\w.]+\.)?SpringBootApplication\b`)
	packageDecl  = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*;`)
	lineComment  = regexp.MustCompile(`//[^\n]*`)
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
)

// Runnable reports the line and fully qualified name of a Java class that
// can be run: one with `public static void main` or `@SpringBootApplication`.
// rel is the project-relative path. The FQCN is the package declaration plus
// the file name without .java; a class in the default package keeps its bare
// name. line is 0-based, for editor.GutterMark.
func Runnable(rel, src string) (line int, fqcn string, ok bool) {
	if path.Ext(rel) != ".java" {
		return 0, "", false
	}
	masked := blankComments(src)
	at := -1
	for i, l := range strings.Split(masked, "\n") {
		if mainMethod.MatchString(l) || springBoot.MatchString(l) {
			at = i
			break
		}
	}
	if at < 0 {
		return 0, "", false
	}
	class := strings.TrimSuffix(path.Base(rel), ".java")
	name := class
	if m := packageDecl.FindStringSubmatch(masked); m != nil {
		name = m[1] + "." + class
	}
	return at, name, true
}

// SpringBoot reports whether src marks the class with the annotation. It
// uses the same comment masking as Runnable, so a commented-out annotation
// does not turn a plain main class into a Spring Boot one.
func SpringBoot(rel, src string) bool {
	if path.Ext(rel) != ".java" {
		return false
	}
	return springBoot.MatchString(blankComments(src))
}

// blankComments replaces comment bytes with spaces, so line and column
// offsets in the rest of the source stay where the editor sees them.
func blankComments(src string) string {
	out := []byte(src)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			if out[i] != '\n' {
				out[i] = ' '
			}
		}
	}
	for _, m := range blockComment.FindAllStringIndex(src, -1) {
		blank(m[0], m[1])
	}
	for _, m := range lineComment.FindAllStringIndex(src, -1) {
		blank(m[0], m[1])
	}
	return string(out)
}
