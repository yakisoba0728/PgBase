package backup

import "testing"

func TestPostgresToolKeepsNativePathFallback(t *testing.T) {
	for _, name := range []string{"pg_dump", "pg_restore"} {
		if got := postgresTool(name, 99999); got != name {
			t.Fatalf("native PATH replaced: %q", got)
		}
	}
}
