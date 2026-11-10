package keymap

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestParseRoundTrip(t *testing.T) {
	for _, a := range Actions {
		s := a.Default.String()
		got, ok := Parse(s)
		if !ok || got != a.Default {
			t.Errorf("%s: %q parsed as %v, %v", a.ID, s, got, ok)
		}
	}
	for _, bad := range []string{"", "cmd+", "hyper+w", "cmd+shift", "w+cmd"} {
		if b, ok := Parse(bad); ok {
			t.Errorf("Parse(%q) = %v, want a failure", bad, b)
		}
	}
}

func TestDefaultsDoNotConflict(t *testing.T) {
	k := New(nil)
	for _, a := range Actions {
		if c := k.Conflict(a.ID, a.Default); c != "" {
			t.Errorf("%s and %s share %s", a.ID, c, a.Default)
		}
	}
}

func TestSetKeepsOnlyChanges(t *testing.T) {
	k := New(nil)
	b := Binding{ui.Cmd, ui.KeyE}
	if c := k.Set("findFile", b); c != "" {
		t.Fatalf("Set conflicted with %s", c)
	}
	if k.Binding("findFile") != b {
		t.Errorf("findFile = %v, want %v", k.Binding("findFile"), b)
	}
	if got := k.Overrides(); len(got) != 1 || got["findFile"] != b.String() {
		t.Errorf("overrides = %v", got)
	}
	k.Reset("findFile")
	if k.Overrides() != nil {
		t.Errorf("overrides after reset = %v", k.Overrides())
	}
}

func TestSetRefusesAKeyInUse(t *testing.T) {
	k := New(nil)
	if c := k.Set("findFile", k.Binding("closeTab")); c != "closeTab" {
		t.Errorf("conflict = %q, want closeTab", c)
	}
	if k.Binding("findFile").Key != ui.KeyP {
		t.Error("a refused Set changed the binding")
	}
}

func TestUnbound(t *testing.T) {
	k := New(map[string]string{"closeTab": ""})
	if !k.Binding("closeTab").Zero() {
		t.Error("closeTab should be unbound")
	}
	// ⌘W is free now.
	if c := k.Set("findFile", Binding{ui.Cmd, ui.KeyW}); c != "" {
		t.Errorf("conflict %q for a key nothing holds", c)
	}
}

func TestNewDropsBadOverrides(t *testing.T) {
	k := New(map[string]string{
		"nope":           "cmd+x",
		"findFile":       "garbage",
		"findInFiles":    "cmd+w", // closeTab's
		"closeOtherTabs": "cmd+alt+w",
	})
	want := map[string]string{"closeOtherTabs": "cmd+alt+w"}
	got := k.Overrides()
	if len(got) != 1 || got["closeOtherTabs"] != want["closeOtherTabs"] {
		t.Errorf("overrides = %v, want %v", got, want)
	}
}
