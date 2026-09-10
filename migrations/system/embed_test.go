package system_test

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

func TestHistoricalMigrationChecksumsRemainImmutable(t *testing.T) {
	want := map[int64]string{
		1:  "c1fc7648bae60359cb3adcb11feae210b02b54a79fd5933abfb1cd3e2736fca4",
		2:  "fe3eebdc35d995593d3185169e61670b37a1c5906d06562649a00c50448c32fb",
		3:  "effa5a5895a8ec387c73b4a0afdb1ed3aeb154f09f3d0825862f00d6237c1303",
		4:  "747f4377b19f70e2bdeaf77e5ee9b96f0bb019f0ba0a6d5f7d856d8adb90751b",
		5:  "576df4a578efbd1259cf5dbf22da8d7669bd729e33d2ef1d1b4ed30e1d16197a",
		6:  "6c3f728926d04b9bbeb81152df6eede97de155ddd2bb503a43faba4af0d6ec7a",
		7:  "8c8f2a4b0506d4abaffc19097a090f84f424ef521ec9575ac4577ecaa1b3432e",
		8:  "e1cf6ca6e847d1b0593c6def69c5c3a65c0916efe1011fbdacedb87f72177cff",
		9:  "19baaeab06d5b9c23333ae5c14cf0c6c806360e1dabc958c35c24263225d35c6",
		10: "7b8f592b013ddac0030fd0c86502895ea6aad2985b841cd72ff1ca8371d242d6",
		11: "495ebb9be48ca51a4bbf551dbc7227e090dade8d99e99e11626928dfa2005f46",
		12: "be38595641afed3679625e82aca681e5bfc3eaf9a456598f533fdca1f7fa03e5",
		13: "86cb31bfe2eb5395c8c9ce9cbaa0995f5cc691e8a3f63584836fda8283263d96",
		14: "a6b10d7298eaa5b50f9ec38d1d905aaec1bd2b3a8db028be7258acfd87bacee2",
		15: "e1d6c22dc0956bdd629454b3998f06ddeab3f5dfb0c03dc20012d389a86bbae8",
		16: "be29348f64030d2f8f6b7a3d34ab40ee53b5cedb530d11a5f4254e8a634b5f39",
		17: "32c70d1b44e5cb71984b7f52b6267636cb331cba9742887bcf9ee67b3dbc95f6",
		18: "f9ebb300d2cebb3240718102fe7b46bf4de7a43dd90bf3bc117f769a9f29316c",
		19: "54b98e728f384ec299adba736d3377510223ade4759663d3e765da8d06ec3fbc",
		20: "c3e73309d202eee4831dcd51b8236805a19687dc0dcf1ec9a701bbcb96a4aa7e",
		21: "11799b1af659bac9bf2bb7aa018bbb0107b48a63a04021860af834d8947d2047",
		22: "3e7fc37b22960f2fc70cddc5a359a62f4efbe518082ea26dbc7df35c305482f8",
		23: "f885af22a200b5d821240641c79831e553b6f49d2515adf7852cc8aca82eeaf1",
		24: "ec66d8252d0a7e18185b2641b131d045068bf63b085b65690217a284e154ac3c",
		25: "a5ac68896bae6c425b5d923da6f056e2caa6a8aa0f0450b0e0c1a12c118be1d7",
		26: "96ea7b54ebeec0695ff15e855aea2304097584a707e52c1f1a92892a27b9033c",
		27: "e7ade857ac4b00edd45b7a60e82a809347860b39a9ee8be10ccd46e9b78a6498",
		28: "681cd06ef208148def50dee619e4dc25d7adbe440c274d0f96be78126412ad55",
		29: "d3fb7d4d46e0ad3c6a923b02150c42eac76e2c3978eda4fe35ea435fab51eb68",
		30: "c0abd6cb2699e9ab1d563ec2dd14b254e1e550bf174a41360aad1354836a1a04",
		31: "9d69b647922968b13e38ae5296b4ce14fae058181cf7990c01f0a6d2d85b76ca",
		32: "eaee2beac3dfbf8ee9138ade05511adfefd8f06b60fa659c49188edb3681ef1b",
		33: "7cc64253f7fb79635b13c70f4cea3666c56b1410712c9f51f122af44e8ca1100",
		34: "3852567d4227770481d0a80b2409d21c40ae2ddb1f03e64b875b37bb7a163a3b",
		35: "e54150f3de976e134befa4041764b3b81252efc2c1c0499feff5727d8fb40625",
		36: "9b5905e5e63918a41ac2c95ebe92332828b2aa162b9d72670b054756f0362b1c",
		37: "68488c5f1ac96f1b6c5ad09f9e4e7d115c32d1e0832df5ff8d3d0e1b8274b3b2",
		38: "530ffdd30ea33fb293469e050abac8a5a1ebed055bcf94634d9d4336d6f89b97",
		39: "c6acb137040bbe69bc7f276aa74f0f1b94f1aa2bfd5fdc3f1d109dbffe2d35ed",
		40: "ffecb2cff732be1c1922fa893d61ba3f0ad77c1d8c8d1e2159ece97c967d64ee",
		41: "628b3920dd97db4b419ba7f2ed4b3b921911a07d374033ad8e2f0a849871d699",
		42: "8ab7ffccff22509e2b3186eca18420b5428b827ec9279a36eac7920f91a2fd92",
		43: "d7fb1735466f7cfa7463ad95d7c312bc704350115eab8fe8ad5f0a78e784e6b0",
		45: "43dddfefca04b239547c579dda39c8ddd348fdbd5ec85f3018cc9805c42141c7",
		48: "a8ce4d47642feadb70d9c1dfa5466b3448b37ba506535d2a09dd7c9d52c88c3f",
	}
	migrations := system.Migrations()
	if len(migrations) != 45 || migrations[len(migrations)-1].Version != 48 ||
		migrations[len(migrations)-1].Name != "auth_oidc_compat" || migrations[7].Version != 8 ||
		migrations[7].Name != "schema_companion_indexes" ||
		migrations[8].Version != 9 || migrations[8].Name != "schema_compiler_generation" ||
		migrations[9].Version != 10 || migrations[9].Name != "public_users" ||
		migrations[10].Version != 11 || migrations[10].Name != "owner_access" {
		t.Fatalf("migration ordering invalid; count=%d", len(migrations))
	}
	// Every applied migration is pinned, so adding one without its checksum
	// fails here instead of at the runner of an already migrated database.
	if len(want) != len(migrations) {
		t.Fatalf("pinned checksums=%d migrations=%d", len(want), len(migrations))
	}
	for _, migration := range migrations {
		if got := hex.EncodeToString(migration.SHA256[:]); got != want[migration.Version] {
			t.Fatalf("historical migration %d checksum=%s want=%s", migration.Version, got, want[migration.Version])
		}
	}
	copyOfMigrations := system.Migrations()
	copyOfMigrations[0].Name = "changed"
	if system.Migrations()[0].Name != "initial" {
		t.Fatal("Migrations returned mutable ordering state")
	}
}

func newRecordOperationsSchema(t *testing.T) (context.Context, *database.Pool) {
	t.Helper()
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 10*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	for _, migration := range system.Migrations() {
		if _, err := pool.Exec(ctx, migration.SQL); err != nil {
			t.Fatalf("apply migration %d: %v", migration.Version, err)
		}
	}
	return ctx, pool
}

func insertConsistentRecordArtifacts(t *testing.T, ctx context.Context, pool *database.Pool) {
	t.Helper()
	_, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.collections
		(id, name, table_name, status, created_at, updated_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000100', 'records', 'records',
		 'active', transaction_timestamp(), transaction_timestamp());

		INSERT INTO pgbase_meta.record_audit_events
		(event_id, actor_kind, actor_id, request_id, collection_id, collection_name,
		 record_id, operation, schema_version, schema_digest, previous_version,
		 new_version, previous_etag, new_etag, changed_fields, input_hash,
		 idempotency_key_hash, compatibility, committed_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000102', 'administrator',
		 '019551d0-e9c0-7000-8000-000000000103', 'request01',
		 '019551d0-e9c0-7000-8000-000000000100', 'records',
		 '019551d0-e9c0-7000-8000-000000000101', 'create', 1,
		 decode(repeat('aa', 32), 'hex'), NULL, 1, NULL, '"pgb1.test"',
		 ARRAY['title'], decode(repeat('bb', 32), 'hex'),
		 decode(repeat('cc', 32), 'hex'), 'none', transaction_timestamp());

		INSERT INTO pgbase_meta.record_revisions
		(collection_id, record_id, version, validator, last_event_id, updated_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000100',
		 '019551d0-e9c0-7000-8000-000000000101', 1,
		 decode(repeat('dd', 32), 'hex'),
		 '019551d0-e9c0-7000-8000-000000000102', transaction_timestamp());

		INSERT INTO pgbase_meta.record_outbox
		(event_id, topic, collection_id, collection_name, record_id, operation,
		 record_version, envelope, created_at)
		VALUES ('019551d0-e9c0-7000-8000-000000000102', 'record.mutation.v1',
		 '019551d0-e9c0-7000-8000-000000000100', 'records',
		 '019551d0-e9c0-7000-8000-000000000101', 'create', 1,
		 ('{"version":1,"eventId":"019551d0-e9c0-7000-8000-000000000102",' ||
		 '"collectionId":"019551d0-e9c0-7000-8000-000000000100",' ||
		 '"collectionName":"records",' ||
		 '"recordId":"019551d0-e9c0-7000-8000-000000000101",' ||
		 '"operation":"create","recordVersion":1,"changes":{"title":"ok"}}')::jsonb,
		 transaction_timestamp());

		INSERT INTO pgbase_meta.record_idempotency
		(actor_kind, actor_id, collection_id, key_hash, request_hash, record_id,
		 event_id, response_body, etag, created_at, expires_at)
		VALUES ('administrator', '019551d0-e9c0-7000-8000-000000000103',
		 '019551d0-e9c0-7000-8000-000000000100', decode(repeat('cc', 32), 'hex'),
		 decode(repeat('bb', 32), 'hex'), '019551d0-e9c0-7000-8000-000000000101',
		 '019551d0-e9c0-7000-8000-000000000102',
		 '{"id":"019551d0-e9c0-7000-8000-000000000101","title":"ok"}',
		 '"pgb1.test"', transaction_timestamp(), transaction_timestamp() + interval '24 hours')`)
	if err != nil {
		t.Fatalf("insert consistent record artifacts: %v", err)
	}
}

func requireSQLState(t *testing.T, ctx context.Context, pool *database.Pool, statement, want string) {
	t.Helper()
	_, err := pool.Exec(ctx, statement)
	requireErrorSQLState(t, err, want)
}

func requireErrorSQLState(t *testing.T, err error, want string) {
	t.Helper()
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != want {
		t.Fatalf("statement error=%v SQLSTATE=%q want=%q", err, postgresErrorCode(postgresError), want)
	}
}

func postgresErrorCode(err *pgconn.PgError) string {
	if err == nil {
		return ""
	}
	return err.Code
}
