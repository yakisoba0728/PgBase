package doctor

import (
	"context"
	"fmt"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// ValidateRequiredPhysicalSchema verifies every planner-owned object while
// allowing unrelated user objects. It is intended for the pre-commit check of
// a user-authored migration, where the transaction can still be rolled back.
func ValidateRequiredPhysicalSchema(ctx context.Context, db database.DBTX, manifest schema.ExpectedPhysicalSchema) error {
	snapshot, err := readPhysicalSnapshot(ctx, db, manifest.ManagedNamespaces())
	if err != nil {
		return err
	}
	report := Report{Findings: []Finding{}}
	comparison := physicalComparison{
		manifest: manifest,
		snapshot: snapshot,
		roles:    physicalRoles(nil),
		report:   &report,
		matched:  make(map[schema.PhysicalObjectRef]struct{}),
	}
	required := manifest.Required()
	comparison.compareTables(required.Tables)
	comparison.compareColumns(required.Columns)
	comparison.compareIndexes(required.Indexes)
	comparison.compareConstraints(required.Constraints)
	comparison.compareTriggers(required.Triggers)
	comparison.compareFunctions(required.Functions)
	if len(report.Findings) != 0 {
		return fmt.Errorf("managed physical schema is inconsistent: %s", report.Findings[0].Code)
	}
	return nil
}
