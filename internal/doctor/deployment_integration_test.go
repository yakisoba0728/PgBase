//go:build integration

package doctor

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

func deploymentWarningCodes(t *testing.T, db database.DBTX, seedEndpoint string) []string {
	t.Helper()
	findings, err := DeploymentWarnings(context.Background(), db, seedEndpoint)
	if err != nil {
		t.Fatal(err)
	}
	codes := []string{}
	for _, finding := range findings {
		if finding.Severity != SeverityWarning || finding.Message == "" {
			t.Fatalf("finding=%+v, want a warning with a message", finding)
		}
		codes = append(codes, finding.Code)
	}
	return codes
}

func TestDeploymentWarningsReportSuperuserRoleAndPlaintextBackupEndpoint(t *testing.T) {
	ctx := context.Background()
	pool := newDoctorPool(t)

	// The integration database is reached as a superuser, like the bundled
	// Compose deployment. With no settings row the environment seed decides.
	if got := deploymentWarningCodes(t, pool, ""); !slices.Equal(got, []string{superuserRoleCode}) {
		t.Fatalf("superuser without backups: %v", got)
	}
	if got := deploymentWarningCodes(t, pool, "http://203.0.113.10:9000"); !slices.Equal(got, []string{superuserRoleCode, plaintextBackupEndpointCode}) {
		t.Fatalf("plaintext seed endpoint: %v", got)
	}
	if got := deploymentWarningCodes(t, pool, "http://127.0.0.1:9000"); !slices.Equal(got, []string{superuserRoleCode}) {
		t.Fatalf("loopback seed endpoint: %v", got)
	}

	// Once saved settings exist they take precedence over the environment.
	if _, err := pool.Exec(ctx, `insert into pgbase_meta.backup_storage_settings(singleton,driver,s3_bucket,s3_region,s3_endpoint)
		values(true,'s3','bucket','us-east-1','http://minio:9000')`); err != nil {
		t.Fatal(err)
	}
	if got := deploymentWarningCodes(t, pool, "https://s3.example.com"); !slices.Equal(got, []string{superuserRoleCode, plaintextBackupEndpointCode}) {
		t.Fatalf("stored plaintext endpoint: %v", got)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.backup_storage_settings set s3_endpoint='http://10.0.0.5:9000'`); err != nil {
		t.Fatal(err)
	}
	if got := deploymentWarningCodes(t, pool, "http://203.0.113.10:9000"); !slices.Equal(got, []string{superuserRoleCode}) {
		t.Fatalf("stored private endpoint: %v", got)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.backup_storage_settings set driver='local', s3_endpoint='http://203.0.113.10:9000'`); err != nil {
		t.Fatal(err)
	}
	if got := deploymentWarningCodes(t, pool, "http://203.0.113.10:9000"); !slices.Equal(got, []string{superuserRoleCode}) {
		t.Fatalf("stored local driver: %v", got)
	}

	// A non-superuser owner role gets neither warning for a safe target.
	if _, err := pool.Exec(ctx, `update pgbase_meta.backup_storage_settings set driver='s3', s3_endpoint='https://s3.example.com'`); err != nil {
		t.Fatal(err)
	}
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		role := pgx.Identifier{"pgbase_doctor_" + strings.ReplaceAll(uuid.NewString(), "-", "")}.Sanitize()
		// Role creation is transactional, so the rollback below removes it.
		if _, err := tx.Exec(ctx, "create role "+role+" nologin nosuperuser;"+
			"grant usage on schema pgbase_meta to "+role+";"+
			"grant select on pgbase_meta.backup_storage_settings to "+role+";"+
			"set local role "+role); err != nil {
			return err
		}
		if got := deploymentWarningCodes(t, tx, ""); len(got) != 0 {
			t.Fatalf("non-superuser role: %v", got)
		}
		return errRollbackDeploymentProbe
	}); !errors.Is(err, errRollbackDeploymentProbe) {
		t.Fatal(err)
	}
}

var errRollbackDeploymentProbe = errors.New("roll back deployment probe")
