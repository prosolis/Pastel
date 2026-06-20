// Package normalize provides the canonical text normalization used to match
// user-entered game names and search queries against stored, normalized titles.
//
// The watchlist matcher (write side) and the web deal search (read side) must
// produce byte-identical output or search silently stops matching stored rows,
// so the implementation lives here in one place rather than being copied.
package normalize

import (
	"strings"
	"unicode"
)

// Text lowercases, strips non-alphanumeric runes (keeping spaces), and
// collapses whitespace.
func Text(s string) string {
	s = strings.ToLower(s)
	var b strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' {
			b.WriteRune(r)
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}
