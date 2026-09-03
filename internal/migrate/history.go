package migrate

import (
	"bytes"
	"fmt"
	"slices"

	"github.com/webisnoteasy/PgBase/internal/releasephase"
	"github.com/webisnoteasy/PgBase/migrations/system"
)

// HistoryIssueKind identifies why committed migration history is not a clean
// prefix of the migrations embedded in this binary.
type HistoryIssueKind string

const (
	HistoryIssueUnknownVersion   HistoryIssueKind = "unknown_version"
	HistoryIssueChecksumMismatch HistoryIssueKind = "checksum_mismatch"
	HistoryIssueGap              HistoryIssueKind = "gap"
	HistoryIssueDuplicateVersion HistoryIssueKind = "duplicate_version"
)

// HistoryIssue describes one fail-closed migration history defect.
type HistoryIssue struct {
	Kind    HistoryIssueKind
	Version int64

	migrationName  string
	appliedVersion int64
}

func (i *HistoryIssue) Error() string {
	if i == nil {
		return "system migration history issue"
	}
	switch i.Kind {
	case HistoryIssueUnknownVersion:
		return fmt.Sprintf("system migration history contains unknown version %d", i.Version)
	case HistoryIssueChecksumMismatch:
		if i.migrationName != "" {
			return fmt.Sprintf("system migration %d (%s) checksum mismatch", i.Version, i.migrationName)
		}
		return fmt.Sprintf("system migration %d checksum mismatch", i.Version)
	case HistoryIssueGap:
		return fmt.Sprintf("system migration history is out of order at applied version %d after missing version %d", i.appliedVersion, i.Version)
	case HistoryIssueDuplicateVersion:
		return fmt.Sprintf("system migration history contains duplicate version %d", i.Version)
	default:
		return fmt.Sprintf("system migration history issue at version %d", i.Version)
	}
}

type historyInspection struct {
	state   HistoryState
	latest  int64
	issue   *HistoryIssue
	applied map[int64][]byte
	err     error
}

func inspectHistory(migrations []system.Migration, applied map[int64][]byte) historyInspection {
	inspection := historyInspection{applied: applied}
	if err := validateMigrationManifest(migrations); err != nil {
		inspection.err = err
		return inspection
	}
	known := make(map[int64]system.Migration, len(migrations))
	for _, migration := range migrations {
		known[migration.Version] = migration
		if migration.Version > inspection.latest {
			inspection.latest = migration.Version
		}
	}

	appliedVersions := make([]int64, 0, len(applied))
	for version := range applied {
		appliedVersions = append(appliedVersions, version)
		if version > inspection.state.Version {
			inspection.state.Version = version
		}
	}
	slices.Sort(appliedVersions)
	if checksum := applied[inspection.state.Version]; len(checksum) == len(inspection.state.Checksum) {
		copy(inspection.state.Checksum[:], checksum)
	}
	for _, version := range appliedVersions {
		if _, ok := known[version]; !ok {
			inspection.issue = &HistoryIssue{Kind: HistoryIssueUnknownVersion, Version: version}
			inspection.state.Dirty = true
			return inspection
		}
	}

	var firstMissing int64
	for _, migration := range migrations {
		checksum, ok := applied[migration.Version]
		if !ok {
			if firstMissing == 0 {
				firstMissing = migration.Version
			}
			continue
		}
		if firstMissing != 0 {
			inspection.issue = &HistoryIssue{
				Kind:           HistoryIssueGap,
				Version:        firstMissing,
				appliedVersion: migration.Version,
			}
			inspection.state.Dirty = true
			return inspection
		}
		if !bytes.Equal(checksum, migration.SHA256[:]) {
			inspection.issue = &HistoryIssue{
				Kind:          HistoryIssueChecksumMismatch,
				Version:       migration.Version,
				migrationName: migration.Name,
			}
			inspection.state.Dirty = true
			return inspection
		}
	}
	return inspection
}

func validateMigrationManifest(migrations []system.Migration) error {
	var previous int64
	for index, migration := range migrations {
		if migration.Version <= 0 {
			return fmt.Errorf("system migration manifest contains invalid version %d", migration.Version)
		}
		if index > 0 && migration.Version <= previous {
			return fmt.Errorf("system migration manifest versions must be unique and strictly increasing: %d follows %d", migration.Version, previous)
		}
		previous = migration.Version
	}
	return nil
}

type historyCompatibilityPolicy struct {
	requiredVersion int64
	optionalVersion int64
}

// schemaCompilerGenerationPreparationPolicy is pinned to migration 9's P0
// boundary. The preparatory artifact is the only binary allowed to serve both
// sides of that boundary; a later feature artifact replaces this declaration
// with an exact-latest policy instead of silently widening it.
func schemaCompilerGenerationPreparationPolicy(migrations []system.Migration) *historyCompatibilityPolicy {
	if releasephase.SchemaCompilerGeneration != 2 || len(migrations) != 9 ||
		migrations[7].Version != 8 || migrations[7].Name != "schema_companion_indexes" ||
		migrations[8].Version != 9 || migrations[8].Name != "schema_compiler_generation" {
		return nil
	}
	return &historyCompatibilityPolicy{requiredVersion: 8, optionalVersion: 9}
}

func requiredHistoryPolicy(migrations []system.Migration) historyCompatibilityPolicy {
	if len(migrations) == 0 {
		return historyCompatibilityPolicy{}
	}
	return historyCompatibilityPolicy{requiredVersion: migrations[len(migrations)-1].Version}
}

func validateHistoryPolicy(migrations []system.Migration, declared *historyCompatibilityPolicy) (historyCompatibilityPolicy, error) {
	if err := validateMigrationManifest(migrations); err != nil {
		return historyCompatibilityPolicy{}, err
	}
	policy := requiredHistoryPolicy(migrations)
	if declared != nil {
		policy = *declared
	}
	if len(migrations) == 0 {
		if policy != (historyCompatibilityPolicy{}) {
			return historyCompatibilityPolicy{}, fmt.Errorf("system migration compatibility policy requires an empty history")
		}
		return policy, nil
	}
	if policy.optionalVersion == 0 {
		if policy.requiredVersion != migrations[len(migrations)-1].Version {
			return historyCompatibilityPolicy{}, fmt.Errorf("system migration compatibility policy must require latest version %d", migrations[len(migrations)-1].Version)
		}
		return policy, nil
	}
	if len(migrations) < 2 || policy.requiredVersion != migrations[len(migrations)-2].Version || policy.optionalVersion != migrations[len(migrations)-1].Version {
		return historyCompatibilityPolicy{}, fmt.Errorf("system migration compatibility policy may only make the latest manifest entry optional")
	}
	return policy, nil
}

func (p historyCompatibilityPolicy) compatible(state HistoryState) bool {
	return !state.Dirty && (state.Version == p.requiredVersion || p.optionalVersion != 0 && state.Version == p.optionalVersion)
}
