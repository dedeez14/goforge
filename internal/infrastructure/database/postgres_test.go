package database

import "testing"

func TestIsLikelyPgBouncer(t *testing.T) {
	cases := []struct {
		port uint16
		want bool
	}{
		{6432, true},
		{5432, false},
		{15432, false},
		{0, false},
	}
	for _, c := range cases {
		if got := isLikelyPgBouncer(c.port); got != c.want {
			t.Errorf("isLikelyPgBouncer(%d) = %v, want %v", c.port, got, c.want)
		}
	}
}
