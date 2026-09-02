// Package system provides the embedded pgBase system schema migrations.
package system

import (
	"crypto/sha256"
	_ "embed"
)

type Migration struct {
	Version int64
	Name    string
	SQL     string
	SHA256  [32]byte
}

//go:embed 000001_initial.sql
var initialSQL string

//go:embed 000002_record_page_indexes.sql
var recordPageIndexesSQL string

//go:embed 000003_collection_update_delete.sql
var collectionUpdateDeleteSQL string

//go:embed 000004_record_operations.sql
var recordOperationsSQL string

//go:embed 000005_relation_schema.sql
var relationSchemaSQL string

//go:embed 000006_schema_operations.sql
var schemaOperationsSQL string

//go:embed 000007_record_membership.sql
var recordMembershipSQL string

//go:embed 000008_schema_companion_indexes.sql
var schemaCompanionIndexesSQL string

//go:embed 000009_schema_compiler_generation.sql
var schemaCompilerGenerationSQL string

var migrations = []Migration{
	{
		Version: 1,
		Name:    "initial",
		SQL:     initialSQL,
		SHA256:  sha256.Sum256([]byte(initialSQL)),
	},
	{
		Version: 2,
		Name:    "record_page_indexes",
		SQL:     recordPageIndexesSQL,
		SHA256:  sha256.Sum256([]byte(recordPageIndexesSQL)),
	},
	{
		Version: 3,
		Name:    "collection_update_delete",
		SQL:     collectionUpdateDeleteSQL,
		SHA256:  sha256.Sum256([]byte(collectionUpdateDeleteSQL)),
	},
	{
		Version: 4,
		Name:    "record_operations",
		SQL:     recordOperationsSQL,
		SHA256:  sha256.Sum256([]byte(recordOperationsSQL)),
	},
	{
		Version: 5,
		Name:    "relation_schema",
		SQL:     relationSchemaSQL,
		SHA256:  sha256.Sum256([]byte(relationSchemaSQL)),
	},
	{
		Version: 6,
		Name:    "schema_operations",
		SQL:     schemaOperationsSQL,
		SHA256:  sha256.Sum256([]byte(schemaOperationsSQL)),
	},
	{
		Version: 7,
		Name:    "record_membership",
		SQL:     recordMembershipSQL,
		SHA256:  sha256.Sum256([]byte(recordMembershipSQL)),
	},
	{
		Version: 8,
		Name:    "schema_companion_indexes",
		SQL:     schemaCompanionIndexesSQL,
		SHA256:  sha256.Sum256([]byte(schemaCompanionIndexesSQL)),
	},
	{
		Version: 9,
		Name:    "schema_compiler_generation",
		SQL:     schemaCompilerGenerationSQL,
		SHA256:  sha256.Sum256([]byte(schemaCompilerGenerationSQL)),
	},
}

// Migrations returns the ordered system migrations. The returned slice is a
// copy, so callers cannot change the embedded migration ordering or contents.
func Migrations() []Migration {
	return append([]Migration(nil), migrations...)
}
