package i18n

import "testing"

// pickLocale must tolerate the optional whitespace before ";" that
// RFC 9110 explicitly allows. Headers like "id ;q=0.8" used to fall
// through silently to the default locale because the trailing space
// was preserved in the candidate.
func TestPickLocale_TrimsWhitespaceAroundQValue(t *testing.T) {
	allow := map[Locale]struct{}{
		Locale("en"): {},
		Locale("id"): {},
	}
	cases := map[string]Locale{
		"id":              "id",
		"id;q=0.8":        "id",
		"id ;q=0.8":       "id",
		"id  ;  q=0.8":    "id",
		"en-US ;q=0.9":    "en",
		"fr-FR, id;q=0.8": "id",
		// no match → empty result
		"fr,de;q=0.4": "",
	}
	for header, want := range cases {
		t.Run(header, func(t *testing.T) {
			got := pickLocale(header, allow)
			if got != want {
				t.Fatalf("pickLocale(%q) = %q, want %q", header, got, want)
			}
		})
	}
}
