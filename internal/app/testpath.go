package app

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// isTestPath is whether a project-relative path is in a test source root,
// src/test as Maven and Gradle lay it out, which is what IntelliJ greens.
// A folder counts too, so the root itself is tinted down to its files.
func isTestPath(rel string) bool {
	return strings.HasPrefix(rel, "src/test/") || rel == "src/test" ||
		strings.Contains(rel, "/src/test/") || strings.HasSuffix(rel, "/src/test")
}

// testTint is the row background of a test file, the theme's green kept
// faint, and drawn with square corners so that neighbouring rows join into one block.
func testTint(c *ui.Context) ui.Color {
	return c.Theme().Success.Alpha(0.07)
}
