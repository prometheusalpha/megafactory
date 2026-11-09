package main

import (
	"context"
	"slices"
	"strings"
	"time"

	"megafactory/internal/build"

	"github.com/egoist/mygo/ui"
)

// buildView is the Build page's tasks of one project, read when the page
// first shows and kept until the project changes.
type buildView struct {
	// root is the project the tasks are of; a switch starts over.
	root    string
	project build.Project
	err     error
	tasks   []build.Task
	loaded  bool
	loading bool
}

// buildPage lists the project's build tasks: Maven's phases at once,
// Gradle's from its wrapper once, and the command line a task would run.
// Nothing runs here; the runner of Phase 4 does.
func (a *app) buildPage(c *ui.Context, k *colors) {
	v := &a.builds
	if v.root != a.project {
		*v = buildView{root: a.project}
		if a.project != "" {
			v.project, v.err = build.Detect(a.project)
		}
	}
	if a.project == "" || v.project.Kind == build.KindNone {
		note := "No Maven or Gradle build"
		if v.err != nil {
			note = v.err.Error()
		}
		placeholder(c, k, note)
		return
	}
	if !v.loaded && !v.loading {
		a.loadBuild(v)
	}
	kind := "Maven"
	if v.project.Kind == build.KindGradle {
		kind = "Gradle"
	}
	ui.Column(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		ui.Row(c).Height(30).Padding(0, 8, 0, 6).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, kind).FontSize(12).FontWeight(600).TextColor(k.textMuted).SingleLine()
			ui.Spacer(c)
			if iconButton(c, k, "refresh-cw", "Refresh Tasks", 24, 14).Clicked() {
				v.loaded, v.loading, v.err, v.tasks = false, false, nil, nil
			}
		})
		ui.Scroll(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
			ui.Column(c).Padding(0, 6, 0, 6).Gap(1).Children(func() {
				switch {
				case v.loading && !v.loaded:
					buildNote(c, "Loading tasks…", k.textMuted)
					return
				case v.err != nil:
					buildNote(c, v.err.Error(), c.Theme().Danger)
				case len(v.tasks) == 0:
					buildNote(c, "No tasks", k.textMuted)
					return
				}
				for _, task := range v.tasks {
					a.taskRow(c, k, task)
				}
			})
		})
		// The line a task would run, which the run config names.
		if cmd, err := a.buildCommand(v); err != nil {
			ui.Row(c).Height(26).Padding(0, 8, 0, 6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, err.Error()).FontSize(11.5).TextColor(c.Theme().Danger).SingleLine().Ellipsis("…")
			})
		} else if cmd != "" {
			ui.Row(c).Height(26).Padding(0, 8, 0, 6).AlignItems(ui.Center).Children(func() {
				ui.Text(c, cmd).FontSize(11.5).TextColor(k.textMuted).SingleLine().Ellipsis("…")
			})
		}
	})
}

// taskRow is one task, which a click chooses to run.
func (a *app) taskRow(c *ui.Context, k *colors, task build.Task) {
	on := a.runConfig == task.Name
	row := ui.Row(c).Height(26).Gap(10).Padding(0, 8).AlignItems(ui.Center).Radius(8).
		Cursor(ui.CursorPointer).Role(ui.RoleButton).Label(task.Name)
	if on {
		row.Background(k.selected)
	} else if row.Hovered() {
		row.Background(k.hover)
	}
	row.Children(func() {
		ui.Text(c, task.Name).FontSize(12.5).FontWeight(600).TextColor(k.text).SingleLine().Shrink(0)
		ui.Text(c, task.Description).FontSize(11.5).TextColor(k.textFaint).SingleLine().Ellipsis("…")
	})
	row.OnClick(func() {
		a.runConfig = task.Name
	})
}

// buildNote is a page's line of explanation, as no build system, loading,
// or an error.
func buildNote(c *ui.Context, text string, col ui.Color) {
	ui.Text(c, text).FontSize(12.5).TextColor(col).SingleLine().Padding(6)
}

// loadBuild reads the build's tasks: Maven's are static, Gradle's ask its
// wrapper, off the UI thread and with time to.
func (a *app) loadBuild(v *buildView) {
	p, root := v.project, a.project
	v.loading = true
	if p.Kind == build.KindMaven {
		v.tasks, v.err = p.Tasks(context.Background())
		v.loading, v.loaded = false, true
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	a.host.run(func() func() {
		tasks, err := p.Tasks(ctx)
		cancel()
		return func() {
			if a.project != root {
				return
			}
			v.tasks, v.err = tasks, err
			v.loading, v.loaded = false, true
		}
	})
}

// buildCommand is the command line of the task the run config names, when
// it is one of the build's tasks.
func (a *app) buildCommand(v *buildView) (string, error) {
	if v.project.Kind == build.KindNone || a.runConfig == "" {
		return "", nil
	}
	if !slices.ContainsFunc(v.tasks, func(t build.Task) bool { return t.Name == a.runConfig }) {
		return "", nil
	}
	cmd, err := v.project.Command(a.runConfig)
	if err != nil {
		return "", err
	}
	return strings.Join(cmd, " "), nil
}
