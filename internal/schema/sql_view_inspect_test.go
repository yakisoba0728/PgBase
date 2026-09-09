package schema

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestInspectSQLViewRejectsInvalidInputBeforeUsingTransaction(t *testing.T) {
	for _, sql := range []string{"", "select '\x00'", strings.Repeat("x", maximumSQLViewBytes+1)} {
		if _, err := InspectSQLView(context.Background(), nil, Catalog{}, sql); !errors.Is(err, ErrConflict) {
			t.Fatalf("InspectSQLView input error=%v", err)
		}
	}
}
