package runner

import "testing"

func TestLinksFindsStackTraceReferences(t *testing.T) {
	cases := []struct {
		line string
		path string
		ln   int
		col  int
	}{
		{"Foo.java:12", "Foo.java", 12, 0},
		{"Foo.java:12:3", "Foo.java", 12, 3},
		{"com/example/Foo.java:12", "com/example/Foo.java", 12, 0},
		{"at com.example.Foo.main(Foo.java:12)", "Foo.java", 12, 0},
		{"\tat com.example.Foo.run(App.java:7) ~[classes/:na]", "App.java", 7, 0},
	}
	for _, tc := range cases {
		got := Links(tc.line)
		if len(got) != 1 {
			t.Errorf("%q: got %v", tc.line, got)
			continue
		}
		l := got[0]
		if l.Path != tc.path || l.Line != tc.ln || l.Col != tc.col {
			t.Errorf("%q: got %+v, want path %q line %d col %d", tc.line, l, tc.path, tc.ln, tc.col)
		}
		if l.From < 0 || l.To > len(tc.line) || tc.line[l.From:l.To] == "" {
			t.Errorf("%q: offsets %d..%d", tc.line, l.From, l.To)
		}
	}
}

func TestLinksIgnoresURLsAndDriveLetters(t *testing.T) {
	for _, line := range []string{
		"https://example.com/Foo.java",
		"see C:\\src\\App.java for details",
		"https://example.com:8080/thing",
	} {
		if got := Links(line); len(got) != 0 {
			t.Errorf("%q: unexpected %v", line, got)
		}
	}
}

func TestLinksFindsSeveralOnOneLine(t *testing.T) {
	line := "at A.one(A.java:1) at B.two(B.java:2:5)"
	got := Links(line)
	if len(got) != 2 {
		t.Fatalf("got %v", got)
	}
	if got[0].Path != "A.java" || got[0].Line != 1 || got[1].Path != "B.java" || got[1].Col != 5 {
		t.Errorf("got %v", got)
	}
}
