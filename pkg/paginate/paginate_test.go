package paginate

import "testing"

func TestFromStrings(t *testing.T) {
	cases := []struct {
		page, size       string
		wantPage, wantSz int
	}{
		{"", "", DefaultPage, DefaultPageSize},
		{"2", "50", 2, 50},
		{"abc", "-5", DefaultPage, DefaultPageSize},
		{"3", "9999", 3, MaxPageSize},
	}
	for _, c := range cases {
		got := FromStrings(c.page, c.size)
		if got.Page != c.wantPage || got.PageSize != c.wantSz {
			t.Errorf("FromStrings(%q,%q) = %+v, want page=%d size=%d", c.page, c.size, got, c.wantPage, c.wantSz)
		}
	}
}

func TestOffsetLimit(t *testing.T) {
	p := Params{Page: 3, PageSize: 25}
	if p.Offset() != 50 {
		t.Errorf("Offset = %d, want 50", p.Offset())
	}
	if p.Limit() != 25 {
		t.Errorf("Limit = %d, want 25", p.Limit())
	}
}
