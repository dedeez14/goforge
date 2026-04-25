package main

import "testing"

// TestIsLocalhostDSN guards against false-positives on hostnames
// that merely start with "localhost" or "127.0.0.1" - the original
// substring-based implementation flagged
// "postgres://app@localhost.db.internal:5432/app" as a localhost
// DSN. Devin Review caught it; this test pins the fix in place.
func TestIsLocalhostDSN(t *testing.T) {
	cases := []struct {
		dsn  string
		want bool
		why  string
	}{
		// Loopback host names (positives).
		{"postgres://u:p@localhost:5432/db", true, "localhost url form"},
		{"postgres://u:p@127.0.0.1:5432/db", true, "ipv4 loopback url form"},
		{"postgres://u:p@[::1]:5432/db", true, "ipv6 loopback url form"},
		{"host=localhost port=5432 user=u dbname=db", true, "localhost kv form"},
		{"host=127.0.0.1 user=u dbname=db", true, "ipv4 loopback kv form"},

		// Non-loopback hosts whose name merely starts with the
		// loopback strings (negatives - the false-positive bug).
		{"postgres://u:p@localhost.db.internal:5432/db", false, "tld lookalike"},
		{"postgres://u:p@localhost-replica.example.com:5432/db", false, "subdomain lookalike"},
		{"postgres://u:p@127.0.0.100:5432/db", false, "different /24"},
		{"host=localhost.db.internal port=5432 user=u dbname=db", false, "tld lookalike kv form"},

		// Pathological / empty.
		{"", false, "empty"},
		{"some-garbage-no-host", false, "unparseable"},
	}
	for _, tc := range cases {
		t.Run(tc.why, func(t *testing.T) {
			if got := isLocalhostDSN(tc.dsn); got != tc.want {
				t.Fatalf("isLocalhostDSN(%q) = %v, want %v", tc.dsn, got, tc.want)
			}
		})
	}
}
