package runcfg

import "strings"

// EnvVar is one KEY=value of a config's Environment. Config.Env stays the
// newline-joined text the runner reads, so the editor and old files agree.
type EnvVar struct{ Name, Value string }

// ParseEnv reads Env text, one KEY=value per line. A line without "=" keeps
// its text as a name, so the editor shows what the user typed.
func ParseEnv(s string) []EnvVar {
	var out []EnvVar
	for _, l := range envLines(s) {
		k, v, _ := strings.Cut(l, "=")
		out = append(out, EnvVar{k, v})
	}
	return out
}

// JoinEnv is ParseEnv's inverse. A row with no name is not a variable a
// process could have, so it is left out.
func JoinEnv(vars []EnvVar) string {
	var lines []string
	for _, v := range vars {
		if n := strings.TrimSpace(v.Name); n != "" {
			lines = append(lines, n+"="+v.Value)
		}
	}
	return strings.Join(lines, "\n")
}

// EnvSemicolons is the "A=1;B=2" form other tools (IntelliJ) copy and paste.
func EnvSemicolons(vars []EnvVar) string {
	return strings.ReplaceAll(JoinEnv(vars), "\n", ";")
}

// ParseEnvText reads pasted text: entries split on ";" or a line break. A
// value that holds ";" cannot survive this, which is the cost of the format.
func ParseEnvText(s string) []EnvVar {
	s = strings.NewReplacer("\r\n", "\n", ";", "\n").Replace(s)
	return ParseEnv(s)
}

// MergeEnv sets each of add over vars by name, and appends the new ones.
func MergeEnv(vars, add []EnvVar) []EnvVar {
	for _, a := range add {
		found := false
		for i := range vars {
			if vars[i].Name == a.Name {
				vars[i].Value, found = a.Value, true
				break
			}
		}
		if !found {
			vars = append(vars, a)
		}
	}
	return vars
}
