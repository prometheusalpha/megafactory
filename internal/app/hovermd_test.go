package app

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestHoverMarkdownDropsJdtLinkURL(t *testing.T) {
	in := "x\n\nSource: *[Java 21 (module: java.base)](jdt://contents/java.base/UUID.java?=a/%5C/b#492)*"
	want := "x\n\nSource: *Java 21 (module: java.base)*"
	if got := hoverMarkdown(in); got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if got := hoverMarkdown("[doc](https://x.dev/a)"); got != "[doc](https://x.dev/a)" {
		t.Fatalf("http link changed: %q", got)
	}
}

func TestHoverBuildScrollsLongText(t *testing.T) {
	long := strings.Repeat("line of javadoc\n\n", 200)
	tr := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Key("host").Children(func() {
			hoverBuild(long)(c)
			ui.Text(c, "END")
		})
	}, 400, 1200)
	tr.Frame()
	r, ok := tr.Find("END")
	if !ok {
		t.Fatal("END not found")
	}
	// 200 paragraphs are thousands of DIPs tall: END sits right under the cap.
	if r.Y <= 0 || r.Y > 450 {
		t.Fatalf("END at y=%v, want the hover capped under 450", r.Y)
	}
}
