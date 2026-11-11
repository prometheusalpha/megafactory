package app

import (
	"regexp"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

// jdtLink matches a link into a jdtls virtual document. The editor can't
// open one, and its URL is long and escaped, so the hover keeps the label.
var jdtLink = regexp.MustCompile(`\[([^\]]*)\]\(jdt://[^)]*\)`)

// hoverMarkdown prepares a language server's hover text for markdown.View.
func hoverMarkdown(text string) string {
	return jdtLink.ReplaceAllString(text, "$1")
}

// hoverMaxHeight caps a hover in theme spaces; a longer one scrolls, as the
// javadoc of a big method would otherwise cover the editor.
const hoverMaxHeight = 90

// hoverBuild builds the content of a hover that shows text as markdown.
func hoverBuild(text string) func(c *ui.Context) {
	src := hoverMarkdown(text)
	return func(c *ui.Context) {
		ui.Scroll(c).Key("hover scroll").MaxHeight(c.Theme().Space(hoverMaxHeight)).Children(func() {
			markdown.View(c, src, nil)
		})
	}
}
