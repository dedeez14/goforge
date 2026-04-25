package i18n

import (
	"context"
	"testing"
)

func TestBundle_LookupFallsBackToDefault(t *testing.T) {
	b := NewBundle(LocaleEN)
	b.Add("err.x", LocaleEN, "X went wrong")
	b.Add("err.x", LocaleID, "X mengalami masalah")

	if msg, _ := b.Lookup("err.x", LocaleID); msg != "X mengalami masalah" {
		t.Fatalf("ID lookup wrong: %q", msg)
	}
	if msg, _ := b.Lookup("err.x", LocaleEN); msg != "X went wrong" {
		t.Fatalf("EN lookup wrong: %q", msg)
	}
	// Unknown locale → falls back to default (EN).
	if msg, _ := b.Lookup("err.x", "fr"); msg != "X went wrong" {
		t.Fatalf("FR fallback wrong: %q", msg)
	}
	// Unknown code → not found.
	if _, ok := b.Lookup("err.unknown", LocaleEN); ok {
		t.Fatalf("unknown code should miss")
	}
}

func TestLocale_NormaliseStripsRegion(t *testing.T) {
	cases := map[Locale]Locale{
		"en-US": "en",
		"id-ID": "id",
		"EN":    "en",
		"zh_CN": "zh",
		"":      "",
	}
	for in, want := range cases {
		if got := in.Normalise(); got != want {
			t.Fatalf("%q: want %q got %q", in, want, got)
		}
	}
}

func TestPickLocale_HonoursOrder(t *testing.T) {
	allow := map[Locale]struct{}{LocaleEN: {}, LocaleID: {}}
	cases := map[string]Locale{
		"id, en;q=0.5":            LocaleID,
		"en-US,en;q=0.9,id;q=0.8": LocaleEN,
		"fr,de":                   "",
		"":                        "",
		"*":                       "",
		"id-ID":                   LocaleID,
	}
	for hdr, want := range cases {
		if got := pickLocale(hdr, allow); got != want {
			t.Fatalf("header %q: want %q got %q", hdr, want, got)
		}
	}
}

func TestT_FallsBackWhenNoGlobal(t *testing.T) {
	// Reset global to nil for this test.
	SetGlobal(nil)
	if got := T(context.Background(), "err.x", "fallback"); got != "fallback" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestT_TranslatesViaGlobal(t *testing.T) {
	b := NewBundle(LocaleEN)
	b.Add("err.x", LocaleID, "pesan id")
	SetGlobal(b)
	defer SetGlobal(nil)

	ctx := WithLocale(context.Background(), LocaleID)
	if got := T(ctx, "err.x", "fallback en"); got != "pesan id" {
		t.Fatalf("expected pesan id, got %q", got)
	}
	// No locale on ctx → bundle default ("en") which has no entry → fallback.
	if got := T(context.Background(), "err.x", "fallback en"); got != "fallback en" {
		t.Fatalf("expected fallback, got %q", got)
	}
}

func TestDefaultBundle_HasShippedCodes(t *testing.T) {
	b := DefaultBundle()
	must := []string{
		"internal", "validation", "auth.invalid", "user.invalid_credentials",
		"rate_limited", "route.not_found", "menu.cycle", "permission.taken",
	}
	for _, code := range must {
		if _, ok := b.Lookup(code, LocaleID); !ok {
			t.Fatalf("default bundle missing ID translation for %q", code)
		}
		if _, ok := b.Lookup(code, LocaleEN); !ok {
			t.Fatalf("default bundle missing EN translation for %q", code)
		}
	}
}
