package pgbase

import "testing"

func TestDatabaseURLRejectsAmbiguousTLSParameters(t *testing.T) {
	for _, allow := range []bool{false, true} {
		for _, query := range []string{"sslmode=verify-full&sslmode=disable", "sslmode=disable&sslmode=verify-full", "sslmode=verify-full&ssl=true", "ssl=true", "sslmode=verify-full&sslrootcert=a&sslrootcert=b"} {
			if err := validateDatabaseURL("postgres://fixture:fixture@network-must-not-run.invalid/db?"+query, allow); err == nil {
				t.Fatalf("accepted ambiguous TLS %q allowInsecure=%t", query, allow)
			}
		}
	}
	for _, test := range []struct {
		url   string
		allow bool
	}{{"postgres://fixture:fixture@127.0.0.1/db?sslmode=disable", false}, {"postgres://fixture:fixture@network-must-not-run.invalid/db?sslmode=verify-full", false}, {"postgres://fixture:fixture@network-must-not-run.invalid/db?sslmode=disable", true}} {
		if err := validateDatabaseURL(test.url, test.allow); err != nil {
			t.Fatal(err)
		}
	}
}
