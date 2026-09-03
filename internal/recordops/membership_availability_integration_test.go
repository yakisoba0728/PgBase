//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/internal/membership"
)

func TestReleaseBTraversalControlFailureIsStablyUnavailable(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `delete from pgbase_meta.record_membership_capability where singleton`); err != nil {
		t.Fatal(err)
	}
	_, err := records.List(ctx, ListRequest{
		Principal: principal, RequestID: "availability-traversal-list", Collection: "records", Limit: 1,
	})
	assertStableMembershipUnavailable(t, err)
}

func TestReleaseACutoverMakesEveryMutationStablyUnavailable(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
	if err != nil {
		t.Fatal(err)
	}
	records := NewService(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	created, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "availability-cutover-fixture", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"before-cutover"`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `with timestamp as (select clock_timestamp() as value)
		update pgbase_meta.record_membership_capability set
			state = 'cutover', generation = 1, minimum_writer_version = 1,
			cutover_at = timestamp.value, updated_at = timestamp.value
		from timestamp where singleton`); err != nil {
		t.Fatal(err)
	}

	recordID := recordResultID(t, created)
	mutations := []struct {
		name string
		run  func() error
	}{
		{name: "create fence", run: func() error {
			_, err := records.Create(ctx, CreateRequest{
				Principal: principal, RequestID: "availability-cutover-create", Collection: "records",
				Input: map[string]json.RawMessage{"title": json.RawMessage(`"blocked-create"`)},
			})
			return err
		}},
		{name: "update fence", run: func() error {
			_, err := records.Update(ctx, UpdateRequest{
				Principal: principal, RequestID: "availability-cutover-update", Collection: "records", ID: recordID,
				Input: map[string]json.RawMessage{"title": json.RawMessage(`"blocked-update"`)},
			})
			return err
		}},
		{name: "delete fence", run: func() error {
			_, err := records.Delete(ctx, DeleteRequest{
				Principal: principal, RequestID: "availability-cutover-delete", Collection: "records", ID: recordID,
			})
			return err
		}},
	}
	for _, mutation := range mutations {
		t.Run(mutation.name, func(t *testing.T) {
			assertStableMembershipUnavailable(t, mutation.run())
		})
	}
	var base, blocked int
	if err := pool.QueryRow(ctx, `select count(*), count(*) filter (where title like 'blocked-%')
		from app.records`).Scan(&base, &blocked); err != nil {
		t.Fatal(err)
	}
	if base != 1 || blocked != 0 {
		t.Fatalf("cutover base/blocked rows = %d/%d, want 1/0", base, blocked)
	}
}

func TestReleaseARevokedAndExpiredLeasesAreStablyUnavailable(t *testing.T) {
	for _, mode := range []string{"revoked", "expired"} {
		t.Run(mode, func(t *testing.T) {
			pool, schemas, _, principal := newRecordOperationsTest(t)
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
			if err != nil {
				t.Fatal(err)
			}
			records := NewService(
				pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
			)
			created, err := records.Create(ctx, CreateRequest{
				Principal: principal, RequestID: "availability-lease-fixture-" + mode, Collection: "records",
				Input: map[string]json.RawMessage{"title": json.RawMessage(`"before-lease-loss"`)},
			})
			if err != nil {
				t.Fatal(err)
			}
			instanceID := runtime.FleetMember().InstanceID
			switch mode {
			case "revoked":
				if _, err := pool.Exec(ctx, `delete from pgbase_meta.runtime_writer_leases
					where instance_id = $1`, instanceID); err != nil {
					t.Fatal(err)
				}
			case "expired":
				if _, err := pool.Exec(ctx, `update pgbase_meta.runtime_writer_leases set
					started_at = clock_timestamp() - interval '3 minutes',
					heartbeat_at = clock_timestamp() - interval '2 minutes',
					lease_expires_at = clock_timestamp() - interval '1 minute'
					where instance_id = $1`, instanceID); err != nil {
					t.Fatal(err)
				}
			default:
				t.Fatalf("unknown lease mode %q", mode)
			}

			recordID := recordResultID(t, created)
			for _, mutation := range []struct {
				name string
				run  func() error
			}{
				{name: "create", run: func() error {
					_, err := records.Create(ctx, CreateRequest{
						Principal: principal, RequestID: "availability-lease-create-" + mode, Collection: "records",
						Input: map[string]json.RawMessage{"title": json.RawMessage(`"blocked-create"`)},
					})
					return err
				}},
				{name: "update", run: func() error {
					_, err := records.Update(ctx, UpdateRequest{
						Principal: principal, RequestID: "availability-lease-update-" + mode,
						Collection: "records", ID: recordID,
						Input: map[string]json.RawMessage{"title": json.RawMessage(`"blocked-update"`)},
					})
					return err
				}},
				{name: "delete", run: func() error {
					_, err := records.Delete(ctx, DeleteRequest{
						Principal: principal, RequestID: "availability-lease-delete-" + mode,
						Collection: "records", ID: recordID,
					})
					return err
				}},
			} {
				t.Run(mutation.name, func(t *testing.T) {
					assertStableMembershipUnavailable(t, mutation.run())
				})
			}
			var count int
			if err := pool.QueryRow(ctx, `select count(*) from app.records`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 1 {
				t.Fatalf("lease loss left %d records, want unchanged 1", count)
			}
		})
	}
}

func TestReleaseAReserveAndSidecarFailuresAreStablyUnavailable(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterA)
	if err != nil {
		t.Fatal(err)
	}
	records := NewService(
		pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime,
	)
	if _, err := membership.New(pool).Cutover(ctx, membership.FleetProof{
		Members: []membership.FleetMember{runtime.FleetMember()},
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `create function pgbase_meta.fail_release_a_reserve()
		returns trigger language plpgsql as $$ begin
			raise exception 'secret reserve failure';
		end $$;
		create trigger fail_release_a_reserve before update of last_ordinal
		on pgbase_meta.collection_creation_counters
		for each row execute function pgbase_meta.fail_release_a_reserve()`); err != nil {
		t.Fatal(err)
	}
	request := CreateRequest{
		Principal: principal, RequestID: "availability-reserve", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"membership-failure"`)},
	}
	_, err = records.Create(ctx, request)
	assertStableMembershipUnavailable(t, err)
	if _, err := pool.Exec(ctx, `drop trigger fail_release_a_reserve
		on pgbase_meta.collection_creation_counters;
		drop function pgbase_meta.fail_release_a_reserve()`); err != nil {
		t.Fatal(err)
	}

	if _, err := pool.Exec(ctx, `create function pgbase_meta.fail_release_a_sidecar_insert()
		returns trigger language plpgsql as $$ begin
			raise exception 'secret sidecar insert failure';
		end $$;
		create trigger fail_release_a_sidecar_insert before insert
		on pgbase_meta.record_creation_ordinals
		for each row execute function pgbase_meta.fail_release_a_sidecar_insert()`); err != nil {
		t.Fatal(err)
	}
	request.RequestID = "availability-sidecar-insert"
	_, err = records.Create(ctx, request)
	assertStableMembershipUnavailable(t, err)
	if _, err := pool.Exec(ctx, `drop trigger fail_release_a_sidecar_insert
		on pgbase_meta.record_creation_ordinals;
		drop function pgbase_meta.fail_release_a_sidecar_insert()`); err != nil {
		t.Fatal(err)
	}

	request.RequestID = "availability-sidecar-fixture"
	created, err := records.Create(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `create function pgbase_meta.fail_release_a_sidecar_delete()
		returns trigger language plpgsql as $$ begin
			raise exception 'secret sidecar delete failure';
		end $$;
		create trigger fail_release_a_sidecar_delete before delete
		on pgbase_meta.record_creation_ordinals
		for each row execute function pgbase_meta.fail_release_a_sidecar_delete()`); err != nil {
		t.Fatal(err)
	}
	_, err = records.Delete(ctx, DeleteRequest{
		Principal: principal, RequestID: "availability-sidecar-delete", Collection: "records",
		ID: recordResultID(t, created),
	})
	assertStableMembershipUnavailable(t, err)

	var base, sidecars int
	if err := pool.QueryRow(ctx, `select count(*) from app.records`).Scan(&base); err != nil {
		t.Fatal(err)
	}
	if err := pool.QueryRow(ctx, `select count(*) from pgbase_meta.record_creation_ordinals`).Scan(&sidecars); err != nil {
		t.Fatal(err)
	}
	if base != 1 || sidecars != 1 {
		t.Fatalf("failed membership writes left base/sidecars = %d/%d, want 1/1", base, sidecars)
	}
}

func assertStableMembershipUnavailable(t *testing.T, err error) {
	t.Helper()
	if !IsCode(err, CodeUnavailable) || err == nil || err.Error() != string(CodeUnavailable) {
		t.Fatalf("membership failure = %#v, want stable availability", err)
	}
	for _, sentinel := range []error{
		membership.ErrUnavailable,
		membership.ErrInvalidState,
		membership.ErrWriterTooOld,
		membership.ErrMembershipDrift,
	} {
		if errors.Is(err, sentinel) {
			t.Fatalf("availability error leaked membership sentinel %v", sentinel)
		}
	}
}

// A service built by the direct constructor registers its own runtime on the
// first write. That lazily obtained lease has to carry the same release
// generation the application registers, because the durable lease set is what
// every rollout proof reads: a service that under-declares itself would let a
// transition be proved against a fleet it does not describe.
func TestDirectConstructorRegistersTheSameWriterReleaseAsTheApplication(t *testing.T) {
	pool, schemas, _, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	records := NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, nil)
	if _, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "direct-constructor-writer-release", Collection: "records",
		Input: map[string]json.RawMessage{"title": json.RawMessage(`"lazy-runtime"`)},
	}); err != nil {
		t.Fatal(err)
	}
	var versions []int16
	rows, err := pool.Query(ctx, `select writer_version from pgbase_meta.runtime_writer_leases
		where lease_expires_at > clock_timestamp() order by writer_version`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var version int16
		if err := rows.Scan(&version); err != nil {
			t.Fatal(err)
		}
		versions = append(versions, version)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || membership.WriterVersion(versions[0]) != membership.WriterC {
		t.Fatalf("active writer leases = %v, want exactly one at Release C", versions)
	}
}
