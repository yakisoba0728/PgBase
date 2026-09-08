package userauth

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
)

func TestAuthCollectionDeleteConflictRecognizesPostgreSQLRestrictionCodes(t *testing.T) {
	for _, code := range []string{"23503", "23001"} {
		err := fmt.Errorf("delete realm: %w", &pgconn.PgError{Code: code})
		if !isAuthCollectionDeleteConflict(err) {
			t.Fatalf("code %s was not recognized as a delete conflict", code)
		}
	}
	if isAuthCollectionDeleteConflict(errors.New("unrelated")) {
		t.Fatal("unrelated error was recognized as a delete conflict")
	}
}
