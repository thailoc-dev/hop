package main

import "testing"

func TestInferEnv(t *testing.T) {
	tests := []struct {
		host, container, want string
	}{
		{"example-backend-dev", "app_mongo_staging", "stg"},
		{"example-backend-prod", "app_mongo", "prod"},
		{"example-api", "api_redis_dev", "dev"},
		{"host-production", "db", "prod"},
		{"h", "c", ""},
		// The container is more specific than the host and wins when they
		// disagree: a staging container on a dev box is staging.
		{"example-backend-dev", "app_mongo_production", "prod"},
	}

	for _, tc := range tests {
		if got := inferEnv(tc.host, tc.container); got != tc.want {
			t.Fatalf("inferEnv(%q, %q) = %q, want %q", tc.host, tc.container, got, tc.want)
		}
	}
}
