//go:build integration

package backup

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// Plain http stays accepted for existing private deployments; the settings
// read only flags it so the administrator page and operators can see it.
func TestBackupStorageSettingsFlagPlaintextEndpointWithoutRejectingIt(t *testing.T) {
	ctx := context.Background()
	for _, testCase := range []struct {
		endpoint string
		want     bool
	}{
		{"http://minio:9000", true},
		{"http://10.0.0.5:9000", false},
		{"https://s3.example.com", false},
	} {
		pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		if err = migrate.New(pool, nil).Up(ctx); err != nil {
			pool.Close()
			t.Fatal(err)
		}
		controller, err := NewStorageController(ctx, pool, bytes.Repeat([]byte("k"), 32), NewLocalRepository(t.TempDir()),
			StorageConfig{Driver: "s3", Bucket: "pgbase-backups", Region: "us-east-1", Endpoint: testCase.endpoint})
		if err != nil {
			pool.Close()
			t.Fatalf("%s: %v", testCase.endpoint, err)
		}
		settings, err := controller.Settings(ctx)
		pool.Close()
		if err != nil || settings.Endpoint != testCase.endpoint || settings.PlaintextEndpoint != testCase.want {
			t.Fatalf("%s: settings=%+v err=%v, want plaintextEndpoint=%v", testCase.endpoint, settings, err, testCase.want)
		}
		encoded, err := json.Marshal(settings)
		if err != nil || !strings.Contains(string(encoded), `"plaintextEndpoint":`) {
			t.Fatalf("%s: encoded=%s err=%v", testCase.endpoint, encoded, err)
		}
	}
}
