package api

import "testing"

// Explicit gzip preferences take precedence over the wildcard, regardless of
// field-line or list order. Invalid weights never opt a client into encoding.
func TestAcceptsGzipNegotiation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		values []string
		want   bool
	}{
		{"wildcard cannot override explicit refusal", []string{"*;q=1, gzip;q=0"}, false},
		{"explicit acceptance overrides wildcard refusal", []string{"*;q=0, gzip;q=1"}, true},
		{"explicit refusal in later field line", []string{"*", "gzip;q=0"}, false},
		{"explicit acceptance in later field line", []string{"*;q=0", "gzip;q=0.5"}, true},
		{"negative quality", []string{"gzip;q=-1"}, false},
		{"quality above one", []string{"gzip;q=1.001"}, false},
		{"not a number", []string{"gzip;q=NaN"}, false},
		{"infinite quality", []string{"gzip;q=Inf"}, false},
		{"scientific notation", []string{"gzip;q=1e0"}, false},
		{"excess precision", []string{"gzip;q=0.0001"}, false},
		{"duplicate weight", []string{"gzip;q=1;q=0"}, false},
		{"unknown parameter", []string{"gzip;foo=bar"}, false},
		{"valid minimum positive quality", []string{"gzip;q=0.001"}, true},
		{"valid full quality precision", []string{"gzip;q=1.000"}, true},
		{"zero fractional digits", []string{"gzip;q=1."}, true},
		{"all zero quality", []string{"gzip;q=0.000"}, false},
		{"optional whitespace and case", []string{" GZIP ; Q = 0.500 "}, true},
		{"duplicate explicit refusal first", []string{"gzip;q=0, gzip"}, false},
		{"duplicate explicit refusal last", []string{"gzip, gzip;q=0"}, false},
		{"duplicate wildcard refusal", []string{"*, *;q=0"}, false},
		{"explicit overrides duplicate wildcards", []string{"*;q=0, *, gzip"}, true},
		{"empty header", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := acceptsGzip(tc.values); got != tc.want {
				t.Fatalf("acceptsGzip(%q) = %v, want %v", tc.values, got, tc.want)
			}
		})
	}
}

func FuzzAcceptsGzipExplicitRefusal(f *testing.F) {
	for _, value := range []string{"", "*", "gzip", "br, *;q=1", "gzip;q=NaN", "gzip;q=1, gzip;q=0.5"} {
		f.Add(value)
	}
	f.Fuzz(func(t *testing.T, value string) {
		if acceptsGzip([]string{value, "gzip;q=0"}) || acceptsGzip([]string{"gzip;q=0", value}) {
			t.Fatalf("explicit refusal was overridden by %q", value)
		}
	})
}
