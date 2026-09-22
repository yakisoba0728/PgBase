package runtimesettings

import (
	"strings"
	"unicode"
)

func validAPIRateLabel(label string) bool {
	if len(label) == 0 || len(label) > 256 {
		return false
	}
	if !strings.HasPrefix(label, "/") {
		method, route, ok := strings.Cut(label, " ")
		if !ok || method == "" {
			return false
		}
		for _, letter := range method {
			if letter < 'A' || letter > 'Z' {
				return false
			}
		}
		label = route
	}
	return strings.HasPrefix(label, "/") && !strings.ContainsAny(label, "?#") &&
		strings.IndexFunc(label, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) < 0
}
