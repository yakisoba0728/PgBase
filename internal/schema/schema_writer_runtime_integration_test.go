//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/migrations/system"
)

// A ready listener is one running pgbase process. Even without an Apply, it
// must be present in the durable fleet proof before an operator can activate a
// plan generation that would fence this process out.
func TestListenerReadinessIncludesAnIdleSchemaWriterLease(t *testing.T) {
	pool, _, _ := newSchemaServiceTest(t)
	cache := &Cache{}
	service := NewService(pool, cache, nil)
	listener := NewListener(pool, cache, nil, service)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	waitForListenerReady(t, listener)

	leases, err := service.SchemaWriterFleet(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(leases) != 1 || leases[0].CompilerVersion != schemaCompilerVersion {
		t.Fatalf("idle ready writer fleet = %#v, want one generation-%d lease", leases, schemaCompilerVersion)
	}

	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("listener run: %v", err)
	}
}

func TestP0IdleWriterLeaseSurvivesMigrationNineWithoutRestart(t *testing.T) {
	if schemaCompilerVersion != 2 {
		return
	}
	databaseURL := testutil.NewDatabase(t)
	migrations := system.Migrations()
	testutil.ApplySystemMigrationPrefix(t, databaseURL, migrations[:8])
	pool := openSchemaListenerPool(t, databaseURL)
	service := NewService(pool, &Cache{}, nil)
	listener := NewListener(pool, service.cache, nil, service)
	listener.writerHeartbeatInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("listener run: %v", err)
		}
	})
	waitForListenerReady(t, listener)
	var instanceID uuid.UUID
	var before time.Time
	if err := pool.QueryRow(context.Background(), `select instance_id, heartbeat_at
		from pgbase_meta.schema_writer_leases where compiler_version = 2
		and lease_expires_at > clock_timestamp()`).Scan(&instanceID, &before); err != nil {
		t.Fatalf("live P0 writer before migration 9: %v", err)
	}
	if _, err := migrate.New(pool, nil).UpThrough(context.Background(), 9); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		leases, err := service.SchemaWriterFleet(context.Background())
		var protocol int16
		var heartbeat time.Time
		protocolErr := pool.QueryRow(context.Background(), `select writer_protocol, heartbeat_at
			from pgbase_meta.schema_writer_leases where instance_id = $1`, instanceID).Scan(&protocol, &heartbeat)
		if err == nil && protocolErr == nil && listener.Ready(context.Background()) == nil &&
			len(leases) == 1 && leases[0].CompilerVersion == 2 && protocol == 1 && heartbeat.After(before) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("running P0 did not re-register after migration 9")
}

func TestListenerRejectsSchemaWriterCatalogDriftAtSupportedMigrations(t *testing.T) {
	tests := []struct {
		name            string
		migrationPrefix int
		breakCatalog    string
		verifyApply     bool
	}{
		{
			name:            "v8 missing lease table",
			migrationPrefix: 8,
			breakCatalog:    `drop table pgbase_meta.schema_writer_leases`,
			verifyApply:     true,
		},
		{
			name:            "v8 missing capability table",
			migrationPrefix: 8,
			breakCatalog:    `drop table pgbase_meta.schema_companion_index_capability`,
		},
		{
			name:            "v8 has unrecorded generation markers",
			migrationPrefix: 8,
			breakCatalog: `alter table pgbase_meta.schema_writer_leases
				add column writer_protocol smallint not null default 1;
				alter table pgbase_meta.schema_companion_index_capability
				add column proven_compiler_version smallint`,
		},
		{
			name:            "v9 missing lease table",
			migrationPrefix: 9,
			breakCatalog:    `drop table pgbase_meta.schema_writer_leases`,
		},
		{
			name:            "v9 missing capability table",
			migrationPrefix: 9,
			breakCatalog:    `drop table pgbase_meta.schema_companion_index_capability`,
		},
		{
			name:            "v9 generation markers removed",
			migrationPrefix: 9,
			breakCatalog: `alter table pgbase_meta.schema_writer_leases
				drop column writer_protocol cascade;
				alter table pgbase_meta.schema_companion_index_capability
				drop column proven_compiler_version cascade`,
			verifyApply: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			databaseURL := testutil.NewDatabase(t)
			testutil.ApplySystemMigrationPrefix(t, databaseURL, system.Migrations()[:test.migrationPrefix])
			pool := openSchemaListenerPool(t, databaseURL)
			ctx := context.Background()
			var applyService *Service
			var applyIntent ApplyIntent
			if test.verifyApply {
				administratorID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000001")
				if _, err := pool.Exec(ctx, `insert into pgbase_meta.admins
					(id, email, password_hash, active, created_at, updated_at)
					values ($1, 'drift-test@example.com', 'test', true,
					transaction_timestamp(), transaction_timestamp())`, administratorID); err != nil {
					t.Fatal(err)
				}
				applyService = NewService(pool, &Cache{}, nil)
				change := NewCreateChange(CreateCollectionInput{Name: "drift_guard_collection"})
				plan, err := applyService.Preview(ctx, change)
				if err != nil {
					t.Fatal(err)
				}
				applyIntent = LegacyApply(change, plan, administratorID)
			}
			if _, err := pool.Exec(context.Background(), test.breakCatalog); err != nil {
				t.Fatal(err)
			}
			if test.verifyApply {
				_, applyErr := applyService.Apply(ctx, applyIntent)
				catalog, catalogErr := NewService(pool, &Cache{}, nil).Catalog(ctx)
				if catalogErr != nil {
					t.Fatal(catalogErr)
				}
				if catalog.Version != 0 || len(catalog.Collections) != 0 {
					t.Fatalf("apply error = %v; catalog version = %d, collections = %d; want 0, 0",
						applyErr, catalog.Version, len(catalog.Collections))
				}
				if applyErr == nil {
					t.Fatal("schema apply succeeded with a damaged schema-writer catalog")
				}
			}

			recorder := &schemaTelemetryRecorder{}
			service := NewService(pool, &Cache{}, recorder)
			listener := NewListener(pool, service.cache, recorder, service)
			listener.retryMin, listener.retryMax = 10*time.Millisecond, 10*time.Millisecond
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() { done <- listener.Run(ctx) }()
			t.Cleanup(func() {
				cancel()
				if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
					t.Errorf("listener run: %v", err)
				}
			})

			deadline := time.Now().Add(time.Second)
			for time.Now().Before(deadline) {
				if listener.Ready(context.Background()) == nil {
					t.Fatal("listener became ready with a damaged schema-writer catalog")
				}
				for _, event := range recorder.snapshot() {
					if event.Operation() == observability.OperationListenerReload &&
						event.Outcome() != observability.OutcomeSuccess {
						return
					}
				}
				time.Sleep(10 * time.Millisecond)
			}
			t.Fatal("listener did not report the schema-writer catalog drift")
		})
	}
}

func TestIdleSchemaWriterLeaseStaysLiveWithoutNotificationsOrApplies(t *testing.T) {
	pool, _, _ := newSchemaServiceTest(t)
	cache := &Cache{}
	service := NewService(pool, cache, nil)
	listener := NewListener(pool, cache, nil, service)
	listener.writerHeartbeatInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	waitForListenerReady(t, listener)

	leases, err := service.SchemaWriterFleet(context.Background())
	if err != nil || len(leases) != 1 {
		t.Fatalf("initial idle writer fleet = %#v, %v", leases, err)
	}
	initialExpiry := leases[0].ExpiresAt
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		leases, err = service.SchemaWriterFleet(context.Background())
		if err == nil && len(leases) == 1 && leases[0].ExpiresAt.After(initialExpiry) {
			cancel()
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Fatalf("listener run: %v", err)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("idle schema writer lease was not renewed: %#v, %v", leases, err)
}

func TestListenerHeartbeatFailsReadinessAtANewerGenerationFenceAndReconnects(t *testing.T) {
	pool, _, _ := newSchemaServiceTest(t)
	service := NewService(pool, &Cache{}, nil)
	listener := NewListener(pool, service.cache, nil, service)
	listener.writerHeartbeatInterval = 20 * time.Millisecond
	listener.retryMin, listener.retryMax = 10*time.Millisecond, 10*time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("listener run: %v", err)
		}
	})
	waitForListenerReady(t, listener)

	if _, err := pool.Exec(context.Background(), `update pgbase_meta.schema_companion_index_capability set
		state = 'ready_v3', generation = 1,
		catalog_version = (select version from pgbase_meta.schema_state where singleton),
		building_at = transaction_timestamp(), ready_at = transaction_timestamp(),
		ready_index_count = 0, proven_compiler_version = $1,
		updated_at = transaction_timestamp() where singleton`, schemaCompilerVersion+1); err != nil {
		t.Fatal(err)
	}
	waitForListenerUnavailable(t, listener)
	if _, err := service.RollbackCompanionIndexes(context.Background()); err != nil {
		t.Fatal(err)
	}
	waitForListenerReady(t, listener)
}

func TestSchemaApplyAndItsOwnListenerHeartbeatCannotDeadlock(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	pool, service, administratorID, collection := readyCompanionFixture(t)
	recorder := &schemaTelemetryRecorder{}
	listener := NewListener(pool, service.cache, recorder, service)
	listener.writerHeartbeatInterval = 50 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("listener run: %v", err)
		}
	})
	waitForListenerReady(t, listener)
	var deadlocksBefore int64
	if err := pool.QueryRow(context.Background(), `select deadlocks from pg_stat_database
		where datname = current_database()`).Scan(&deadlocksBefore); err != nil {
		t.Fatal(err)
	}

	change := NewUpdateChange(UpdateCollectionInput{Name: collection.Name, Fields: []FieldInput{
		{Name: "title", Type: TypeText, Required: true, Indexed: true},
		{Name: "nickname", Type: TypeText, Indexed: true},
		{Name: "handle", Type: TypeText, Indexed: true},
	}})
	plan, err := service.Preview(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	enteredDDL, allowDDL := make(chan struct{}), make(chan struct{})
	service.beforeDDL = func(ctx context.Context, _ pgx.Tx) error {
		close(enteredDDL)
		select {
		case <-allowDDL:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	applyCtx, cancelApply := context.WithCancel(context.Background())
	t.Cleanup(cancelApply)
	applyDone := make(chan error, 1)
	go func() {
		_, err := service.Apply(applyCtx, LegacyApply(change, plan, administratorID))
		applyDone <- err
	}()
	select {
	case <-enteredDDL:
	case <-time.After(5 * time.Second):
		t.Fatal("schema apply did not reach the post-lease DDL barrier")
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		if err := pool.QueryRow(context.Background(), `select coalesce(wait_event_type = 'Lock', false)
			from pg_stat_activity where pid = $1`, listener.pid.Load()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("listener heartbeat did not reach the apply-held lock")
		}
		time.Sleep(10 * time.Millisecond)
	}
	close(allowDDL)
	if err := <-applyDone; err != nil {
		t.Fatalf("apply raced with its listener heartbeat: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	for _, event := range recorder.snapshot() {
		if event.Operation() == observability.OperationListenerReload &&
			event.Outcome() == observability.OutcomeFailure {
			t.Fatal("listener heartbeat was selected as a deadlock victim")
		}
	}
	var deadlocksAfter int64
	if err := pool.QueryRow(context.Background(), `select deadlocks from pg_stat_database
		where datname = current_database()`).Scan(&deadlocksAfter); err != nil {
		t.Fatal(err)
	}
	if deadlocksAfter != deadlocksBefore {
		t.Fatalf("apply/listener deadlocks = %d, want %d", deadlocksAfter, deadlocksBefore)
	}
}

func TestLongSchemaApplyDoesNotBlockAnotherWriterHeartbeat(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	pool, writer, administratorID, collection := readyCompanionFixture(t)
	other := NewService(pool, &Cache{}, nil)
	listener := NewListener(pool, other.cache, nil, other)
	listener.writerHeartbeatInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- listener.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("listener run: %v", err)
		}
	})
	waitForListenerReady(t, listener)
	otherID, err := other.instance()
	if err != nil {
		t.Fatal(err)
	}
	var before time.Time
	if err := pool.QueryRow(context.Background(), `select heartbeat_at
		from pgbase_meta.schema_writer_leases where instance_id = $1`, otherID).Scan(&before); err != nil {
		t.Fatal(err)
	}

	change := NewUpdateChange(UpdateCollectionInput{Name: collection.Name, Fields: []FieldInput{
		{Name: "title", Type: TypeText, Required: true, Indexed: true},
		{Name: "nickname", Type: TypeText, Indexed: true},
		{Name: "handle", Type: TypeText, Indexed: true},
	}})
	plan, err := writer.Preview(context.Background(), change)
	if err != nil {
		t.Fatal(err)
	}
	enteredDDL, allowDDL := make(chan struct{}), make(chan struct{})
	released := false
	t.Cleanup(func() {
		if !released {
			close(allowDDL)
		}
	})
	writer.beforeDDL = func(ctx context.Context, _ pgx.Tx) error {
		close(enteredDDL)
		select {
		case <-allowDDL:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	applyCtx, cancelApply := context.WithCancel(context.Background())
	t.Cleanup(cancelApply)
	applyDone := make(chan error, 1)
	go func() {
		_, err := writer.Apply(applyCtx, LegacyApply(change, plan, administratorID))
		applyDone <- err
	}()
	select {
	case <-enteredDDL:
	case <-time.After(5 * time.Second):
		t.Fatal("schema apply did not reach its long-DDL barrier")
	}

	deadline := time.Now().Add(time.Second)
	for {
		var heartbeat time.Time
		err := pool.QueryRow(context.Background(), `select heartbeat_at
			from pgbase_meta.schema_writer_leases where instance_id = $1`, otherID).Scan(&heartbeat)
		if err == nil && heartbeat.After(before) && listener.Ready(context.Background()) == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("other writer heartbeat did not advance during schema DDL: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}
	released = true
	close(allowDDL)
	if err := <-applyDone; err != nil {
		t.Fatalf("long schema apply: %v", err)
	}
}

func TestCompanionActivationRefusesIdleMixedCompilerListeners(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	pool, _, _ := newSchemaServiceTest(t)
	feature := NewService(pool, &Cache{}, nil)
	featureListener := NewListener(pool, feature.cache, nil, feature)
	older := NewService(pool, &Cache{}, nil)
	olderListener := NewListener(pool, older.cache, nil, older)
	older.compilerVersion = 2
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 2)
	for _, listener := range []*Listener{featureListener, olderListener} {
		go func() { done <- listener.Run(ctx) }()
	}
	t.Cleanup(func() {
		cancel()
		for range 2 {
			if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("listener run: %v", err)
			}
		}
	})
	waitForListenerReady(t, featureListener)
	waitForListenerReady(t, olderListener)

	leases, err := feature.SchemaWriterFleet(context.Background())
	if err != nil || len(leases) != 2 {
		t.Fatalf("idle mixed writer fleet = %#v, %v, want two leases", leases, err)
	}
	versions := map[int16]int{}
	for _, lease := range leases {
		versions[lease.CompilerVersion]++
	}
	if versions[2] != 1 || versions[3] != 1 {
		t.Fatalf("idle mixed writer generations = %v, want one each of 2 and 3", versions)
	}
	proof := SchemaWriterFleetProof{Instances: []uuid.UUID{leases[0].InstanceID, leases[1].InstanceID}}
	if _, err := feature.ActivateCompanionIndexes(context.Background(), proof); !errors.Is(err, ErrCompanionIndexFleet) {
		t.Fatalf("activation over idle generation-2 and generation-3 listeners = %v, want ErrCompanionIndexFleet", err)
	}
}

func TestActivatedCompanionGenerationKeepsAnOlderListenerUnready(t *testing.T) {
	if !companionFeatureArtifact(t) {
		return
	}
	pool, _, _ := newSchemaServiceTest(t)
	feature := NewService(pool, &Cache{}, nil)
	featureListener := NewListener(pool, feature.cache, nil, feature)
	featureListener.writerHeartbeatInterval = 20 * time.Millisecond
	featureCtx, cancelFeature := context.WithCancel(context.Background())
	featureDone := make(chan error, 1)
	go func() { featureDone <- featureListener.Run(featureCtx) }()
	t.Cleanup(func() {
		cancelFeature()
		if err := <-featureDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("feature listener run: %v", err)
		}
	})
	waitForListenerReady(t, featureListener)
	leases, err := feature.SchemaWriterFleet(context.Background())
	if err != nil || len(leases) != 1 {
		t.Fatalf("feature writer fleet = %#v, %v", leases, err)
	}
	featureExpiry := leases[0].ExpiresAt
	if _, err := feature.ActivateCompanionIndexes(context.Background(), SchemaWriterFleetProof{
		Instances: []uuid.UUID{leases[0].InstanceID},
	}); err != nil {
		t.Fatal(err)
	}

	recorder := &schemaTelemetryRecorder{}
	older := NewService(pool, &Cache{}, recorder)
	olderListener := NewListener(pool, older.cache, recorder, older)
	older.compilerVersion = 2
	olderListener.retryMin, olderListener.retryMax = 10*time.Millisecond, 10*time.Millisecond
	olderCtx, cancelOlder := context.WithCancel(context.Background())
	olderDone := make(chan error, 1)
	go func() { olderDone <- olderListener.Run(olderCtx) }()
	t.Cleanup(func() {
		cancelOlder()
		if err := <-olderDone; err != nil && !errors.Is(err, context.Canceled) {
			t.Errorf("older listener run: %v", err)
		}
	})
	deadline := time.Now().Add(time.Second)
	fenced := false
	for time.Now().Before(deadline) && !fenced {
		for _, event := range recorder.snapshot() {
			if event.Operation() == observability.OperationListenerReload &&
				event.Outcome() == observability.OutcomeFailure {
				if err := olderListener.Ready(context.Background()); err == nil {
					t.Fatal("generation-2 listener became ready after generation 3 activation")
				}
				fenced = true
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !fenced {
		t.Fatal("generation-2 listener never attempted the activated generation fence")
	}
	deadline = time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		leases, err = feature.SchemaWriterFleet(context.Background())
		if err == nil && featureListener.Ready(context.Background()) == nil && len(leases) == 1 &&
			leases[0].CompilerVersion == schemaCompilerVersion && leases[0].ExpiresAt.After(featureExpiry) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("feature heartbeat after activation = %#v, %v", leases, err)
}
