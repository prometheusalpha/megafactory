// Package session keeps the files each project had open, to open them
// again with the project.
package session

import "slices"

// Project is the tabs of one project: its files in tab order, and the one
// that was active, "" when it was no file.
type Project struct {
	Files  []string `json:"files"`
	Active string   `json:"active,omitempty"`
}

func (p Project) Equal(q Project) bool {
	return p.Active == q.Active && slices.Equal(p.Files, q.Files)
}

// Store is the projects' sessions by the project's path.
type Store map[string]Project

// With is s with the session of project replaced. A project left with no
// file is dropped, so the store holds only what there is to restore.
func (s Store) With(project string, p Project) Store {
	out := make(Store, len(s)+1)
	for k, v := range s {
		out[k] = v
	}
	if len(p.Files) == 0 {
		delete(out, project)
	} else {
		out[project] = p
	}
	return out
}
