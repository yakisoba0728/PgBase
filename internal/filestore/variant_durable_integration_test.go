//go:build integration

package filestore

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/yakisoba0728/PgBase/internal/database"
)

type variantFaultStore struct {
	ObjectStore
	put    func(context.Context, string, io.Reader) error
	remove func(context.Context, string) error
}

func (s *variantFaultStore) Put(ctx context.Context, key string, body io.Reader) error {
	if s.put != nil {
		return s.put(ctx, key, body)
	}
	return s.ObjectStore.Put(ctx, key, body)
}

func (s *variantFaultStore) Delete(ctx context.Context, key string) error {
	if s.remove != nil {
		return s.remove(ctx, key)
	}
	return s.ObjectStore.Delete(ctx, key)
}

func variantFixtureURL(t *testing.T, pool *database.Pool) string {
	t.Helper()
	conn, err := pool.Acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	return conn.Conn().Config().ConnString()
}

func requireVariantBytes(t *testing.T, store ObjectStore, key string, present bool) {
	t.Helper()
	r, err := store.Open(t.Context(), key)
	if r != nil {
		_ = r.Close()
	}
	if present && err != nil || !present && !errors.Is(err, ErrNotFound) {
		t.Fatalf("object %q present=%t error=%v", key, present, err)
	}
}

func TestVariantDurablePartialPutAndConcurrentCleanup(t *testing.T) {
	pool, service, file, _, original := newClosureFile(t, "attachment")
	putFailure := errors.New("storage reply lost after write")
	var key string
	service.objects = &variantFaultStore{ObjectStore: original, put: func(ctx context.Context, candidate string, body io.Reader) error {
		key = candidate
		var retained bool
		if err := pool.QueryRow(ctx, `select retain_on_success from pgbase_meta.file_object_deletions where object_key=$1`, key).Scan(&retained); err != nil || !retained {
			return fmt.Errorf("Put started before durable uncertainty intent: retained=%t err=%v", retained, err)
		}
		if err := original.Put(ctx, key, body); err != nil {
			return err
		}
		return putFailure
	}}
	if _, err := service.variantObject(t.Context(), file, "2x2"); !errors.Is(err, putFailure) {
		t.Fatalf("failed Put outcome=%v", err)
	}
	var refs int
	if err := pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.record_file_variants where object_key=$1`, key).Scan(&refs); err != nil || refs != 0 {
		t.Fatalf("failed Put published metadata: refs=%d err=%v", refs, err)
	}
	requireVariantBytes(t, original, key, true)
	var deletes atomic.Int32
	service.objects = &variantFaultStore{ObjectStore: original, remove: func(ctx context.Context, candidate string) error {
		deletes.Add(1)
		return original.Delete(ctx, candidate)
	}}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := service.DrainOne(t.Context()); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	if deletes.Load() != 1 {
		t.Fatalf("concurrent cleaners deleted %d times", deletes.Load())
	}
	requireVariantBytes(t, original, key, false)
	var retained bool
	var attempts int
	var scheduled bool
	if err := pool.QueryRow(t.Context(), `select retain_on_success,attempts,next_attempt_at>clock_timestamp() from pgbase_meta.file_object_deletions where object_key=$1`, key).Scan(&retained, &attempts, &scheduled); err != nil || !retained || attempts != 1 || !scheduled {
		t.Fatalf("uncertain write lost retry state: retained=%t attempts=%d scheduled=%t err=%v", retained, attempts, scheduled, err)
	}
	// A late remote completion after the first successful Delete is still
	// covered by the same intent and is physically deleted on the next pass.
	if err := original.Put(t.Context(), key, strings.NewReader("late bytes")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `update pgbase_meta.file_object_deletions set next_attempt_at=clock_timestamp() where object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.DrainOne(t.Context()); err != nil || !worked {
		t.Fatalf("retry worked=%t err=%v", worked, err)
	}
	requireVariantBytes(t, original, key, false)
	// Retained future work must not starve a normal deletion behind it.
	normal := "thumbnails/" + uuid.NewString()
	if err := original.Put(t.Context(), normal, strings.NewReader("ordinary cleanup")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `insert into pgbase_meta.file_object_deletions(object_key,queued_at) values($1,current_timestamp)`, normal); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.DrainOne(t.Context()); err != nil || !worked {
		t.Fatalf("normal cleanup starved: worked=%t err=%v", worked, err)
	}
	requireVariantBytes(t, original, normal, false)
	if _, err := pool.Exec(t.Context(), `insert into pgbase_meta.file_object_deletions(object_key,queued_at,retain_on_success) values($1,current_timestamp,true)`, file.ObjectKey); err != nil {
		t.Fatal(err)
	}
	before := deletes.Load()
	if worked, err := service.DrainOne(t.Context()); err != nil || !worked || deletes.Load() != before {
		t.Fatalf("live object reached deletion: worked=%t before=%d after=%d err=%v", worked, before, deletes.Load(), err)
	}
	requireVariantBytes(t, original, file.ObjectKey, true)
}

func TestVariantCleanupRecordsDeletionFailureAndRetries(t *testing.T) {
	pool, service, _, _, original := newClosureFile(t, "attachment")
	key := "thumbnails/" + uuid.NewString()
	if err := original.Put(t.Context(), key, strings.NewReader("delete retry")); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(t.Context(), `insert into pgbase_meta.file_object_deletions(object_key,queued_at) values($1,current_timestamp)`, key); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("injected delete failure")
	service.objects = &variantFaultStore{ObjectStore: original, remove: func(context.Context, string) error { return failure }}
	if worked, err := service.DrainOne(t.Context()); !worked || !errors.Is(err, failure) {
		t.Fatalf("failed deletion worked=%t err=%v", worked, err)
	}
	var attempts int
	var message string
	var dated bool
	if err := pool.QueryRow(t.Context(), `select attempts,last_error,last_attempt_at is not null and next_attempt_at>last_attempt_at from pgbase_meta.file_object_deletions where object_key=$1`, key).Scan(&attempts, &message, &dated); err != nil || attempts != 1 || message != "object deletion failed" || !dated {
		t.Fatalf("failed deletion rolled back evidence: attempts=%d error=%q dated=%t err=%v", attempts, message, dated, err)
	}
	service.objects = original
	if _, err := pool.Exec(t.Context(), `update pgbase_meta.file_object_deletions set next_attempt_at=clock_timestamp() where object_key=$1`, key); err != nil {
		t.Fatal(err)
	}
	if worked, err := service.DrainOne(t.Context()); err != nil || !worked {
		t.Fatalf("retry worked=%t err=%v", worked, err)
	}
	if worked, err := service.DrainOne(t.Context()); err != nil || worked {
		t.Fatalf("idempotent drain worked=%t err=%v", worked, err)
	}
	requireVariantBytes(t, original, key, false)
}

func TestVariantDurablePoolFourAndExhaustion(t *testing.T) {
	pool, service, file, _, original := newClosureFile(t, "attachment")
	u, err := url.Parse(variantFixtureURL(t, pool))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	q.Set("pool_max_conns", "4")
	u.RawQuery = q.Encode()
	bounded, err := database.Open(t.Context(), u.String(), time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bounded.Close)
	service.pool = bounded
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
	defer cancel()
	errs := make(chan error, 4)
	for size := 2; size <= 5; size++ {
		go func() { _, err := service.variantObject(ctx, file, fmt.Sprintf("%dx%d", size, size)); errs <- err }()
	}
	for range 4 {
		if err := <-errs; err != nil {
			t.Fatalf("four creators exhausted their own pool: %v", err)
		}
	}
	if _, err := service.variantObject(ctx, file, "2x2"); err != nil {
		t.Fatalf("cache hit failed: %v", err)
	}
	var queued int
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.file_object_deletions`).Scan(&queued); err != nil || queued != 0 {
		t.Fatalf("completed/cache-hit intents=%d err=%v", queued, err)
	}
	var held []*pgxpool.Conn
	defer func() {
		for _, conn := range held {
			conn.Release()
		}
	}()
	for range 4 {
		conn, err := bounded.Acquire(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	exhausted, stop := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer stop()
	if _, err := service.variantObject(exhausted, file, "6x6"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exhausted reservation ignored deadline: %v", err)
	}
	if err := pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.record_file_variants`).Scan(&queued); err != nil || queued != 4 {
		t.Fatalf("exhausted reservation published a fifth variant: count=%d err=%v", queued, err)
	}
	requireVariantBytes(t, original, file.ObjectKey, true)
}

func TestVariantDurableDelayedCommitAndRollback(t *testing.T) {
	for _, rollback := range []bool{false, true} {
		t.Run(fmt.Sprintf("rollback=%t", rollback), func(t *testing.T) {
			pool, service, file, _, original := newClosureFile(t, "attachment")
			blocker, err := pool.OpenCoordinationSession(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = blocker.Close(context.Background()) }()
			const gate int64 = 691300024
			if _, err := blocker.Exec(t.Context(), `select pg_advisory_lock($1)`, gate); err != nil {
				t.Fatal(err)
			}
			outcome := "RETURN NEW;"
			if rollback {
				outcome = "RAISE EXCEPTION 'injected delayed rollback';"
			}
			_, err = pool.Exec(t.Context(), fmt.Sprintf(`create function pgbase_meta.variant_commit_fixture() returns trigger language plpgsql as $$ begin perform pg_advisory_xact_lock(%d); %s end; $$; create constraint trigger variant_commit_fixture after insert on pgbase_meta.record_file_variants deferrable initially deferred for each row execute function pgbase_meta.variant_commit_fixture()`, gate, outcome))
			if err != nil {
				t.Fatal(err)
			}
			keys := make(chan string, 1)
			service.objects = &variantFaultStore{ObjectStore: original, put: func(ctx context.Context, key string, body io.Reader) error {
				if err := original.Put(ctx, key, body); err != nil {
					return err
				}
				keys <- key
				return nil
			}}
			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() { _, err := service.variantObject(ctx, file, "2x2"); done <- err }()
			var key string
			select {
			case key = <-keys:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			waitClosureLock(t, pool, "commit")
			var owner int32
			if err := pool.QueryRow(ctx, `select pid from pg_locks where locktype='advisory' and classid=((hashtextextended($1,0)>>32)&4294967295)::oid and objid=(hashtextextended($1,0)&4294967295)::oid and objsubid=1 and granted and database=(select oid from pg_database where datname=current_database())`, stagedObjectLockName(key)).Scan(&owner); err != nil {
				t.Fatal(err)
			}
			if _, err := pool.Exec(ctx, `select pg_terminate_backend($1,5000)`, owner); err != nil {
				t.Fatal(err)
			}
			// The staging session is gone, but COMMIT's reference lock must
			// protect the bytes and the old visible intent throughout the delay.
			if worked, err := service.DrainOne(ctx); err != nil || worked {
				t.Fatalf("in-flight publication drained: worked=%t err=%v", worked, err)
			}
			requireVariantBytes(t, original, key, true)
			time.Sleep(1100 * time.Millisecond)
			if _, err := blocker.Exec(ctx, `select pg_advisory_unlock($1)`, gate); err != nil {
				t.Fatal(err)
			}
			err = <-done
			if rollback != (err != nil) {
				t.Fatalf("rollback=%t outcome=%v", rollback, err)
			}
			if worked, err := service.DrainOne(ctx); err != nil || worked != rollback {
				t.Fatalf("settled cleanup worked=%t rollback=%t err=%v", worked, rollback, err)
			}
			requireVariantBytes(t, original, key, !rollback)
		})
	}
}

type variantCrashSpec struct {
	URL, Directory, Marker string
	File                   File
}

func TestVariantDurableCrashRestart(t *testing.T) {
	if raw := os.Getenv("PGBASE_VARIANT_CRASH_FIXTURE"); raw != "" {
		var spec variantCrashSpec
		if err := json.Unmarshal([]byte(raw), &spec); err != nil {
			t.Fatal(err)
		}
		pool, err := database.Open(t.Context(), spec.URL, time.Second)
		if err != nil {
			t.Fatal(err)
		}
		local, err := NewLocalStore(spec.Directory)
		if err != nil {
			t.Fatal(err)
		}
		store := &variantFaultStore{ObjectStore: local, put: func(ctx context.Context, key string, body io.Reader) error {
			if err := local.Put(ctx, key, body); err != nil {
				return err
			}
			if err := os.WriteFile(spec.Marker, []byte(key), 0o600); err != nil {
				return err
			}
			<-ctx.Done()
			return ctx.Err()
		}}
		service := &Service{pool: pool, objects: store, options: Options{MaximumBytes: 1 << 20, MaximumImagePixels: 1_000_000, Now: time.Now}}
		_, err = service.variantObject(t.Context(), spec.File, "2x2")
		t.Fatalf("crash fixture returned before SIGKILL: %v", err)
	}
	pool, _, file, _, original := newClosureFile(t, "attachment")
	spec := variantCrashSpec{URL: variantFixtureURL(t, pool), Directory: original.ObjectStore.(*LocalStore).root, Marker: filepath.Join(t.TempDir(), "put-completed"), File: file}
	raw, err := json.Marshal(spec)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestVariantDurableCrashRestart$", "-test.timeout=15s")
	command.Env = append(os.Environ(), "PGBASE_VARIANT_CRASH_FIXTURE="+string(raw))
	var output bytes.Buffer
	command.Stdout, command.Stderr = &output, &output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	t.Cleanup(func() { _ = command.Process.Kill() })
	var key string
	deadline := time.NewTimer(6 * time.Second)
	defer deadline.Stop()
	for key == "" {
		if data, err := os.ReadFile(spec.Marker); err == nil {
			key = string(data)
			break
		}
		select {
		case err := <-done:
			t.Fatalf("child exited before Put: %v %s", err, output.String())
		case <-deadline.C:
			_ = command.Process.Kill()
			<-done
			t.Fatalf("child never wrote object: %s", output.String())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := command.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err == nil {
		t.Fatal("crash child exited successfully")
	}
	restarted, err := database.Open(t.Context(), spec.URL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	local, err := NewLocalStore(spec.Directory)
	if err != nil {
		t.Fatal(err)
	}
	service := &Service{pool: restarted, objects: local}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	for {
		worked, err := service.DrainOne(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if worked {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("crashed sessions did not release GC locks")
		case <-time.After(5 * time.Millisecond):
		}
	}
	requireVariantBytes(t, local, key, false)
	requireVariantBytes(t, local, file.ObjectKey, true)
	var retained bool
	if err := pool.QueryRow(t.Context(), `select retain_on_success from pgbase_meta.file_object_deletions where object_key=$1`, key).Scan(&retained); err != nil || !retained {
		t.Fatalf("restart lost uncertainty state: retained=%t err=%v", retained, err)
	}
}

// The plaintext loopback fixture drops a real server COMMIT reply only after
// PostgreSQL sends CommandComplete. No production transaction code is mocked.
func commitReplyLossProxy(t *testing.T, sourceURL string) (string, *atomic.Bool) {
	t.Helper()
	u, err := url.Parse(sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	upstreamAddress := u.Host
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var dropped atomic.Bool
	var group sync.WaitGroup
	var mu sync.Mutex
	var wires []net.Conn
	group.Add(1)
	go func() {
		defer group.Done()
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			upstream, err := net.Dial("tcp", upstreamAddress)
			if err != nil {
				_ = client.Close()
				return
			}
			mu.Lock()
			wires = append(wires, client, upstream)
			mu.Unlock()
			group.Add(2)
			go func() { defer group.Done(); _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
			go func() {
				defer group.Done()
				defer client.Close()
				defer upstream.Close()
				for {
					header := make([]byte, 5)
					if _, err := io.ReadFull(upstream, header); err != nil {
						return
					}
					length := binary.BigEndian.Uint32(header[1:])
					if length < 4 || length > 1<<20 {
						return
					}
					body := make([]byte, int(length)-4)
					if _, err := io.ReadFull(upstream, body); err != nil {
						return
					}
					if header[0] == 'C' && string(body) == "COMMIT\x00" && dropped.CompareAndSwap(false, true) {
						return
					}
					if _, err := client.Write(append(header, body...)); err != nil {
						return
					}
				}
			}()
		}
	}()
	t.Cleanup(func() {
		_ = listener.Close()
		mu.Lock()
		for _, wire := range wires {
			_ = wire.Close()
		}
		mu.Unlock()
		group.Wait()
	})
	u.Host = listener.Addr().String()
	return u.String(), &dropped
}

func TestVariantDurableAmbiguousCommittedResult(t *testing.T) {
	pool, service, file, _, original := newClosureFile(t, "attachment")
	proxyURL, dropped := commitReplyLossProxy(t, variantFixtureURL(t, pool))
	proxied, err := database.Open(t.Context(), proxyURL, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer proxied.Close()
	service.pool = proxied
	key, err := service.variantObject(t.Context(), file, "2x2")
	if err == nil || !dropped.Load() {
		t.Fatalf("COMMIT response was not lost: dropped=%t err=%v", dropped.Load(), err)
	}
	var published string
	if err := pool.QueryRow(t.Context(), `select object_key from pgbase_meta.record_file_variants where file_id=$1 and variant='2x2'`, file.ID).Scan(&published); err != nil || published != key {
		t.Fatalf("actual commit was not durable: key=%q published=%q err=%v", key, published, err)
	}
	var queued bool
	if err := pool.QueryRow(t.Context(), `select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1)`, key).Scan(&queued); err != nil || queued {
		t.Fatalf("committed reference and intent were not atomic: queued=%t err=%v", queued, err)
	}
	service.pool = pool
	if worked, err := service.DrainOne(t.Context()); err != nil || worked {
		t.Fatalf("unknown committed result queued live bytes: worked=%t err=%v", worked, err)
	}
	requireVariantBytes(t, original, key, true)
}
