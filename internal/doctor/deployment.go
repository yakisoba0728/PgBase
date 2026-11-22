package doctor

import (
	"context"
	"fmt"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
)

// SeverityWarning marks an advisory finding: a deployment risk the operator
// should see, which does not make the report fail. Report.OK only reflects
// error findings.
const SeverityWarning = "warning"

const (
	superuserRoleCode    = "database_role_superuser"
	superuserRoleMessage = "The connected PostgreSQL role is a superuser. Restoring a backup executes the archive's SQL with this role, " +
		"so an administrator who can upload and restore a backup, or anyone who can tamper with a stored archive, " +
		"can run arbitrary SQL as a superuser. Run pgBase as a non-superuser database owner role (see docs/backup-restore.md)"
	plaintextBackupEndpointCode    = "backup_endpoint_plaintext"
	plaintextBackupEndpointMessage = "The backup S3 endpoint uses plain http to a host that is not loopback or a private IP address; " +
		"access keys and backup archives travel unencrypted. Use https unless the network path is trusted (see docs/backup-s3.md)"
)

// DeploymentWarnings reports advisory deployment risks that are independent of
// schema consistency. They are kept out of Run so the administrator schema
// health view, which counts Run findings as drift, is unaffected.
//
// seedBackupEndpoint is the environment-configured S3 endpoint. It is only
// consulted when no backup storage settings row exists yet, because saved
// settings take precedence over the environment once the row is created.
func DeploymentWarnings(ctx context.Context, db database.DBTX, seedBackupEndpoint string) ([]Finding, error) {
	var superuser, settingsTable bool
	if err := db.QueryRow(ctx, `select pg_catalog.current_setting('is_superuser') = 'on',
		pg_catalog.to_regclass('pgbase_meta.backup_storage_settings') is not null`).Scan(&superuser, &settingsTable); err != nil {
		return nil, fmt.Errorf("inspect database role: %w", err)
	}
	findings := []Finding{}
	if superuser {
		findings = append(findings, warning(superuserRoleCode, superuserRoleMessage))
	}
	endpoint := seedBackupEndpoint
	if settingsTable {
		var driver, storedEndpoint *string
		if err := db.QueryRow(ctx, `select s.driver, s.s3_endpoint from (select 1) as one
			left join pgbase_meta.backup_storage_settings as s on s.singleton`).Scan(&driver, &storedEndpoint); err != nil {
			return findings, fmt.Errorf("inspect backup storage settings: %w", err)
		}
		if driver != nil {
			endpoint = ""
			if *driver == "s3" && storedEndpoint != nil {
				endpoint = *storedEndpoint
			}
		}
	}
	if backup.PlaintextEndpoint(endpoint) {
		findings = append(findings, warning(plaintextBackupEndpointCode, plaintextBackupEndpointMessage))
	}
	return findings, nil
}

func warning(code, message string) Finding {
	return Finding{Severity: SeverityWarning, Code: code, Message: message}
}
