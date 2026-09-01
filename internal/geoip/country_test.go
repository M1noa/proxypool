package geoip

import "testing"

func TestCountryToISO(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"United States", "US"},
		{"united states", "US"},
		{"United States of America", "US"},
		{"Russian Federation", "RU"},
		{"South Korea", "KR"},
		{"US", "US"},
		{"USA", "US"},
		// the " - city" and ", city" shapes real sources emit
		{"United Kingdom - London", "GB"},
		{"Germany - Frankfurt am Main", "DE"},
		{"South Korea - Seoul", "KR"},
		{"Seoul, South Korea", ""}, // head is "Seoul", which resolves to nothing
		{"France, Lauterbourg", "FR"},
		{"Nowhereland", ""},
		{"  France  ", "FR"}, // strip runs on the retry, so the full key misses first
	}
	for _, c := range cases {
		if got := CountryToISO(c.in); got != c.want {
			t.Errorf("CountryToISO(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}
