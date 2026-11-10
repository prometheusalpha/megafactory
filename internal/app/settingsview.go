package app

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// settingsPage is the settings tab. A change takes effect and is kept at
// once, as macOS settings are, without an Apply.
func (a *App) settingsPage(c *ui.Context, k *colors) {
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Column(c).Padding(20, 28).Gap(22).MaxWidth(680).Children(func() {
			ui.Text(c, "Settings").FontSize(20).FontWeight(700).TextColor(k.text)
			section(c, k, "Appearance", "Follow macOS, or keep the window light or dark.", func() {
				a.appearancePicker(c, k)
			})
			section(c, k, "Theme", "Colors of the editor and the window. Default is megafactory's own.", func() {
				ui.Column(c).Gap(2).Label("Theme").Children(func() {
					for _, ch := range themeChoices {
						a.themeRow(c, k, ch)
					}
				})
			})
			section(c, k, "Project tree", "How the sidebar shows folders.", func() {
				if ui.Checkbox(c, &prefs.CompactFolders, "Compact middle folders (a/b/c)").Changed() {
					saveSettings()
				}
			})
			section(c, k, "Java", "What the language server shows in the editor.", func() {
				cb := ui.Checkbox(c, &prefs.InlineErrors, "Show problems inline")
				if cb.Changed() {
					saveSettings()
					a.applyInlineSettings()
				}
			})
			section(c, k, "Git", "Blame annotations in the editor.", func() {
				inline := ui.Checkbox(c, &prefs.InlineBlame, "Show blame on the caret's line")
				if inline.Changed() {
					saveSettings()
					a.applyBlameSettings()
				}
				gutter := ui.Checkbox(c, &prefs.GutterBlame, "Show blame in the left gutter")
				if gutter.Changed() {
					saveSettings()
					a.applyBlameSettings()
				}
			})
			a.keymapSection(c, k)
		})
	})
}

func section(c *ui.Context, k *colors, title, hint string, body func()) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Column(c).Gap(3).Children(func() {
			ui.Text(c, title).FontSize(14).FontWeight(600).TextColor(k.text)
			ui.Text(c, hint).FontSize(12.5).TextColor(k.textMuted)
		})
		body()
	})
}

// appearancePicker is a segmented control, a track as the tabs'.
func (a *App) appearancePicker(c *ui.Context, k *colors) {
	track(k, ui.Row(c)).AlignSelf(ui.Start).Role(ui.RoleRadioGroup).Label("Appearance").Children(func() {
		for _, opt := range []struct{ source, label string }{{"system", "System"}, {"light", "Light"}, {"dark", "Dark"}} {
			c.Key(opt.source)
			on := prefs.Appearance == opt.source
			b := trackTab(k, ui.Row(c), on).Padding(0, 14).Cursor(ui.CursorPointer).Role(ui.RoleRadio).Label(opt.label).Checked(on)
			b.Children(func() {
				ui.Text(c, opt.label).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine()
			})
			b.OnClick(func() { a.setAppearance(opt.source) })
		}
	})
}

func (a *App) setAppearance(source string) {
	prefs.Appearance = source
	a.host.SetAppearance(source)
	saveSettings()
}

// themeRow is a theme to pick, with a swatch of its code as the window's
// appearance shows it.
func (a *App) themeRow(c *ui.Context, k *colors, ch themeChoice) {
	c.Key(ch.name)
	on := prefs.Theme == ch.name
	row := ui.Row(c).Height(40).Padding(0, 10).Gap(12).Radius(10).AlignItems(ui.Center).
		Cursor(ui.CursorPointer).Role(ui.RoleRadio).Label(ch.name).Checked(on)
	if on {
		row.Background(k.selected)
	} else if row.Hovered() {
		row.Background(k.hover)
	}
	et := swatchTheme(c, ch.name)
	row.Children(func() {
		ui.Row(c).Size(64, 26).Padding(0, 9).Gap(5).Radius(7).AlignItems(ui.Center).Shrink(0).
			Background(et.Background).Border(1, k.panelBorder).Children(func() {
			for _, kind := range []editor.TokenKind{editor.TokenKeyword, editor.TokenType, editor.TokenFunction, editor.TokenString} {
				ui.Box(c).Size(8, 8).Radius(4).Background(et.Tokens[kind].Color)
			}
		})
		ui.Text(c, ch.name).FontSize(13).TextColor(k.text).SingleLine()
	})
	row.OnClick(func() { a.setTheme(ch.name) })
}

func swatchTheme(c *ui.Context, name string) *editor.Theme {
	if _, et, _ := themeNamed(name).pick(c.Theme().Dark); et != nil {
		return et
	}
	k := &lightColors
	if c.Theme().Dark {
		k = &darkColors
	}
	et := editor.DefaultTheme(c.Theme())
	et.Background = k.editor
	return et
}

func (a *App) setTheme(name string) {
	prefs.Theme = name
	themes = themeNamed(name)
	saveSettings()
}
