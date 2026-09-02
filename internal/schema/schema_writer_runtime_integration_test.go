//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/testutil"
	"github.com/webisnoteasy/PgBase/migrations/system"
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
		state = 'ready', generation = 1,
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
	if _, err := feature.ActivateCompanionIndexes(context.Background(), SchemaWriterFleetProof{
		Instances: []uuid.UUID{leases[0].InstanceID},
	}); err != nil {
		t.Fatal(err)
	}

	older := NewService(pool, &Cache{}, nil)
	olderListener := NewListener(pool, older.cache, nil, older)
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
	time.Sleep(100 * time.Millisecond)
	if err := olderListener.Ready(context.Background()); err == nil {
		t.Fatal("generation-2 listener became ready after generation 3 activation")
	}
	leases, err = feature.SchemaWriterFleet(context.Background())
	if err != nil || len(leases) != 1 || leases[0].CompilerVersion != schemaCompilerVersion {
		t.Fatalf("fleet after older listener start = %#v, %v", leases, err)
	}
}
