//go:build integration

package jobs

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

// Expiry permits a management operation, but does not stop the old handler.
// Neither its success nor its failure may replace an accepted operator edit.
func TestJobManagementFencesExpiredHandlerCompletion(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	for _, operation := range []struct {
		name string
		run  func(*Service, uuid.UUID, string) error
	}{
		{"configure", func(s *Service, id uuid.UUID, _ string) error {
			return s.ConfigureJob(ctx, id, "@every 2h", true)
		}},
		{"disable", func(s *Service, id uuid.UUID, _ string) error {
			return s.ConfigureJob(ctx, id, "@every 1h", false)
		}},
		{"schedule", func(s *Service, _ uuid.UUID, name string) error {
			return s.Schedule(ctx, name, "@every 2h")
		}},
		{"request_run", func(s *Service, id uuid.UUID, _ string) error {
			return s.RequestRun(ctx, id)
		}},
		{"unregister", func(s *Service, _ uuid.UUID, name string) error {
			return s.Unregister(ctx, name)
		}},
	} {
		for _, outcome := range []struct {
			name string
			err  error
		}{{"success", nil}, {"failure", errors.New("old handler failure")}} {
			t.Run(operation.name+"/"+outcome.name, func(t *testing.T) {
				registry := NewRegistry()
				name := t.Name()
				if err := registry.Register(name, func(context.Context, Job) error { return nil }); err != nil {
					t.Fatal(err)
				}
				insertJob(t, pool, name, "@every 1h")
				service := NewService(pool, registry, nil)
				claims, err := service.claim(ctx)
				if err != nil || len(claims) != 1 {
					t.Fatalf("claim = %v, %v", claims, err)
				}
				claim := claims[0]
				if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, claim.ID); err != nil {
					t.Fatal(err)
				}
				if err := operation.run(service, claim.ID, name); err != nil {
					t.Fatal(err)
				}
				read := func() []byte {
					t.Helper()
					var state []byte
					if err := pool.QueryRow(ctx, `SELECT to_jsonb(j) FROM pgbase_meta.scheduled_jobs j WHERE id=$1`, claim.ID).Scan(&state); err != nil {
						t.Fatal(err)
					}
					return state
				}
				before := read()
				if err := service.complete(ctx, claim, outcome.err); err != nil {
					t.Fatal(err)
				}
				if after := read(); !bytes.Equal(before, after) {
					t.Fatalf("expired completion replaced accepted management state:\nbefore: %s\nafter: %s", before, after)
				}
			})
		}
	}
}

func TestScheduleCannotReplaceAnActiveClaim(t *testing.T) {
	ctx := context.Background()
	pool := jobsTestPool(t)
	registry := NewRegistry()
	if err := registry.Register("running", func(context.Context, Job) error { return nil }); err != nil {
		t.Fatal(err)
	}
	insertJob(t, pool, "running", "@every 1h")
	service := NewService(pool, registry, nil)
	claims, err := service.claim(ctx)
	if err != nil || len(claims) != 1 {
		t.Fatalf("claim = %v, %v", claims, err)
	}
	if err := service.Schedule(ctx, "running", "@every 2h"); !errors.Is(err, ErrNotManageable) {
		t.Fatalf("Schedule during claim = %v, want ErrNotManageable", err)
	}
}

func TestExpiredCompletionWithoutManagementCannotRenewItsAuthority(t *testing.T) {
	ctx := t.Context()
	pool := jobsTestPool(t)
	for _, outcome := range []struct {
		name string
		err  error
	}{{"success", nil}, {"failure", errors.New("expired handler failure")}} {
		t.Run(outcome.name, func(t *testing.T) {
			registry := NewRegistry()
			if err := registry.Register(outcome.name, func(context.Context, Job) error { return nil }); err != nil {
				t.Fatal(err)
			}
			insertJob(t, pool, outcome.name, "@every 1h")
			service := NewService(pool, registry, nil)
			claims, err := service.claim(ctx)
			if err != nil || len(claims) != 1 {
				t.Fatalf("claim = %v, %v", claims, err)
			}
			if _, err := pool.Exec(ctx, `UPDATE pgbase_meta.scheduled_jobs SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, claims[0].ID); err != nil {
				t.Fatal(err)
			}
			read := func() []byte {
				t.Helper()
				var state []byte
				if err := pool.QueryRow(ctx, `SELECT to_jsonb(j) FROM pgbase_meta.scheduled_jobs j WHERE id=$1`, claims[0].ID).Scan(&state); err != nil {
					t.Fatal(err)
				}
				return state
			}
			before := read()
			if err := service.complete(ctx, claims[0], outcome.err); err != nil {
				t.Fatal(err)
			}
			if after := read(); !bytes.Equal(before, after) {
				t.Fatalf("expired completion mutated the unclaimed occurrence:\nbefore: %s\nafter: %s", before, after)
			}
			retry, err := service.claim(ctx)
			if err != nil || len(retry) != 1 || !retry[0].ScheduledAt.Equal(claims[0].ScheduledAt) || retry[0].FenceGeneration <= claims[0].FenceGeneration {
				t.Fatalf("reclaimed occurrence = %v, %v; original = %v", retry, err, claims)
			}
		})
	}
}
