package query

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// PostgreSQL retains responsibility for timestamp-literal interpretation. Only
// its invalid datetime errors in filters with a literal time argument become
// caller validation failures; other query errors keep their original meaning.
func functionFilterQueryError(filter *plannedRelationFilter, err error) error {
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) && (postgresError.Code == "22007" || postgresError.Code == "22008") && filterHasLiteralTimeArgument(filter) {
		return validation("filter", "type_mismatch")
	}
	return err
}

func filterHasLiteralTimeArgument(filter *plannedRelationFilter) bool {
	if filter == nil {
		return false
	}
	if filter.comparison != nil {
		fn := filter.comparison.function
		return fn != nil && fn.name == "strftime" && len(fn.arguments) > 1 && fn.arguments[1].field == nil
	}
	for _, child := range filter.children {
		if filterHasLiteralTimeArgument(child) {
			return true
		}
	}
	return false
}
