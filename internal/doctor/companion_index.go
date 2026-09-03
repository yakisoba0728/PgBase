package doctor

import (
	"context"

	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

// checkCompanionIndexes reports the nullable-DESC companion rollout against the
// physical shape PostgreSQL actually stores, read from the caller's snapshot so
// the state and the objects it describes are the same instant.
//
// The three transitional states are distinguishable in the report:
//
//   - pending emits nothing, because no companion is a required contract yet;
//   - building emits an incomplete-rollout finding, because an operator still
//     has maintenance to run;
//   - ready emits a missing-object finding, because the contract now applies.
//
// Invalid residue is reported in every state and is deliberately a separate
// code: an interrupted CREATE INDEX CONCURRENTLY is recovered by rerunning
// maintenance, not by repairing the catalog by hand.
//
// The manifest is the one the physical comparison already compiled. Doctor runs
// in one repeatable-read snapshot, so a second catalog read could not observe
// anything different and would only cost statements against the fixed query
// bound.
func checkCompanionIndexes(
	ctx context.Context,
	db database.DBTX,
	schemaVersion int64,
	manifest schema.ExpectedPhysicalSchema,
	report *Report,
) error {
	status, err := schema.ReadCompanionIndexStatus(ctx, db)
	if err != nil {
		return err
	}
	if !status.Installed {
		return nil
	}
	diagnostics, err := schema.CompanionIndexDiagnosticsForManifest(ctx, db, status, schemaVersion, manifest)
	if err != nil {
		return err
	}
	if !diagnostics.Status.Installed || !diagnostics.CatalogReadable {
		return nil
	}
	if diagnostics.Invalid > 0 {
		report.Findings = append(report.Findings, finding(
			"schema_companion_index_invalid",
			"An interrupted concurrent index build left an unusable sort index; rerun companion index maintenance",
		))
	}
	switch diagnostics.Status.State {
	case schema.CompanionIndexBuilding:
		report.Findings = append(report.Findings, finding(
			"schema_companion_index_build_incomplete",
			"A companion sort index rollout is still building; rerun companion index maintenance to finish it",
		))
	case schema.CompanionIndexReady:
		if companionContractBroken(diagnostics) {
			report.Findings = append(report.Findings, finding(
				"schema_companion_index_missing",
				"An enabled companion sort index is missing or has an unexpected key order",
			))
		}
	}
	return nil
}

// companionContractBroken reports drift while the capability claims ready. The
// stored count is part of the contract: a catalog change closes the gate, so
// while ready the promoted count and the catalog must still agree.
func companionContractBroken(diagnostics schema.CompanionIndexDiagnostics) bool {
	if diagnostics.Missing > 0 || diagnostics.Mismatched > 0 {
		return true
	}
	count := diagnostics.Status.ReadyIndexCount
	return count == nil || *count != int64(diagnostics.Expected)
}
