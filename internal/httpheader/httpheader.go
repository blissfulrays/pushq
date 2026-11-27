// Package httpheader validates HTTP header names and values the same way
// net/http does when sending a request, so bad headers are rejected when a
// task is created instead of failing every delivery attempt.
package httpheader

import "fmt"

// ValidName reports whether s is an RFC 9110 field name (a token).
func ValidName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isTokenChar(s[i]) {
			return false
		}
	}
	return true
}

// ValidValue reports whether s is a valid field value: no control characters
// other than horizontal tab (so no CR, LF, or NUL).
func ValidValue(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < 0x20 && c != '\t') || c == 0x7f {
			return false
		}
	}
	return true
}

// Check returns a descriptive error for the first invalid header in h.
func Check(h map[string]string) error {
	for k, v := range h {
		if !ValidName(k) {
			return fmt.Errorf("invalid header name %q", k)
		}
		if !ValidValue(v) {
			return fmt.Errorf("header %q has an invalid value (control characters such as CR, LF or NUL are not allowed)", k)
		}
	}
	return nil
}

func isTokenChar(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}
	switch c {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
