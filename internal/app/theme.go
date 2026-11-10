package app

import (
	"fmt"
	"log"
	"strings"

	"github.com/egoist/mygo/plugins/terminal"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// codeTheme is the theme chosen in the settings, a terminal theme in
// Ghostty's format, one for light windows and one for dark, as Ghostty's
// theme = light:A,dark:B. The terminal shows it as it is; the editor's
// syntax colors and the window's colors come from it, so that code looks
// the same in both. Nil for the app's own colors.
type codeTheme struct {
	light, dark *terminal.Theme

	editorLight, editorDark *editor.Theme
	colorsLight, colorsDark *colors
}

var themes codeTheme

// themeChoice is a theme the settings offer, by a spec of bundled themes
// as Ghostty's theme setting: a name, or "light:A,dark:B".
type themeChoice struct{ name, spec string }

// themeChoices are the app's own colors, then Catppuccin's: its flavors,
// and Latte and Mocha as a pair that follows the appearance.
var themeChoices = []themeChoice{
	{"Default", ""},
	{"Catppuccin", "light:Catppuccin Latte,dark:Catppuccin Mocha"},
	{"Catppuccin Latte", "Catppuccin Latte"},
	{"Catppuccin Frappé", "Catppuccin Frappé"},
	{"Catppuccin Macchiato", "Catppuccin Macchiato"},
	{"Catppuccin Mocha", "Catppuccin Mocha"},
}

// bundledThemes are the theme files under assets/themes, from
// github.com/catppuccin/ghostty: the themes Ghostty installs are not all
// there are, nor the latest.
var bundledThemes = map[string]string{
	"Catppuccin Latte":     "catppuccin-latte.conf",
	"Catppuccin Frappé":    "catppuccin-frappe.conf",
	"Catppuccin Macchiato": "catppuccin-macchiato.conf",
	"Catppuccin Mocha":     "catppuccin-mocha.conf",
}

func bundledTheme(name string) (*terminal.Theme, error) {
	file, ok := bundledThemes[name]
	if !ok {
		return nil, fmt.Errorf("no theme %q", name)
	}
	data, err := assets.ReadFile("assets/themes/" + file)
	if err != nil {
		return nil, err
	}
	return terminal.ParseGhosttyTheme(data)
}

var loadedThemes = map[string]codeTheme{}

// themeNamed is the theme of a choice, the app's own colors for an
// unknown one, as a choice saved by a later version.
func themeNamed(name string) codeTheme {
	if th, ok := loadedThemes[name]; ok {
		return th
	}
	var th codeTheme
	for _, ch := range themeChoices {
		if ch.name == name && ch.spec != "" {
			var err error
			if th, err = loadThemes(ch.spec); err != nil {
				log.Printf("theme %s: %v", name, err)
			}
		}
	}
	loadedThemes[name] = th
	return th
}

// loadThemes loads bundled themes as Ghostty's theme setting names them: a
// name, or "light:A,dark:B".
func loadThemes(spec string) (codeTheme, error) {
	var th codeTheme
	if l, d, ok := strings.Cut(spec, ","); ok {
		for _, part := range []string{l, d} {
			kind, name, _ := strings.Cut(strings.TrimSpace(part), ":")
			t, err := bundledTheme(strings.TrimSpace(name))
			if err != nil {
				return codeTheme{}, err
			}
			switch kind {
			case "light":
				th.light = t
			case "dark":
				th.dark = t
			default:
				return codeTheme{}, fmt.Errorf("theme %q: %q is neither light: nor dark:", spec, part)
			}
		}
		if th.light == nil || th.dark == nil {
			return codeTheme{}, fmt.Errorf("theme %q wants a light and a dark theme", spec)
		}
	} else {
		t, err := bundledTheme(spec)
		if err != nil {
			return codeTheme{}, err
		}
		th.light, th.dark = t, t
	}
	th.editorLight, th.editorDark = editorTheme(th.light), editorTheme(th.dark)
	th.colorsLight, th.colorsDark = colorsAround(th.light), colorsAround(th.dark)
	return th, nil
}

func (th codeTheme) pick(dark bool) (*terminal.Theme, *editor.Theme, *colors) {
	if th.light == nil {
		return nil, nil, nil
	}
	if dark {
		return th.dark, th.editorDark, th.colorsDark
	}
	return th.light, th.editorLight, th.colorsLight
}

// The palette of a terminal theme, by ANSI index.
const (
	ansiRed, ansiGreen, ansiYellow, ansiBlue, ansiMagenta, ansiCyan = 1, 2, 3, 4, 5, 6
	ansiBrightBlack                                                 = 8
	ansiBrightRed, ansiBrightYellow, ansiBrightBlue                 = 9, 11, 12
	ansiBrightMagenta, ansiBrightCyan                               = 13, 14
)

// editorTheme colors code with a terminal theme's palette: the theme
// says nothing of syntax, so each kind of token takes the ANSI color
// editors of terminal themes usually give it.
func editorTheme(t *terminal.Theme) *editor.Theme {
	dark := isDark(t.Background)
	base := ui.LightTheme()
	if dark {
		base = ui.DarkTheme()
	}
	et := editor.DefaultTheme(base)
	fg := t.Foreground
	et.Background, et.Text = t.Background, fg
	et.Cursor = fg
	if t.Cursor.A != 0 {
		et.Cursor = t.Cursor
	}
	sel := fg.Alpha(0.2)
	if t.Selection.A != 0 {
		sel = t.Selection.Alpha(0.35)
	}
	et.Selection, et.InactiveSelection = sel, sel.Alpha(0.2)
	et.LineNumber, et.ActiveLineNumber = fg.Alpha(0.35), fg.Alpha(0.85)
	et.ActiveLine = fg.Alpha(0.045)
	et.IndentGuide, et.ActiveIndentGuide, et.WrapGuide = fg.Alpha(0.1), fg.Alpha(0.25), fg.Alpha(0.1)
	et.Whitespace = fg.Alpha(0.25)

	p := t.Palette
	et.Error, et.Warning, et.Info, et.Hint = p[ansiRed], p[ansiYellow], p[ansiBlue], p[ansiBrightBlack]
	et.Added, et.Deleted, et.Modified = p[ansiGreen], p[ansiRed], p[ansiBlue]
	for kind, i := range map[editor.TokenKind]int{
		editor.TokenKeyword:   ansiMagenta,
		editor.TokenOperator:  ansiCyan,
		editor.TokenType:      ansiYellow,
		editor.TokenFunction:  ansiBlue,
		editor.TokenMeta:      ansiBrightMagenta,
		editor.TokenProperty:  ansiBrightBlue,
		editor.TokenConstant:  ansiRed,
		editor.TokenNumber:    ansiRed,
		editor.TokenAttribute: ansiBrightYellow,
		editor.TokenString:    ansiGreen,
		editor.TokenEscape:    ansiBrightCyan,
		editor.TokenRegexp:    ansiBrightCyan,
		editor.TokenTag:       ansiBlue,
		editor.TokenComment:   ansiBrightBlack,
		editor.TokenHeading:   ansiBlue,
		editor.TokenLink:      ansiCyan,
		editor.TokenCode:      ansiGreen,
		editor.TokenInserted:  ansiGreen,
		editor.TokenDeleted:   ansiRed,
		editor.TokenInvalid:   ansiBrightRed,
	} {
		et.Tokens[kind].Color = p[i]
	}
	et.Tokens[editor.TokenComment].Italic = true
	return et
}

// colorsAround are the window's colors around code of a theme, as GoRex's
// colorsAround: the theme's dark or light, whatever the system's, with
// the cards of its background.
func colorsAround(t *terminal.Theme) *colors {
	k, shade := lightColors, float32(0.07)
	if isDark(t.Background) {
		k, shade = darkColors, 0.25
	}
	k.cardFocused = t.Background.Alpha(0.96)
	k.card = t.Background.Alpha(0.78)
	k.editor = t.Background
	// The window's gradient: the theme's background a shade darker than
	// the cards', toward its magenta at the top and its yellow at the
	// bottom.
	black := ui.RGB(0, 0, 0)
	k.bgMid = t.Background.Mix(black, shade)
	k.bgLow = t.Background.Mix(black, shade*1.2)
	k.bgTop, k.bgBottom, k.bgTint = k.bgMid, k.bgLow, ui.Color{}
	if magenta := t.Palette[ansiMagenta]; magenta.A != 0 {
		k.bgTop = k.bgMid.Mix(magenta, 0.1)
		k.bgTint = magenta.Alpha(0.1)
	}
	if yellow := t.Palette[ansiYellow]; yellow.A != 0 {
		k.bgBottom = k.bgLow.Mix(yellow, 0.07)
	}
	return &k
}

func isDark(c ui.Color) bool {
	return 0.2126*float64(c.R)+0.7152*float64(c.G)+0.0722*float64(c.B) < 128
}
