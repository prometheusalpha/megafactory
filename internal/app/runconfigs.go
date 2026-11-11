package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"megafactory/internal/build"
	"megafactory/internal/jdk"
	"megafactory/internal/runcfg"
	"megafactory/internal/runner"
	"megafactory/internal/setup"

	"github.com/egoist/mygo/ui"
)

// runEditState is the Edit Configurations dialog. A draft is edited in
// isolation, so Cancel leaves the stored config alone.
type runEditState struct {
	open    bool
	sel     string
	draft   runcfg.Config
	editing bool
	adding  bool
	err     string
}

func (a *App) runPath() string {
	return filepath.Join(a.project, ".idea", "megafactory_run.json")
}

func (a *App) loadRunConfigs() {
	s, err := runcfg.Load(a.runPath())
	if err != nil {
		a.host.ShowError("Couldn't read megafactory_run.json", err.Error())
	}
	a.runStore = s
	a.runConfig = s.Selected()
	a.mainClasses = nil
}

func (a *App) saveRunConfigs() {
	if a.runStore == nil {
		return
	}
	if err := a.runStore.Save(a.runPath()); err != nil {
		a.host.ShowError("Couldn't save megafactory_run.json", err.Error())
	}
}

// selectRunConfig keeps the toolbar selection and the file's selected id
// in step. A dropdown pick is the only caller that changes just the choice.
func (a *App) selectRunConfig(id string) {
	if a.runConfig == id {
		return
	}
	a.runConfig = id
	a.runStore.Select(id)
	a.saveRunConfigs()
}

// appResolver supplies the facts runcfg.Resolve cannot derive. Its methods
// run off the UI thread, inside host.run's job.
type appResolver struct {
	app     *App
	project string
	srv     langServer
	build   build.Project
}

func (r appResolver) Java(want string) (string, error) {
	installed := jdk.System.Installed()
	if want != "" {
		if n, err := strconv.Atoi(strings.TrimPrefix(want, "JavaSE-")); err == nil {
			for _, j := range installed {
				if j.Major == n {
					return j.Java(), nil
				}
			}
			return "", fmt.Errorf("no JDK %d installed", n)
		}
		candidates := []string{want}
		if filepath.IsAbs(want) {
			candidates = append(candidates, filepath.Join(want, "bin", "java"))
		}
		for _, p := range candidates {
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				return p, nil
			}
		}
		return "", fmt.Errorf("no JDK at %q", want)
	}
	j, _, err := r.app.host.JavaJDK(context.Background(), func(setup.Progress) {})
	if err != nil {
		return "", err
	}
	return j.Java(), nil
}

func (r appResolver) Classpath(mainClass string) (string, []string, []string, error) {
	if r.srv == nil {
		return "", nil, nil, errors.New("the Java language server is not ready")
	}
	classes, err := r.srv.ResolveMainClasses()
	if err != nil {
		return "", nil, nil, err
	}
	project := ""
	for _, c := range classes {
		if c.MainClass == mainClass {
			project = c.ProjectName
			break
		}
	}
	if project == "" && len(classes) > 0 {
		project = classes[0].ProjectName
	}
	modules, classpath, err := r.srv.ResolveClasspath(mainClass, project)
	return project, modules, classpath, err
}

func (r appResolver) Wrapper(string) ([]string, error) { return r.build.Command() }

func (r appResolver) ProjectRoot() string { return r.project }

func (r appResolver) Build() runcfg.BuildSystem {
	switch r.build.Kind {
	case build.KindMaven:
		return runcfg.BuildMaven
	case build.KindGradle:
		return runcfg.BuildGradle
	default:
		return runcfg.BuildNone
	}
}

// runCurrent runs the configuration the toolbar dropdown selected.
func (a *App) runCurrent() {
	if a.runStore == nil {
		return
	}
	cfg, ok := a.runStore.ByID(a.runConfig)
	if !ok {
		a.host.ShowError("No run configuration", "Add one in Edit Configurations.")
		return
	}
	a.startRun(cfg)
}

func (a *App) startRun(cfg runcfg.Config) {
	gen := a.bumpRunGen()
	project, srv := a.project, a.java.srv
	bp, _ := build.Detect(project)
	r := appResolver{app: a, project: project, srv: srv, build: bp}
	a.host.Run(func() func() {
		cmd, err := runcfg.Resolve(cfg, r)
		return func() {
			if gen != a.runGen || a.project != project {
				return
			}
			if err != nil {
				a.host.ShowError("Couldn't run "+cfg.Name, err.Error())
				return
			}
			a.launch(cmd, cfg.Name, gen)
		}
	})
}

func (a *App) bumpRunGen() int {
	a.runGen++
	return a.runGen
}

func (a *App) launch(cmd runcfg.Command, name string, gen int) {
	a.stopProc()
	a.bottomTab = 0
	a.showBottom = true
	a.runConsole = &runConsoleState{name: name}
	proc, err := a.host.StartProc(
		runner.Spec{Argv: cmd.Argv, Dir: cmd.Dir, Env: cmd.Env},
		func(l runner.Line) {
			a.host.Update(func() {
				if gen != a.runGen || a.runConsole == nil {
					return
				}
				a.runConsole.lines = append(a.runConsole.lines,
					consoleLine{text: l.Text, stderr: l.Stream == runner.Stderr})
			})
		},
		func(err error) {
			a.host.Update(func() {
				if gen != a.runGen || a.runConsole == nil {
					return
				}
				a.runProc = nil
				a.runConsole.done = true
				if err != nil {
					a.runConsole.exit = err.Error()
				} else {
					a.runConsole.exit = "exited with code 0"
				}
			})
		},
	)
	if err != nil {
		a.host.ShowError("Couldn't start "+name, err.Error())
		return
	}
	a.runProc = proc
}

// stopProc hangs up the live process without invalidating the generation.
// A new run calls it before installing its own lines.
func (a *App) stopProc() {
	if a.runProc != nil {
		a.runProc.Stop()
		a.runProc = nil
	}
}

// stopRun is the user's Stop button. Bumping runGen drops any trailing
// lines or exit the killed process still delivers.
func (a *App) stopRun() {
	a.stopProc()
	a.runGen++
}

func (a *App) rerun() {
	if cfg, ok := a.runStore.ByID(a.runConfig); ok {
		a.startRun(cfg)
	}
}

// runMain creates a config for a class with main or @SpringBootApplication
// and runs it, as the gutter and the editor's context menu ask.
func (a *App) runMain(rel, fqcn string, spring bool) {
	if a.runStore == nil {
		return
	}
	kind := runcfg.Application
	if spring {
		kind = runcfg.SpringBoot
	}
	cfg, err := a.runStore.Upsert(runcfg.Config{Name: path.Base(fqcn), Kind: kind, MainClass: fqcn})
	if err != nil {
		a.host.ShowError("Couldn't make a run configuration", err.Error())
		return
	}
	a.selectRunConfig(cfg.ID)
	a.startRun(cfg)
}

func (a *App) editConfigurations() {
	a.runEdit = &runEditState{open: true}
	a.loadMainClasses()
}

func (a *App) loadMainClasses() {
	srv, gen := a.java.srv, a.java.gen
	if srv == nil {
		return
	}
	a.host.Run(func() func() {
		cs, err := srv.ResolveMainClasses()
		return func() {
			if a.java.gen == gen && err == nil {
				a.mainClasses = cs
			}
		}
	})
}

func specFor(kind runcfg.Kind) runcfg.Spec {
	for _, s := range runcfg.Specs() {
		if s.Kind == kind {
			return s
		}
	}
	return runcfg.Specs()[0]
}

// runConfigsDialog adds, edits, duplicates and deletes configurations, and
// is the only place a config is written from the UI.
func (a *App) runConfigsDialog(c *ui.Context, k *colors) {
	e := a.runEdit
	if e == nil {
		return
	}
	ui.DialogBase(c.Key("run-configs"), &e.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.28))
		// One size, and clipped: the form scrolls inside it rather than
		// growing the dialog past the window and painting over it.
		panel.Width(680).Height(520).Clip().Padding(12).Gap(10).Radius(14).
			Background(k.dialog).Border(1, k.panelBorder).Shadow(0, 12, 40, 0, k.shadowFocused).
			Label("Run Configurations")
		panel.Children(func() {
			ui.Row(c).Height(30).AlignItems(ui.Center).Children(func() {
				ui.Text(c, "Run Configurations").FontSize(14).FontWeight(600).TextColor(k.text)
				ui.Spacer(c)
				if iconButton(c, k, "x", "Close", 26, 15).Clicked() {
					e.open = false
				}
			})
			ui.Row(c).Grow(1).MinHeight(0).Gap(10).AlignItems(ui.Stretch).Children(func() {
				a.runConfigList(c, k, e)
				a.runConfigForm(c, k, e)
			})
			a.runEditFooter(c, k, e)
		})
	})
	if !e.open {
		a.runEdit = nil
	}
}

// runEditFooter is the dialog's last row: what failed to save, and the
// buttons that keep or drop the draft.
func (a *App) runEditFooter(c *ui.Context, k *colors, e *runEditState) {
	ui.Row(c).Height(30).Shrink(0).Gap(6).AlignItems(ui.Center).Children(func() {
		if e.err != "" {
			ui.Text(c, e.err).FontSize(12).TextColor(c.Theme().Danger).SingleLine().Ellipsis("…")
		}
		ui.Spacer(c)
		if !e.editing {
			return
		}
		if ui.Button(c, "Save").Clicked() {
			a.saveRunDraft(e)
		}
		if ui.Button(c, "Cancel").Clicked() {
			e.editing, e.err = false, ""
		}
	})
}

// saveRunDraft writes the draft, and keeps the dialog on the saved
// configuration, or on nothing when it could not be saved.
func (a *App) saveRunDraft(e *runEditState) {
	cfg, err := a.runStore.Upsert(e.draft)
	if err != nil {
		e.err = err.Error()
		return
	}
	e.sel, e.editing, e.err = cfg.ID, false, ""
	if a.runConfig == "" {
		a.selectRunConfig(cfg.ID)
	} else {
		a.saveRunConfigs()
	}
}

func (a *App) runConfigList(c *ui.Context, k *colors, e *runEditState) {
	ui.Column(c).Width(230).MinHeight(0).Gap(2).Children(func() {
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			for _, cfg := range a.runStore.All() {
				c.Key(cfg.ID)
				on := cfg.ID == e.sel
				row := ui.Row(c).Height(28).Padding(0, 8).AlignItems(ui.Center).Radius(8).
					Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(cfg.Name)
				if on {
					row.Background(k.selected)
				} else if row.Hovered() {
					row.Background(k.hover)
				}
				row.Children(func() {
					ui.Icon(c, icon("play")).Size(12, 12).TextColor(k.iconMuted)
					ui.Text(c, cfg.Name).FontSize(12.5).TextColor(k.text).SingleLine()
				})
				row.OnClick(func() { e.sel, e.editing, e.adding, e.err = cfg.ID, false, false, "" })
			}
		})
		ui.Row(c).Gap(4).Children(func() {
			if ui.Button(c, "Add").Clicked() {
				e.adding, e.editing, e.err = true, false, ""
			}
			if ui.Button(c, "Duplicate").Clicked() && e.sel != "" {
				if _, err := a.runStore.Duplicate(e.sel); err == nil {
					a.saveRunConfigs()
				}
			}
			if ui.Button(c, "Delete").Clicked() && e.sel != "" {
				a.runStore.Delete(e.sel)
				a.runConfig = a.runStore.Selected()
				e.sel, e.editing, e.err = "", false, ""
				a.saveRunConfigs()
			}
		})
	})
}

func (a *App) runConfigForm(c *ui.Context, k *colors, e *runEditState) {
	ui.Column(c).Grow(1).MinHeight(0).Gap(6).FillWidth().Children(func() {
		switch {
		case e.adding:
			ui.Text(c, "New configuration").FontSize(12).TextColor(k.textMuted)
			for _, sp := range runcfg.Specs() {
				c.Key(string(sp.Kind))
				row := ui.Row(c).Height(28).Padding(0, 8).AlignItems(ui.Center).Radius(8).
					Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(sp.Label)
				if row.Hovered() {
					row.Background(k.hover)
				}
				row.Children(func() { ui.Text(c, sp.Label).FontSize(12.5).TextColor(k.text) })
				row.OnClick(func() {
					e.draft = runcfg.Config{Kind: sp.Kind, Name: "New " + sp.Label}
					e.adding, e.editing, e.err = false, true, ""
				})
			}
		case e.editing:
			a.runConfigFields(c, k, e)
		default:
			if cfg, ok := a.runStore.ByID(e.sel); ok {
				ui.Text(c, cfg.Name).FontSize(13).FontWeight(600).TextColor(k.text)
				ui.Text(c, specFor(cfg.Kind).Label).FontSize(12).TextColor(k.textMuted)
				if ui.Button(c, "Edit").Clicked() {
					e.draft, e.editing, e.err = cfg, true, ""
				}
			} else {
				ui.Text(c, "Select a configuration, or add one.").FontSize(12).TextColor(k.textMuted)
			}
		}
	})
}

// fieldLook gives a field the chrome a dialog needs: the Base widgets come
// without any, which would leave a dialog's fields invisible on its face.
func fieldLook(k *colors, e ui.Element) ui.Element {
	return e.Background(k.hover).Border(0.5, k.panelBorder).Radius(6)
}

func (a *App) runConfigFields(c *ui.Context, k *colors, e *runEditState) {
	ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Padding(0, 14, 0, 0).Children(func() {
		ui.Column(c).Gap(6).FillWidth().Children(func() {
			ui.Text(c, "Name").FontSize(11.5).TextColor(k.textMuted)
			fieldLook(k, ui.TextInputBase(c, &e.draft.Name).Label("Name").FontSize(12.5).TextColor(k.text).
				FillWidth().Height(26).Padding(0, 8))
			for _, f := range specFor(e.draft.Kind).Fields {
				ui.Text(c, f.Label).FontSize(11.5).TextColor(k.textMuted)
				switch {
				case f.Pick:
					sel := ui.SelectBase(c, f.Ptr(&e.draft))
					fieldLook(k, sel.Trigger.Row().Height(26).FillWidth().Padding(0, 8).
						Label(f.Label).Children(func() {
						ui.Text(c, *f.Ptr(&e.draft)).FontSize(12.5).TextColor(k.text).SingleLine()
					}))
					sel.Popup(func(panel ui.Element) {
						panel.Padding(4).MinWidth(240).Radius(10).Background(k.panel).Border(1, k.panelBorder)
						for _, m := range a.mainClasses {
							item := sel.Item(m.MainClass).Row().Height(26).Padding(0, 8).Radius(6)
							if item.Highlighted() {
								item.Background(k.selected)
							}
							item.Children(func() { ui.Text(c, m.MainClass).FontSize(12.5).TextColor(k.text).SingleLine() })
						}
					})
				case f.Multi:
					fieldLook(k, ui.TextAreaBase(c, f.Ptr(&e.draft)).Label(f.Label).FontSize(12.5).TextColor(k.text).
						Height(56).FillWidth().Padding(6, 8))
				default:
					fieldLook(k, ui.TextInputBase(c, f.Ptr(&e.draft)).Label(f.Label).FontSize(12.5).TextColor(k.text).
						FillWidth().Height(26).Padding(0, 8))
				}
			}
		})
	})
}
