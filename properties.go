package main

import (
	"path"
	"strings"

	"github.com/egoist/mygo/ui/editor"
)

// languageFor is the language of a file: MyGo's, but for the languages
// the app knows better.
func languageFor(name string) *editor.Language {
	if strings.EqualFold(path.Ext(name), ".properties") {
		return propertiesLanguage
	}
	return editor.LanguageFor(name)
}

// propertiesLanguage is Java's .properties, which MyGo takes for TOML:
// TOML knows neither key: value, ! comments, lines continued with \, nor
// Spring's ${placeholders}.
var propertiesLanguage = &editor.Language{
	Name:        "Properties",
	Extensions:  []string{".properties"},
	LineComment: "#",
	TabSize:     4,
	Tokenizer:   editor.TokenizerFunc(tokenizeProperties),
}

// The states a line of a .properties file ends in.
const (
	propsLine      = 0 // the next line starts a key, or a comment
	propsContinued = 1 // the next line goes on with a value
)

// tokenizeProperties splits a line as java.util.Properties reads it:
// a key up to the first unescaped =, : or space, then its value, which a
// \ at the end of the line continues on the next.
func tokenizeProperties(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
	i := len(line) - len(strings.TrimLeft(line, " \t\f"))
	if i > 0 {
		tokens = append(tokens, editor.Token{End: i, Kind: editor.TokenText})
	}
	if state == propsContinued {
		return propsValue(line, i, tokens)
	}
	if i == len(line) {
		return tokens, propsLine
	}
	if line[i] == '#' || line[i] == '!' {
		return append(tokens, editor.Token{End: len(line), Kind: editor.TokenComment}), propsLine
	}
	// The key, with its escapes.
	start := i
	for i < len(line) && !strings.ContainsRune("=: \t\f", rune(line[i])) {
		if line[i] == '\\' && i+1 < len(line) {
			if i > start {
				tokens = append(tokens, editor.Token{End: i, Kind: editor.TokenProperty})
			}
			i += 2
			tokens = append(tokens, editor.Token{End: i, Kind: editor.TokenEscape})
			start = i
			continue
		}
		i++
	}
	if i > start {
		tokens = append(tokens, editor.Token{End: i, Kind: editor.TokenProperty})
	}
	// The separator: spaces around one = or :, or spaces alone.
	sep := i
	for i < len(line) && strings.ContainsRune(" \t\f", rune(line[i])) {
		i++
	}
	if i < len(line) && (line[i] == '=' || line[i] == ':') {
		i++
		for i < len(line) && strings.ContainsRune(" \t\f", rune(line[i])) {
			i++
		}
	}
	if i > sep {
		tokens = append(tokens, editor.Token{End: i, Kind: editor.TokenOperator})
	}
	return propsValue(line, i, tokens)
}

// propsValue tokenizes a value from i: a string, with escapes and Spring's
// ${placeholders}; a lone boolean or number, as Spring reads it, a
// constant or a number.
func propsValue(line string, i int, tokens []editor.Token) ([]editor.Token, uint64) {
	value := line[i:]
	// An odd run of \ at the end continues the value.
	trailing := len(value) - len(strings.TrimRight(value, `\`))
	next := uint64(propsLine)
	if trailing%2 == 1 {
		next = propsContinued
	}
	switch v := strings.TrimSpace(value); {
	case v == "":
	case next == propsLine && (v == "true" || v == "false"):
		return append(tokens, editor.Token{End: len(line), Kind: editor.TokenConstant}), next
	case next == propsLine && isNumber(v):
		return append(tokens, editor.Token{End: len(line), Kind: editor.TokenNumber}), next
	}
	start := i
	flush := func(end int) {
		if end > start {
			tokens = append(tokens, editor.Token{End: end, Kind: editor.TokenString})
		}
	}
	for i < len(line) {
		switch {
		case line[i] == '\\' && i+1 < len(line):
			flush(i)
			end := i + 2
			if line[i+1] == 'u' {
				end = min(i+6, len(line))
			}
			tokens = append(tokens, editor.Token{End: end, Kind: editor.TokenEscape})
			i, start = end, end
		case line[i] == '\\':
			// The continuation.
			flush(i)
			tokens = append(tokens, editor.Token{End: i + 1, Kind: editor.TokenEscape})
			i, start = i+1, i+1
		case strings.HasPrefix(line[i:], "${"):
			end := strings.IndexByte(line[i:], '}')
			if end < 0 {
				i++
				continue
			}
			flush(i)
			end += i + 1
			tokens = append(tokens, editor.Token{End: end, Kind: editor.TokenEscape})
			i, start = end, end
		default:
			i++
		}
	}
	flush(len(line))
	return tokens, next
}

func isNumber(s string) bool {
	s = strings.TrimPrefix(s, "-")
	digits, dot := 0, false
	for _, r := range s {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.' && !dot:
			dot = true
		default:
			return false
		}
	}
	return digits > 0
}
