package usecase

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// TestTruncateUA_ShortStringsUnchanged pins the zero-work path so a
// refactor to the UTF-8-aware cut doesn't accidentally reshape
// normal headers.
func TestTruncateUA_ShortStringsUnchanged(t *testing.T) {
	const ua = "Mozilla/5.0 (Macintosh; Intel Mac OS X 14_2) AppleWebKit/605.1.15"
	if got := truncateUA(ua); got != ua {
		t.Fatalf("short UA mutated: got %q", got)
	}
}

// TestTruncateUA_ASCIIBoundary pins the byte-exact cap for ASCII-only
// input — the historical behaviour we keep compatible with.
func TestTruncateUA_ASCIIBoundary(t *testing.T) {
	in := strings.Repeat("a", 600)
	got := truncateUA(in)
	if len(got) != 512 {
		t.Fatalf("len = %d, want 512", len(got))
	}
}

// TestTruncateUA_RuneBoundary pins the Devin Review finding: a byte-
// level cap that lands in the middle of a multi-byte rune must be
// pulled back so the result stays valid UTF-8 - otherwise Postgres
// would reject the INSERT with an encoding error and block the whole
// login.
func TestTruncateUA_RuneBoundary(t *testing.T) {
	// 170 CJK runes × 3 bytes = 510 bytes, then a single 3-byte rune
	// whose leading byte sits at offset 510. With the naive
	// byte-cap the cut at 512 would land one byte into that rune
	// and leave a truncated multi-byte sequence.
	in := strings.Repeat("漢", 170) + "語" + "xxxxxx"
	got := truncateUA(in)
	if !utf8.ValidString(got) {
		t.Fatalf("truncateUA produced invalid UTF-8: % x", got)
	}
	if len(got) > 512 {
		t.Fatalf("len = %d, exceeds cap", len(got))
	}
}

// TestTruncateUA_AllMultibyte covers the case where every byte in
// the input is a continuation byte away from the cap — the loop
// must terminate and return a prefix of runes, not spin or return
// the empty string.
func TestTruncateUA_AllMultibyte(t *testing.T) {
	in := strings.Repeat("漢", 400) // 1200 bytes
	got := truncateUA(in)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8 returned")
	}
	if len(got) == 0 {
		t.Fatalf("loop collapsed to empty string")
	}
	if len(got) > 512 {
		t.Fatalf("len = %d, exceeds cap", len(got))
	}
	// Count of runes should be 170 (170*3 = 510) since the cap is
	// 512 and every rune is 3 bytes.
	if n := utf8.RuneCountInString(got); n != 170 {
		t.Fatalf("rune count = %d, want 170", n)
	}
}
