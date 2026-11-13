//go:build restore && upgrade

package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const redesignBaseline = "763c214a754461fe244349979a55e649f875aacd"
const redesignTextCollection = "restore_text_ids"
const redesignTextID = "LegacyText01"
const redesignRealmID = "77777777-7777-4777-8777-777777777701"

// Seed only through an immutable pre-redesign binary. The dump and file
// manifest bind the exact old data to its source before the new binary starts.
func TestSchema66RedesignUpgrade(t *testing.T) {
	if os.Getenv("PGBASE_RESTORE_CANDIDATE_IMAGE") != "" {
		t.Skip("historical source compatibility is a local source drill")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Minute)
	defer cancel()
	old := newCandidateWithBinary(t, buildHistoricalCandidate(t, ctx, redesignBaseline))
	old.fileDirectory = t.TempDir()
	images := postgresMatrix()
	if len(images) == 0 {
		t.Fatal("schema 66 upgrade requires a PostgreSQL image")
	}
	currentCandidate := newCandidate(t)
	binary, err := os.ReadFile(currentCandidate.binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	t.Logf("one candidate for the complete matrix: sha256:%x", digest)
	for _, image := range images {
		t.Run("postgresql-"+image.major, func(t *testing.T) {
			postgres := startPostgres(t, ctx, old, image)
			old.fileDirectory = t.TempDir()
			expected := seedSourceProject(t, ctx, old, postgres)
			if schema := postgres.query(t, ctx, sourceDatabase, "select max(version) from pgbase_meta.system_migrations"); schema != "66" {
				t.Fatalf("immutable baseline produced schema %s, want 66", schema)
			}
			extraETag := seedSchema66Extras(t, ctx, old, postgres)
			expected.extraCollections = 1
			captureSourceProjectCounts(t, ctx, postgres, &expected)
			before := schema66Snapshot(t, ctx, postgres, sourceDatabase)
			if !strings.Contains(before["rules"], `"pocketbase"`) {
				t.Fatal("the historical fixture does not contain the original raw policy source key")
			}
			sentinel := insertReconciliationSentinel(t, ctx, postgres)
			dumpAndRestore(t, ctx, postgres)
			preserveSchema66Fixture(t, ctx, old, postgres, image, before)

			currentValue := *currentCandidate
			current := &currentValue
			current.password = old.password
			current.tokenSecret = old.tokenSecret
			current.fileDirectory = filepath.Join(t.TempDir(), "files")
			if err := os.CopyFS(current.fileDirectory, os.DirFS(old.fileDirectory)); err != nil {
				t.Fatal(err)
			}
			if output, err := current.command(ctx, current.databaseURL(postgres, restoredDatabase), "", "migrate", "up"); err != nil {
				t.Fatalf("upgrade schema 66 fixture: %v\n%s", err, output)
			}
			expected.systemMigration = mustAtoi(t, postgres.query(t, ctx, restoredDatabase, "select count(*) from pgbase_meta.system_migrations"))
			if version := mustAtoi(t, postgres.query(t, ctx, restoredDatabase, "select max(version) from pgbase_meta.system_migrations")); version < 66 {
				t.Fatalf("upgrade lowered schema to %d", version)
			}
			for name, value := range schema66Snapshot(t, ctx, postgres, restoredDatabase) {
				if value != before[name] {
					t.Errorf("upgrade changed preserved %s\nbefore: %s\nafter: %s", name, before[name], value)
				}
			}
			verifyRestoredDatabase(t, ctx, postgres, expected)
			verifyRestoredDoctor(t, ctx, current, postgres)
			t.Run("restored-service", func(t *testing.T) {
				verifyRestoredService(t, ctx, current, postgres, expected, sentinel)
			})
			t.Run("native-policy-text-id-and-realm", func(t *testing.T) {
				instance := current.serve(t, ctx, current.databaseURL(postgres, restoredDatabase), "pgbase-redesign-"+randomHex(t, 6))
				client := newAdminClient(instance.baseURL)
				client.login(t, ctx, administratorEmail, administratorPassword)
				var text map[string]any
				headers := client.call(t, ctx, http.MethodGet, recordsPath(redesignTextCollection)+"/"+redesignTextID, nil, nil, http.StatusOK, &text)
				if text["id"] != redesignTextID || text["title"] != "historical exact text identity" || headers.Get("ETag") != extraETag {
					t.Fatalf("restored text identity or ETag changed: %#v", text)
				}
				var policy struct {
					Rules struct {
						List struct {
							Expression string `json:"expression"`
						} `json:"list"`
					} `json:"rules"`
				}
				client.call(t, ctx, http.MethodGet, "/api/admin/collections/"+articleCollection+"/rules", nil, nil, http.StatusOK, &policy)
				if policy.Rules.List.Expression != "title = 'restore-relation-sentinel-revised'" {
					t.Fatalf("historical policy source lost: %#v", policy)
				}
				var realm struct {
					Items []struct {
						Email string `json:"email"`
					} `json:"items"`
				}
				client.call(t, ctx, http.MethodGet, "/api/admin/auth-collections/"+redesignRealmID+"/records", nil, nil, http.StatusOK, &realm)
				if len(realm.Items) != 1 || realm.Items[0].Email != "realm-restore@example.test" {
					t.Fatalf("realm records did not survive upgrade: %#v", realm)
				}
			})
		})
	}
}

func seedSchema66Extras(t *testing.T, ctx context.Context, old *candidate, postgres *postgresServer) string {
	t.Helper()
	instance := old.serve(t, ctx, old.databaseURL(postgres, sourceDatabase), "pgbase-schema66-seed-"+randomHex(t, 6))
	client := newAdminClient(instance.baseURL)
	client.login(t, ctx, administratorEmail, administratorPassword)
	client.applyCollection(t, ctx, map[string]any{
		"name":     redesignTextCollection,
		"recordId": map[string]any{"kind": "text", "minLength": 1, "maxLength": 32, "pattern": "^[A-Za-z0-9]+$"},
		"fields":   []map[string]any{{"name": "title", "type": "text", "required": true}},
	})
	text := client.createRecord(t, ctx, redesignTextCollection, "schema66-text-identity", map[string]any{"id": redesignTextID, "title": "historical exact text identity"})
	client.call(t, ctx, http.MethodPut, "/api/admin/collections/"+articleCollection+"/rules?compat=pocketbase", map[string]any{
		"pocketbaseRules": map[string]any{"listRule": "title = 'restore-relation-sentinel-revised'", "viewRule": nil, "createRule": nil, "updateRule": nil, "deleteRule": nil},
	}, nil, http.StatusOK, nil)
	client.call(t, ctx, http.MethodPost, "/api/admin/auth-collections", map[string]any{"id": redesignRealmID, "name": "restore_realm", "registrationEnabled": false}, nil, http.StatusCreated, nil)
	client.call(t, ctx, http.MethodPost, "/api/admin/auth-collections/"+redesignRealmID+"/records", map[string]any{"email": "realm-restore@example.test", "password": publicUserPassword}, nil, http.StatusCreated, nil)
	instance.stop(t)
	// The disabled scheduler row has no executable handler and cannot run. It
	// exercises durable scheduler data using the immutable binary's schema.
	postgres.execute(t, ctx, sourceDatabase, `INSERT INTO pgbase_meta.scheduled_jobs(id,name,schedule,enabled,next_run_at) VALUES('88888888-8888-4888-8888-888888888801','redesign-preserved-job','@every 1h',false,CURRENT_TIMESTAMP)`)
	return text.etag
}

func schema66Snapshot(t *testing.T, ctx context.Context, postgres *postgresServer, database string) map[string]string {
	t.Helper()
	queries := map[string]string{
		"rules":        `SELECT rules::text FROM pgbase_meta.collection_rules WHERE collection_id=(SELECT id FROM pgbase_meta.collections WHERE name='` + articleCollection + `')`,
		"textIdentity": `SELECT jsonb_build_object('public',i.external_id,'internal',i.internal_id,'title',r.title)::text FROM pgbase_meta.record_identities i JOIN app.restore_text_ids r ON r.id=i.internal_id WHERE i.collection_kind='base' AND i.external_id='` + redesignTextID + `'`,
		"realm":        `SELECT jsonb_build_object('id',id,'name',name,'registrationEnabled',registration_enabled)::text FROM pgbase_meta.auth_collections WHERE id='` + redesignRealmID + `'`,
		"job":          `SELECT jsonb_build_object('id',id,'name',name,'schedule',schedule,'enabled',enabled,'nextRunAt',next_run_at,'fence',fence_generation)::text FROM pgbase_meta.scheduled_jobs WHERE name='redesign-preserved-job'`,
	}
	result := make(map[string]string, len(queries))
	for name, query := range queries {
		result[name] = postgres.query(t, ctx, database, query)
		if result[name] == "" {
			t.Fatalf("empty schema66 fixture section: %s", name)
		}
	}
	return result
}

func preserveSchema66Fixture(t *testing.T, ctx context.Context, old *candidate, postgres *postgresServer, image postgresImage, snapshot map[string]string) {
	t.Helper()
	root := os.Getenv("PGBASE_REDESIGN_ARTIFACTS")
	if root == "" {
		root = t.TempDir()
	} else if !filepath.IsAbs(root) {
		root = filepath.Join(repositoryRoot(t), root)
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	directory := filepath.Join(root, "schema66-pg"+image.major)
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatalf("fixture artifacts are immutable; choose a fresh PGBASE_REDESIGN_ARTIFACTS directory: %v", err)
	}
	if output, err := dockerRun(ctx, time.Minute, "cp", postgres.container+":/tmp/pgbase.dump", filepath.Join(directory, "database.dump")); err != nil {
		t.Fatalf("retain old fixture dump: %v\n%s", err, output)
	}
	if err := os.CopyFS(filepath.Join(directory, "files"), os.DirFS(old.fileDirectory)); err != nil {
		t.Fatal(err)
	}
	hashes := map[string]string{}
	if err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		digest := sha256.Sum256(data)
		relative, err := filepath.Rel(directory, path)
		if err != nil {
			return err
		}
		hashes[filepath.ToSlash(relative)] = hex.EncodeToString(digest[:])
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	binary, err := os.ReadFile(old.binary)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(binary)
	tree := exec.CommandContext(ctx, "git", "rev-parse", redesignBaseline+"^{tree}")
	tree.Dir = repositoryRoot(t)
	sourceTree, err := tree.Output()
	if err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{"format": "pgbase.redesign-fixture/v1", "sourceCommit": redesignBaseline, "sourceTree": strings.TrimSpace(string(sourceTree)), "binarySHA256": hex.EncodeToString(digest[:]), "schema": 66, "postgresImage": image.reference, "tokenSecret": old.tokenSecret, "sha256": hashes, "snapshot": snapshot}
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "manifest.json"), append(data, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	t.Logf("immutable schema66 fixture manifest: %s", filepath.Join(directory, "manifest.json"))
}
