package picker

import "strings"

// fuzzyMatch reports whether every rune of needle appears in hay, in order,
// ignoring case. "rs" matches "redis-stg"; "sr" does not. Simple on purpose:
// a picker with a dozen entries does not need ranking.
func fuzzyMatch(needle, hay string) bool {
	needle, hay = strings.ToLower(needle), strings.ToLower(hay)
	i := 0
	for _, r := range hay {
		if i < len(needle) && rune(needle[i]) == r {
			i++
		}
	}
	return i == len(needle)
}
