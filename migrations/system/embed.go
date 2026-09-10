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

//go:embed 000022_field_contracts.sql
var fieldContractsSQL string

//go:embed 000023_auth_administration.sql
var authAdministrationSQL string

//go:embed 000024_file_variants.sql
var fileVariantsSQL string

//go:embed 000025_request_logs_and_calendar_jobs.sql
var requestLogsCalendarSQL string

//go:embed 000026_auth_mail_html.sql
var authMailHTMLSQL string

//go:embed 000027_sql_collection_views.sql
var sqlCollectionViewsSQL string

//go:embed 000028_oauth_callback_bridge.sql
var oauthCallbackBridgeSQL string

//go:embed 000029_field_presentation.sql
var fieldPresentationSQL string

//go:embed 000030_email_otp_login.sql
var emailOTPLoginSQL string

//go:embed 000031_schema_import_operations.sql
var schemaImportOperationsSQL string

//go:embed 000032_admin_impersonation.sql
var adminImpersonationSQL string

//go:embed 000033_advanced_fields.sql
var advancedFieldsSQL string

//go:embed 000034_s3_settings.sql
var s3SettingsSQL string

//go:embed 000035_user_migrations.sql
var userMigrationsSQL string

//go:embed 000036_file_storage_migrations.sql
var fileStorageMigrationsSQL string

//go:embed 000037_backup_storage_settings.sql
var backupStorageSettingsSQL string

//go:embed 000038_backup_storage_operations.sql
var backupStorageOperationsSQL string

//go:embed 000039_admin_mfa_network_policy.sql
var adminMFANetworkPolicySQL string

//go:embed 000040_user_migration_sources.sql
var userMigrationSourcesSQL string

//go:embed 000041_sql_console.sql
var sqlConsoleSQL string

//go:embed 000042_schema_compatibility.sql
var schemaCompatibilitySQL string

//go:embed 000043_auth_pb_compat.sql
var authPBCompatSQL string

//go:embed 000045_runtime_settings.sql
var runtimeSettingsSQL string

//go:embed 000048_auth_oidc_compat.sql
var authOIDCCompatSQL string

//go:embed 000050_auth_token_policies.sql
var authTokenPoliciesSQL string

//go:embed 000051_admin_password_resets.sql
var adminPasswordResetsSQL string

//go:embed 000054_runtime_settings_encryption.sql
var runtimeSettingsEncryptionSQL string

//go:embed 000056_admin_email_otps.sql
var adminEmailOTPsSQL string

//go:embed 000057_extension_objects.sql
var extensionObjectsSQL string

//go:embed 000058_api_rate_limits.sql
var apiRateLimitsSQL string

//go:embed 000059_extension_object_inventory.sql
var extensionObjectInventorySQL string

//go:embed 000060_nullable_auth_rules.sql
var nullableAuthRulesSQL string

//go:embed 000061_record_identities.sql
var recordIdentitiesSQL string

//go:embed 000062_auth_email_visibility.sql
var authEmailVisibilitySQL string

//go:embed 000063_advanced_collection_indexes.sql
var advancedCollectionIndexesSQL string

//go:embed 000052_auth_record_model.sql
var authRecordModelSQL string

//go:embed 000055_request_log_context.sql
var requestLogContextSQL string

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
	{Version: 22, Name: "field_contracts", SQL: fieldContractsSQL, SHA256: sha256.Sum256([]byte(fieldContractsSQL))},
	{Version: 23, Name: "auth_administration", SQL: authAdministrationSQL, SHA256: sha256.Sum256([]byte(authAdministrationSQL))},
	{Version: 24, Name: "file_variants", SQL: fileVariantsSQL, SHA256: sha256.Sum256([]byte(fileVariantsSQL))},
	{Version: 25, Name: "request_logs_and_calendar_jobs", SQL: requestLogsCalendarSQL, SHA256: sha256.Sum256([]byte(requestLogsCalendarSQL))},
	{Version: 26, Name: "auth_mail_html", SQL: authMailHTMLSQL, SHA256: sha256.Sum256([]byte(authMailHTMLSQL))},
	{Version: 27, Name: "sql_collection_views", SQL: sqlCollectionViewsSQL, SHA256: sha256.Sum256([]byte(sqlCollectionViewsSQL))},
	{Version: 28, Name: "oauth_callback_bridge", SQL: oauthCallbackBridgeSQL, SHA256: sha256.Sum256([]byte(oauthCallbackBridgeSQL))},
	{Version: 29, Name: "field_presentation", SQL: fieldPresentationSQL, SHA256: sha256.Sum256([]byte(fieldPresentationSQL))},
	{Version: 30, Name: "email_otp_login", SQL: emailOTPLoginSQL, SHA256: sha256.Sum256([]byte(emailOTPLoginSQL))},
	{Version: 31, Name: "schema_import_operations", SQL: schemaImportOperationsSQL, SHA256: sha256.Sum256([]byte(schemaImportOperationsSQL))},
	{Version: 32, Name: "admin_impersonation", SQL: adminImpersonationSQL, SHA256: sha256.Sum256([]byte(adminImpersonationSQL))},
	{Version: 33, Name: "advanced_fields", SQL: advancedFieldsSQL, SHA256: sha256.Sum256([]byte(advancedFieldsSQL))},
	{Version: 34, Name: "s3_settings", SQL: s3SettingsSQL, SHA256: sha256.Sum256([]byte(s3SettingsSQL))},
	{Version: 35, Name: "user_migrations", SQL: userMigrationsSQL, SHA256: sha256.Sum256([]byte(userMigrationsSQL))},
	{Version: 36, Name: "file_storage_migrations", SQL: fileStorageMigrationsSQL, SHA256: sha256.Sum256([]byte(fileStorageMigrationsSQL))},
	{Version: 37, Name: "backup_storage_settings", SQL: backupStorageSettingsSQL, SHA256: sha256.Sum256([]byte(backupStorageSettingsSQL))},
	{Version: 38, Name: "backup_storage_operations", SQL: backupStorageOperationsSQL, SHA256: sha256.Sum256([]byte(backupStorageOperationsSQL))},
	{Version: 39, Name: "admin_mfa_network_policy", SQL: adminMFANetworkPolicySQL, SHA256: sha256.Sum256([]byte(adminMFANetworkPolicySQL))},
	{Version: 40, Name: "user_migration_sources", SQL: userMigrationSourcesSQL, SHA256: sha256.Sum256([]byte(userMigrationSourcesSQL))},
	{Version: 41, Name: "sql_console", SQL: sqlConsoleSQL, SHA256: sha256.Sum256([]byte(sqlConsoleSQL))},
	{Version: 42, Name: "schema_compatibility", SQL: schemaCompatibilitySQL, SHA256: sha256.Sum256([]byte(schemaCompatibilitySQL))},
	{Version: 43, Name: "auth_pb_compat", SQL: authPBCompatSQL, SHA256: sha256.Sum256([]byte(authPBCompatSQL))},
	{Version: 45, Name: "runtime_settings", SQL: runtimeSettingsSQL, SHA256: sha256.Sum256([]byte(runtimeSettingsSQL))},
	{Version: 48, Name: "auth_oidc_compat", SQL: authOIDCCompatSQL, SHA256: sha256.Sum256([]byte(authOIDCCompatSQL))},
	{Version: 50, Name: "auth_token_policies", SQL: authTokenPoliciesSQL, SHA256: sha256.Sum256([]byte(authTokenPoliciesSQL))},
	{Version: 51, Name: "admin_password_resets", SQL: adminPasswordResetsSQL, SHA256: sha256.Sum256([]byte(adminPasswordResetsSQL))},
	{Version: 52, Name: "auth_record_model", SQL: authRecordModelSQL, SHA256: sha256.Sum256([]byte(authRecordModelSQL))},
	{Version: 54, Name: "runtime_settings_encryption", SQL: runtimeSettingsEncryptionSQL, SHA256: sha256.Sum256([]byte(runtimeSettingsEncryptionSQL))},
	{Version: 55, Name: "request_log_context", SQL: requestLogContextSQL, SHA256: sha256.Sum256([]byte(requestLogContextSQL))},
	{Version: 56, Name: "admin_email_otps", SQL: adminEmailOTPsSQL, SHA256: sha256.Sum256([]byte(adminEmailOTPsSQL))},
	{Version: 57, Name: "extension_objects", SQL: extensionObjectsSQL, SHA256: sha256.Sum256([]byte(extensionObjectsSQL))},
	{Version: 58, Name: "api_rate_limits", SQL: apiRateLimitsSQL, SHA256: sha256.Sum256([]byte(apiRateLimitsSQL))},
	{Version: 59, Name: "extension_object_inventory", SQL: extensionObjectInventorySQL, SHA256: sha256.Sum256([]byte(extensionObjectInventorySQL))},
	{Version: 60, Name: "nullable_auth_rules", SQL: nullableAuthRulesSQL, SHA256: sha256.Sum256([]byte(nullableAuthRulesSQL))},
	{Version: 61, Name: "record_identities", SQL: recordIdentitiesSQL, SHA256: sha256.Sum256([]byte(recordIdentitiesSQL))},
	{Version: 62, Name: "auth_email_visibility", SQL: authEmailVisibilitySQL, SHA256: sha256.Sum256([]byte(authEmailVisibilitySQL))},
	{Version: 63, Name: "advanced_collection_indexes", SQL: advancedCollectionIndexesSQL, SHA256: sha256.Sum256([]byte(advancedCollectionIndexesSQL))},
}

// Migrations returns the ordered system migrations. The returned slice is a
// copy, so callers cannot change the embedded migration ordering or contents.
func Migrations() []Migration {
	return append([]Migration(nil), migrations...)
}
