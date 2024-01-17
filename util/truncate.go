package util

import "unicode/utf8"

// TruncateUTF8 truncates a string to contain a
// maximum number of runes, truncating on rune boundary.
// Returns true if the returned string is possibly different
// than the passed-in string.
func TruncateUTF8(s string, maxRunes int) (string, bool) {
	if utf8.RuneCountInString(s) > maxRunes {
		runes := []rune(s)
		// making double sure
		if len(runes) > maxRunes {
			runes = runes[:maxRunes]
		}
		s = string(runes)
		return s, true
	}
	return s, false
}
