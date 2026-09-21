package picker

import "testing"

func TestFuzzyMatch(t *testing.T) {
	tests := []struct {
		needle, hay string
		want        bool
	}{
		{"", "anything", true},
		{"rs", "redis-stg", true},
		{"RS", "redis-stg", true},
		{"redis-stg", "redis-stg", true},
		{"stgr", "redis-stg", false},
		{"x", "redis-stg", false},
		{"mongo", "app_mongo_staging", true},
	}
	for _, tc := range tests {
		if got := fuzzyMatch(tc.needle, tc.hay); got != tc.want {
			t.Fatalf("fuzzyMatch(%q, %q) = %v, want %v", tc.needle, tc.hay, got, tc.want)
		}
	}
}
