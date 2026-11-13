//go:build restore

package restore

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// This test never substitutes a host binary or mounts host PostgreSQL clients.
// It exercises the built stock image's managed online backup and activation.
func TestStockImageManagedBackupRestore(t *testing.T) {
	image := os.Getenv("PGBASE_RUNTIME_BACKUP_IMAGE")
	if image == "" {
		t.Skip("set PGBASE_RUNTIME_BACKUP_IMAGE to the built stock image")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	user, err := dockerRun(ctx, time.Minute, "image", "inspect", "--format", "{{.Config.User}}", image)
	if err != nil || strings.TrimSpace(user) != "nonroot:nonroot" {
		t.Fatalf("stock image user=%q error=%v", user, err)
	}
	for _, version := range supportedPostgresMajors {
		for _, tool := range []string{"pg_dump", "pg_restore"} {
			output, err := dockerRun(ctx, time.Minute, "run", "--rm", "--entrypoint", "/opt/pgbase/postgresql/"+version+"/bin/"+tool, image, "--version")
			if err != nil || !strings.Contains(output, "PostgreSQL) "+version+".") {
				t.Fatalf("packaged %s %s: %v\n%s", tool, version, err, output)
			}
		}
	}
	for _, postgresImage := range postgresMatrix() {
		t.Run("postgresql-"+postgresImage.major, func(t *testing.T) {
			network := "pgbase-runtime-net-" + randomHex(t, 6)
			if out, err := dockerRun(ctx, time.Minute, "network", "create", network); err != nil {
				t.Fatalf("network: %v %s", err, out)
			}
			t.Cleanup(func() { _, _ = dockerRun(context.Background(), time.Minute, "network", "rm", network) })
			drill := &candidate{release: true, image: image, network: network, password: randomHex(t, 24), tokenSecret: base64.StdEncoding.EncodeToString([]byte(randomHex(t, 16)))}
			database := &postgresServer{container: "pgbase-runtime-db-" + randomHex(t, 6), alias: "postgres", major: postgresImage.major}
			if out, err := dockerRun(ctx, 2*time.Minute, "run", "--detach", "--name", database.container, "--network", network, "--network-alias", "postgres", "--env", "POSTGRES_USER="+databaseUser, "--env", "POSTGRES_PASSWORD="+drill.password, "--env", "POSTGRES_DB="+sourceDatabase, postgresImage.reference); err != nil {
				t.Fatalf("database: %v %s", err, out)
			}
			t.Cleanup(func() {
				_, _ = dockerRun(context.Background(), time.Minute, "rm", "--force", "--volumes", database.container)
			})
			runtimePoll(t, ctx, func() bool {
				_, err := database.exec(ctx, "psql", "--host", "127.0.0.1", "--username", databaseUser, "--dbname", sourceDatabase, "--no-psqlrc", "--command", "select 1")
				return err == nil
			})
			url := drill.databaseURL(database, sourceDatabase)
			if out, err := drill.command(ctx, url, "", "migrate", "up"); err != nil {
				t.Fatalf("migrate: %v %s", err, out)
			}
			if out, err := drill.command(ctx, url, administratorPassword+"\n", "admin", "create", "--email", administratorEmail, "--password-stdin"); err != nil {
				t.Fatalf("admin: %v %s", err, out)
			}
			port := freePort(t)
			baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
			instance := &server{container: "pgbase-runtime-app-" + randomHex(t, 6), baseURL: baseURL}
			arguments := []string{"run", "--detach", "--read-only", "--tmpfs", "/tmp:rw,noexec,nosuid,size=16777216,uid=65532,gid=65532,mode=1777", "--name", instance.container, "--network", network, "--publish", fmt.Sprintf("127.0.0.1:%d:8090", port), "--tmpfs", "/runtime:rw,uid=65532,gid=65532,mode=0700"}
			environment := drill.environment(url, "0.0.0.0:8090", baseURL)
			environment = append(environment, "PGBASE_BACKUPS_DIRECTORY=/runtime/backups", "PGBASE_FILES_DRIVER=local", "PGBASE_FILES_LOCAL_DIRECTORY=/runtime/files")
			for _, value := range environment {
				arguments = append(arguments, "--env", value)
			}
			arguments = append(arguments, image, "serve")
			if out, err := dockerRun(ctx, time.Minute, arguments...); err != nil {
				t.Fatalf("serve: %v %s", err, out)
			}
			t.Cleanup(func() { _, _ = dockerRun(context.Background(), time.Minute, "rm", "--force", instance.container) })
			defer func() {
				if t.Failed() {
					t.Log(instance.diagnostics())
				}
			}()
			instance.waitReady(t, ctx)
			client := newAdminClient(baseURL)
			client.login(t, ctx, administratorEmail, administratorPassword)
			fileID := seedRestoreFile(t, ctx, client)
			var created struct{ ID, BackupID, Status string }
			client.call(t, ctx, http.MethodPost, "/api/admin/backups", nil, nil, http.StatusAccepted, &created)
			runtimeBackupJob(t, ctx, client, created.ID)
			var entries struct {
				Items []struct {
					ID              string
					PostgreSQLMajor int `json:"postgresqlMajor"`
				}
			}
			client.call(t, ctx, http.MethodGet, "/api/admin/backups", nil, nil, http.StatusOK, &entries)
			if len(entries.Items) != 1 || entries.Items[0].PostgreSQLMajor != mustAtoi(t, postgresImage.major) {
				t.Fatalf("backup entries=%+v", entries)
			}
			// Write a post-snapshot record which must disappear on activation.
			sentinel := client.createRecord(t, ctx, "restore_files", "after-managed-snapshot", map[string]any{"title": "must disappear"})
			var restored struct{ ID string }
			client.call(t, ctx, http.MethodPost, "/api/admin/backups/"+entries.Items[0].ID+"/restore", nil, nil, http.StatusAccepted, &restored)
			runtimeBackupJob(t, ctx, client, restored.ID)
			instance.waitReady(t, ctx)
			client = newAdminClient(baseURL)
			client.login(t, ctx, administratorEmail, administratorPassword)
			result, err := client.exchange(ctx, http.MethodGet, "/api/admin/collections/restore_files/records/"+sentinel.id, nil, nil)
			if err != nil || result.status != http.StatusNotFound {
				t.Fatalf("post-snapshot record survived: status=%d error=%v", result.status, err)
			}
			verifyRestoreFile(t, ctx, client, fileID)
			t.Logf("stock image %s: PostgreSQL %s online backup, isolated restore, restored file bytes, nonroot runtime PASS", image, postgresImage.major)
		})
	}
}

func runtimePoll(t *testing.T, ctx context.Context, ready func() bool) {
	t.Helper()
	wait, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		if ready() {
			return
		}
		select {
		case <-wait.Done():
			t.Fatalf("runtime operation did not complete: %v", wait.Err())
		case <-ticker.C:
		}
	}
}
func runtimeBackupJob(t *testing.T, ctx context.Context, client *adminClient, id string) {
	t.Helper()
	if id == "" {
		t.Fatal("missing managed backup job identity")
	}
	runtimePoll(t, ctx, func() bool {
		result, err := client.exchange(ctx, http.MethodGet, "/api/admin/backup-jobs/"+id, nil, nil)
		if err != nil || result.status == http.StatusServiceUnavailable {
			return false
		}
		if result.status != http.StatusOK {
			t.Fatalf("job status=%d body=%s error=%v", result.status, result.body, err)
		}
		var job struct{ Status, Error string }
		if err := json.Unmarshal(result.body, &job); err != nil {
			t.Fatal(err)
		}
		if job.Status == "failed" {
			t.Fatalf("managed backup job failed: %s", job.Error)
		}
		return job.Status == "completed"
	})
}
