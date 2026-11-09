package main

import (
	"embed"
	"path/filepath"
	"time"

	"github.com/egoist/mygo/ui"
)

// The look follows GoRex's (../gorex): a gradient window, panes as soft
// translucent cards with a hairline rim and a shadow, capsule tab tracks
// and borderless Lucide icon buttons. Unlike GoRex, the sidebar is a card
// of its own, as IntelliJ's islands.
const (
	gap     = 8  // between cards, and around them
	titleH  = 44 // the title bar
	cardR   = 12 // the cards' corners
	headerH = 38 // the cards' headers
	tabH    = 26
)

type colors struct {
	bgTop, bgMid, bgLow, bgBottom, bgTint ui.Color

	card, cardFocused, cardBorder, cardBorderFocused ui.Color
	shadow, shadowFocused                            ui.Color
	// editor is the editor card's face: opaque, as the editor's diff colors
	// are mixed from its background.
	editor ui.Color

	text, textMuted, textFaint, iconMuted ui.Color
	hover, pressed, selected              ui.Color

	track, trackBorder, tabActive ui.Color

	panel, panelBorder ui.Color
}

var lightColors = colors{
	bgTop:    ui.Hex("#e7ebf8"),
	bgMid:    ui.Hex("#e3e7ef"),
	bgLow:    ui.Hex("#e0e6ea"),
	bgBottom: ui.Hex("#d9e6e6"),
	bgTint:   ui.RGBA(214, 224, 242, 0.35),

	card:              ui.RGBA(255, 255, 255, 0.66),
	cardFocused:       ui.RGBA(255, 255, 255, 0.95),
	cardBorder:        ui.RGBA(255, 255, 255, 0.72),
	cardBorderFocused: ui.RGBA(255, 255, 255, 1),
	shadow:            ui.RGBA(60, 40, 50, 0.07),
	shadowFocused:     ui.RGBA(60, 40, 50, 0.12),
	editor:            ui.Hex("#fcfcfd"),

	text:      ui.Hex("#1d1d1f"),
	textMuted: ui.Hex("#636366"),
	textFaint: ui.Hex("#8e8e93"),
	iconMuted: ui.Hex("#8a8a8f"),
	hover:     ui.RGBA(0, 0, 0, 0.055),
	pressed:   ui.RGBA(0, 0, 0, 0.1),
	selected:  ui.RGBA(46, 111, 208, 0.12),

	track:       ui.RGBA(255, 255, 255, 0.34),
	trackBorder: ui.RGBA(255, 255, 255, 0.45),
	tabActive:   ui.RGBA(255, 255, 255, 0.97),

	panel:       ui.RGBA(252, 252, 253, 0.97),
	panelBorder: ui.RGBA(0, 0, 0, 0.08),
}

var darkColors = colors{
	bgTop:    ui.Hex("#21283a"),
	bgMid:    ui.Hex("#1e2229"),
	bgLow:    ui.Hex("#1e2124"),
	bgBottom: ui.Hex("#1a2627"),
	bgTint:   ui.RGBA(40, 55, 90, 0.25),

	card:              ui.RGBA(20, 20, 24, 0.5),
	cardFocused:       ui.RGBA(36, 36, 41, 0.94),
	cardBorder:        ui.RGBA(255, 255, 255, 0.05),
	cardBorderFocused: ui.RGBA(255, 255, 255, 0.14),
	shadow:            ui.RGBA(0, 0, 0, 0.25),
	shadowFocused:     ui.RGBA(0, 0, 0, 0.4),
	editor:            ui.Hex("#232328"),

	text:      ui.Hex("#f2f2f7"),
	textMuted: ui.Hex("#aeaeb2"),
	textFaint: ui.Hex("#8e8e93"),
	iconMuted: ui.Hex("#98989d"),
	hover:     ui.RGBA(255, 255, 255, 0.08),
	pressed:   ui.RGBA(255, 255, 255, 0.14),
	selected:  ui.RGBA(108, 178, 255, 0.18),

	track:       ui.RGBA(255, 255, 255, 0.06),
	trackBorder: ui.RGBA(255, 255, 255, 0.06),
	tabActive:   ui.RGBA(255, 255, 255, 0.14),

	panel:       ui.RGBA(38, 38, 42, 0.97),
	panelBorder: ui.RGBA(255, 255, 255, 0.1),
}

func colorsOf(c *ui.Context) *colors {
	if _, _, k := themes.pick(c.Theme().Dark); k != nil {
		return k
	}
	if c.Theme().Dark {
		return &darkColors
	}
	return &lightColors
}

// paintBackground paints the window: a gradient from top through mid to
// bottom, and a tint toward the right.
func paintBackground(p *ui.Painter, r ui.Rect, k *colors) {
	h1, h2 := r.H*0.38, r.H*0.32
	p.FillGradient(ui.Rect{X: r.X, Y: r.Y, W: r.W, H: h1 + 1}, ui.LinearGradient{From: k.bgTop, To: k.bgMid, Angle: 180}, 0)
	p.FillGradient(ui.Rect{X: r.X, Y: r.Y + h1, W: r.W, H: h2 + 1}, ui.LinearGradient{From: k.bgMid, To: k.bgLow, Angle: 180}, 0)
	p.FillGradient(ui.Rect{X: r.X, Y: r.Y + h1 + h2, W: r.W, H: r.H - h1 - h2}, ui.LinearGradient{From: k.bgLow, To: k.bgBottom, Angle: 180}, 0)
	clear := k.bgTint
	clear.A = 0
	p.FillGradient(r, ui.LinearGradient{From: clear, To: k.bgTint, Angle: 90, Start: 0.35, End: 1}, 0)
}

// card is an island: brighter, with a stronger rim and shadow, while it
// holds the user's attention.
func card(c *ui.Context, k *colors, focused bool) ui.Element {
	e := ui.Column(c).Radius(cardR).Clip().MinWidth(0).MinHeight(0)
	bg, border, shadow := k.card, k.cardBorder, k.shadow
	if focused {
		bg, border, shadow = k.cardFocused, k.cardBorderFocused, k.shadowFocused
	}
	return e.Background(bg).Border(1, border).
		Shadow(0, 1, 2, 0, shadow).
		Shadow(0, 6, 22, -2, shadow).
		Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
}

//go:embed assets/icons/*.svg assets/fonts/*.ttf assets/themes/*.conf
var assets embed.FS

var svgs = map[string]*ui.SVG{}

func icon(name string) *ui.SVG {
	if s, ok := svgs[name]; ok {
		return s
	}
	data, err := assets.ReadFile("assets/icons/" + name + ".svg")
	if err != nil {
		panic(err)
	}
	s := ui.MustParseSVG(data)
	svgs[name] = s
	return s
}

// iconButton is a borderless button of an icon, with a face on hover;
// label names it for screen readers and its tooltip.
func iconButton(c *ui.Context, k *colors, name, label string, size, iconSize float32) ui.Element {
	b := ui.Box(c).Size(size, size).Center().Radius(size / 2.6).Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(label).Tooltip(label)
	if b.Pressed() {
		b.Background(k.pressed)
	} else if b.Hovered() {
		b.Background(k.hover)
	}
	b.Transition(ui.ElementTransition{Colors: true, Duration: 120 * time.Millisecond})
	col := k.iconMuted
	if b.Hovered() {
		col = k.text
	}
	b.Children(func() {
		ui.Icon(c, icon(name)).Size(iconSize, iconSize).TextColor(col)
	})
	return b
}

// track styles a tab list as the capsule that holds its tabs. It styles
// TabsBase's own List rather than wrapping it: TabsBase places the list
// where it is called, so a wrapper built after it would stay empty.
func track(k *colors, list ui.Element) ui.Element {
	return list.Row().Height(tabH+4).Padding(2).Gap(2).Radius((tabH+4)/2).Shrink(0).
		Background(k.track).Border(0.5, k.trackBorder).AlignItems(ui.Center)
}

// trackTab styles a tab inside a track: the active one raised, as a pill.
func trackTab(k *colors, e ui.Element, active bool) ui.Element {
	e = e.Row().Height(tabH).Gap(6).AlignItems(ui.Center).Radius(tabH / 2).Shrink(0)
	if active {
		e.Background(k.tabActive).Shadow(0, 1, 2, 0, k.shadow).Shadow(0, 2, 8, 0, k.shadow)
	} else if e.Hovered() {
		e.Background(k.hover)
	}
	return e.Transition(ui.ElementTransition{Colors: true, Duration: 160 * time.Millisecond})
}

// fileIcon picks a file's icon by its kind.
func fileIcon(name string) string {
	switch filepath.Ext(name) {
	case ".java", ".xml", ".kt", ".gradle":
		return "file-code"
	case ".json":
		return "file-json"
	case ".md":
		return "file-text"
	}
	return "file"
}
