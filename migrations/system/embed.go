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

//go:embed 000010_public_users.sql
var publicUsersSQL string

//go:embed 000011_owner_access.sql
var ownerAccessSQL string

//go:embed 000012_user_email_actions.sql
var userEmailActionsSQL string

//go:embed 000013_record_files.sql
var recordFilesSQL string

//go:embed 000014_realtime_delivery.sql
var realtimeDeliverySQL string

//go:embed 000015_scheduled_jobs.sql
var scheduledJobsSQL string

//go:embed 000016_user_auth_factors.sql
var userAuthFactorsSQL string

//go:embed 000017_auth_collections.sql
var authCollectionsSQL string

//go:embed 000018_record_rules.sql
var recordRulesSQL string

//go:embed 000019_compound_indexes.sql
var compoundIndexesSQL string

//go:embed 000020_service_tokens.sql
var serviceTokensSQL string

//go:embed 000021_collection_views.sql
var collectionViewsSQL string

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
	{Version: 10, Name: "public_users", SQL: publicUsersSQL, SHA256: sha256.Sum256([]byte(publicUsersSQL))},
	{Version: 11, Name: "owner_access", SQL: ownerAccessSQL, SHA256: sha256.Sum256([]byte(ownerAccessSQL))},
	{Version: 12, Name: "user_email_actions", SQL: userEmailActionsSQL, SHA256: sha256.Sum256([]byte(userEmailActionsSQL))},
	{Version: 13, Name: "record_files", SQL: recordFilesSQL, SHA256: sha256.Sum256([]byte(recordFilesSQL))},
	{Version: 14, Name: "realtime_delivery", SQL: realtimeDeliverySQL, SHA256: sha256.Sum256([]byte(realtimeDeliverySQL))},
	{Version: 15, Name: "scheduled_jobs", SQL: scheduledJobsSQL, SHA256: sha256.Sum256([]byte(scheduledJobsSQL))},
	{Version: 16, Name: "user_auth_factors", SQL: userAuthFactorsSQL, SHA256: sha256.Sum256([]byte(userAuthFactorsSQL))},
	{Version: 17, Name: "auth_collections", SQL: authCollectionsSQL, SHA256: sha256.Sum256([]byte(authCollectionsSQL))},
	{Version: 18, Name: "record_rules", SQL: recordRulesSQL, SHA256: sha256.Sum256([]byte(recordRulesSQL))},
	{Version: 19, Name: "compound_indexes", SQL: compoundIndexesSQL, SHA256: sha256.Sum256([]byte(compoundIndexesSQL))},
	{Version: 20, Name: "service_tokens", SQL: serviceTokensSQL, SHA256: sha256.Sum256([]byte(serviceTokensSQL))},
	{Version: 21, Name: "collection_views", SQL: collectionViewsSQL, SHA256: sha256.Sum256([]byte(collectionViewsSQL))},
}

// Migrations returns the ordered system migrations. The returned slice is a
// copy, so callers cannot change the embedded migration ordering or contents.
func Migrations() []Migration {
	return append([]Migration(nil), migrations...)
}
