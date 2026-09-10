// Package sshconfig reads host aliases out of ~/.ssh/config for tab
// completion. It is READ-ONLY by design: ssh owns that file, and hop has no
// code path that writes to it.
package sshconfig

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// maxIncludeDepth stops a config that includes itself from looping.
const maxIncludeDepth = 8

// Hosts returns the connectable alias names declared in an ssh config,
// following Include directives.
//
// Patterns are excluded: `Host *` and `Host web-*` configure other hosts
// rather than naming one, and a negation like `!nope` names one to skip. None
// of them can be typed as an argument, so none belong in a completion list.
func Hosts(path string) ([]string, error) {
	seen := map[string]bool{}
	var order []string

	if err := collect(path, 0, seen, &order); err != nil {
		return nil, err
	}
	return order, nil
}

// DefaultPath is the ssh config for the current user.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".ssh", "config"), nil
}

func collect(path string, depth int, seen map[string]bool, order *[]string) error {
	if depth > maxIncludeDepth {
		return nil
	}

	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil // no config is not an error, it is an empty list
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		keyword, rest, found := strings.Cut(line, " ")
		if !found {
			continue
		}

		switch strings.ToLower(keyword) {
		case "host":
			for _, alias := range strings.Fields(rest) {
				if isPattern(alias) || seen[alias] {
					continue
				}
				seen[alias] = true
				*order = append(*order, alias)
			}
		case "include":
			for _, pattern := range strings.Fields(rest) {
				for _, included := range expandInclude(path, pattern) {
					if err := collect(included, depth+1, seen, order); err != nil {
						return err
					}
				}
			}
		}
	}
	return scanner.Err()
}

func isPattern(alias string) bool {
	return strings.ContainsAny(alias, "*?") || strings.HasPrefix(alias, "!")
}

// expandInclude resolves an Include pattern relative to the including file's
// directory, which is how ssh treats a relative path.
func expandInclude(parent, pattern string) []string {
	if !filepath.IsAbs(pattern) {
		pattern = filepath.Join(filepath.Dir(parent), pattern)
	}
	matches, err := filepath.Glob(pattern)
	if err != nil {
		return nil
	}
	return matches
}
