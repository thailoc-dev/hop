package main

import "strings"

// envPatterns map substrings onto environment labels. Order matters: prod is
// checked first, because mislabelling production as anything else is the one
// error with real consequences.
var envPatterns = []struct {
	label   string
	needles []string
}{
	{"prod", []string{"prod", "production"}},
	{"stg", []string{"stg", "staging"}},
	{"dev", []string{"dev", "develop"}},
}

// inferEnv guesses the environment from the container and host names.
//
// The container is checked first: it is the more specific of the two, so a
// staging container on a dev host is staging.
func inferEnv(host, container string) string {
	for _, subject := range []string{strings.ToLower(container), strings.ToLower(host)} {
		for _, pattern := range envPatterns {
			for _, needle := range pattern.needles {
				if strings.Contains(subject, needle) {
					return pattern.label
				}
			}
		}
	}
	return ""
}
