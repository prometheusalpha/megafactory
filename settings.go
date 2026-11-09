package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
)

// settings are the app's own preferences, kept in settings.json as the
// settings page changes them.
type settings struct {
	// EditorLineHeight is the height of the editor's lines as a multiple
	// of the font's size. Ghostty's adjust-cell-height sizes the
	// terminal's rows alone: rows roomy enough for a terminal read too
	// sparse as code.
	EditorLineHeight float32 `json:"editorLineHeight"`
	// Appearance is "system", "light" or "dark", as mygo.ThemeSource.
	Appearance string `json:"appearance"`
	// Theme names one of themeChoices.
	Theme string `json:"theme"`
	// InlineErrors shows each problem's message at the end of its line,
	// as an inlay, rather than in the hover alone.
	InlineErrors bool `json:"inlineErrors"`
}

var defaultSettings = settings{EditorLineHeight: 1.5, Appearance: "system", Theme: "Default", InlineErrors: true}

var prefs = defaultSettings

// windowState is what the window shows, kept in state.json to open as the
// user left it. Its frame is MyGo's, by the window's StateKey.
type windowState struct {
	Project     string  `json:"project"`
	ShowSidebar bool    `json:"showSidebar"`
	SidebarW    float32 `json:"sidebarWidth"`
	ShowBottom  bool    `json:"showBottom"`
	BottomH     float32 `json:"bottomHeight"`
}

var defaultState = windowState{ShowSidebar: true, SidebarW: sidebarDefault, ShowBottom: true, BottomH: bottomDefault}

// dataDir holds the app's files. It is not MyGo's PathUserData, which
// takes the executable's name under go run.
var dataDir = func() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "megafactory")
}()

// readJSON reads a file of dataDir over v, which holds the defaults: a
// missing file, or a key it lacks, leaves them.
func readJSON(name string, v any) {
	data, err := os.ReadFile(filepath.Join(dataDir, name))
	if errors.Is(err, fs.ErrNotExist) {
		return
	}
	if err == nil {
		err = json.Unmarshal(data, v)
	}
	if err != nil {
		log.Printf("%s: %v", name, err)
	}
}

// writeJSON replaces a file of dataDir whole, so that a crash midway
// leaves the old one.
func writeJSON(name string, v any) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		log.Printf("%s: %v", name, err)
		return
	}
	path := filepath.Join(dataDir, name)
	err = os.MkdirAll(dataDir, 0o755)
	if err == nil {
		err = os.WriteFile(path+".tmp", append(data, '\n'), 0o644)
	}
	if err == nil {
		err = os.Rename(path+".tmp", path)
	}
	if err != nil {
		log.Printf("%s: %v", name, err)
	}
}

func loadSettings() settings {
	s := defaultSettings
	readJSON("settings.json", &s)
	return s
}

func saveSettings() { writeJSON("settings.json", prefs) }

func loadState() windowState {
	st := defaultState
	readJSON("state.json", &st)
	return st
}

func saveState(st windowState) { writeJSON("state.json", st) }
