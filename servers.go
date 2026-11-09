package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"slices"
	"time"

	"megafactory/internal/setup"
	"megafactory/internal/workspace"

	"github.com/egoist/mygo/ui"
)

// The phases of the servers' install.
const (
	serversIdle = iota
	serversDownloading
	serversReady
	serversFailed
)

// servers is the install of jdtls and java-debug, which the first Java
// project downloads, in the background and without asking.
type servers struct {
	phase    int
	progress setup.Progress
	err      error
	// justReady shows the end of a download for a moment, which the
	// status bar says, then forgets.
	justReady bool

	// artifacts are what to install: none in newApp, so that tests
	// download nothing unless they serve it; main's are setup.Artifacts.
	artifacts []setup.Artifact
	client    *http.Client
}

func serversDir() string { return filepath.Join(dataDir, "servers") }

// javaProject reports whether a project's top folder is a Java one's:
// Maven's or Gradle's, or Eclipse's.
func javaProject(entries []workspace.Entry) bool {
	return slices.ContainsFunc(entries, func(e workspace.Entry) bool {
		switch e.Name {
		case "pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle", "settings.gradle.kts", ".classpath":
			return !e.Dir
		}
		return false
	})
}

// ensureServers installs the servers, unless they are or are being.
func (a *app) ensureServers() {
	s := &a.servers
	if s.phase == serversDownloading || s.phase == serversReady {
		return
	}
	s.phase, s.err, s.progress = serversDownloading, nil, setup.Progress{}
	arts, client, root, update, after := s.artifacts, s.client, serversDir(), a.host.update, a.host.after
	a.host.run(func() func() {
		var err error
		for _, art := range arts {
			// A redraw per percent, not per read.
			shown := int64(-1)
			_, err = setup.Ensure(context.Background(), client, root, art, func(p setup.Progress) {
				if pct := percent(p); pct != shown {
					shown = pct
					update(func() { s.progress = p })
				}
			})
			if err != nil {
				break
			}
		}
		return func() {
			if err != nil {
				log.Printf("servers: %v", err)
				s.phase, s.err = serversFailed, err
				return
			}
			s.phase = serversReady
			// Installed before, it says nothing.
			if s.progress.Done > 0 {
				s.justReady = true
				after(4*time.Second, func() { s.justReady = false })
			}
		}
	})
}

func percent(p setup.Progress) int64 {
	if p.Total <= 0 {
		return p.Done >> 20 // MB, while the size is unknown
	}
	return p.Done * 100 / p.Total
}

// serversStatus shows the install on the status bar: plainly while it
// downloads, with its progress, as it ends, and with a retry when it
// failed; quietly once installed.
func (a *app) serversStatus(c *ui.Context, k *colors) {
	s := &a.servers
	th := c.Theme()
	faint := func(t string) ui.Element { return ui.Text(c, t).FontSize(11.5).TextColor(k.textFaint).SingleLine() }
	loud := func(iconName string, col ui.Color, t string) ui.Element {
		var e ui.Element
		ui.Row(c).Gap(5).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, icon(iconName)).Size(12, 12).TextColor(col)
			e = ui.Text(c, t).FontSize(11.5).FontWeight(600).TextColor(k.text).SingleLine()
		})
		return e
	}
	switch s.phase {
	case serversIdle:
		faint("jdtls: not started")
	case serversDownloading:
		p := s.progress
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			switch {
			case p.Name == "":
				loud("download", th.Accent, "Preparing jdtls…")
			case p.Total > 0:
				loud("download", th.Accent, fmt.Sprintf("Downloading %s %d%%", p.Name, percent(p)))
				progressBar(c, k, th.Accent, float32(p.Done)/float32(p.Total))
			default:
				loud("download", th.Accent, fmt.Sprintf("Downloading %s %d MB", p.Name, percent(p)))
			}
		})
	case serversReady:
		if s.justReady {
			loud("circle-check", th.Success, "jdtls ready")
		} else {
			faint("jdtls: installed")
		}
	case serversFailed:
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			loud("circle-alert", th.Danger, "jdtls download failed").Tooltip(s.err.Error())
			retry := ui.Text(c, "Retry").FontSize(11.5).FontWeight(600).TextColor(th.Accent).Underline().
				Cursor(ui.CursorPointer).Role(ui.RoleButton).Label("Retry jdtls download")
			retry.OnClick(a.ensureServers)
		})
	}
}

// progressBar is a thin bar filled to done, a share of 1.
func progressBar(c *ui.Context, k *colors, col ui.Color, done float32) {
	const w = 80
	ui.Box(c).Size(w, 4).Radius(2).Background(k.text.Alpha(0.12)).Role(ui.RoleProgress).Label("Download progress").Children(func() {
		ui.Box(c).Size(w*min(max(done, 0), 1), 4).Radius(2).Background(col)
	})
}
