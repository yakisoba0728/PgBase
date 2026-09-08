//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestBatchRollsBackEveryRecordArtifact(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	input := func(title string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}
	}
	_, err := records.Batch(context.Background(), BatchRequest{
		Principal: principal, RequestID: "batch-rollback-request",
		Operations: []BatchOperation{
			{Create: &CreateRequest{Collection: "records", Input: input("duplicate")}},
			{Create: &CreateRequest{Collection: "records", Input: input("duplicate")}},
		},
	})
	if err == nil {
		t.Fatal("duplicate batch succeeded")
	}
	for _, table := range []string{"app.records", "pgbase_meta.record_revisions", "pgbase_meta.record_audit_events", "pgbase_meta.record_outbox"} {
		var count int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("%s rows after rollback = %d", table, count)
		}
	}
}

type reverseBatchBarrierHook struct {
	mu       sync.Mutex
	arrivals int
	release  chan struct{}
	after    int
}

func (h *reverseBatchBarrierHook) BeforeMutation(ctx context.Context, _ Mutation) error {
	h.mu.Lock()
	h.arrivals++
	if h.arrivals == 2 {
		close(h.release)
	}
	release := h.release
	h.mu.Unlock()
	select {
	case <-release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (h *reverseBatchBarrierHook) AfterMutation(context.Context, Mutation) {
	h.mu.Lock()
	h.after++
	h.mu.Unlock()
}

func TestConcurrentReverseOrderBatchesRetryWithoutPartialHooks(t *testing.T) {
	_, _, records, principal := newRecordOperationsTest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	first, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "reverse-seed-one", Collection: "records", Input: batchInput("one")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "reverse-seed-two", Collection: "records", Input: batchInput("two")})
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := batchRecordID(t, first), batchRecordID(t, second)
	hook := &reverseBatchBarrierHook{release: make(chan struct{})}
	records.SetHooks([]MutationHook{hook})
	requests := []BatchRequest{
		{Principal: principal, RequestID: "reverse-batch-one", Operations: []BatchOperation{{Update: &UpdateRequest{Collection: "records", ID: firstID, Input: map[string]json.RawMessage{"count": json.RawMessage(`1`)}}}, {Update: &UpdateRequest{Collection: "records", ID: secondID, Input: map[string]json.RawMessage{"count": json.RawMessage(`1`)}}}}},
		{Principal: principal, RequestID: "reverse-batch-two", Operations: []BatchOperation{{Update: &UpdateRequest{Collection: "records", ID: secondID, Input: map[string]json.RawMessage{"count": json.RawMessage(`2`)}}}, {Update: &UpdateRequest{Collection: "records", ID: firstID, Input: map[string]json.RawMessage{"count": json.RawMessage(`2`)}}}}},
	}
	errorsByBatch := make(chan error, 2)
	for _, request := range requests {
		request := request
		go func() { _, err := records.Batch(ctx, request); errorsByBatch <- err }()
	}
	for range requests {
		if err := <-errorsByBatch; err != nil {
			t.Fatalf("reverse batch: %v", err)
		}
	}
	hook.mu.Lock()
	after := hook.after
	arrivals := hook.arrivals
	hook.mu.Unlock()
	if after != 4 {
		t.Fatalf("after hooks=%d arrivals=%d, want only four committed events", after, arrivals)
	}
}

func TestBatchMixedCreateUpdateDeleteAndHooks(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	first, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "batch-seed-one", Collection: "records", Input: batchInput("one")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "batch-seed-two", Collection: "records", Input: batchInput("two")})
	if err != nil {
		t.Fatal(err)
	}
	firstID := batchRecordID(t, first)
	secondID := batchRecordID(t, second)
	hook := &countingHook{}
	records.SetHooks([]MutationHook{hook})
	results, err := records.Batch(ctx, BatchRequest{Principal: principal, RequestID: "batch-mixed", Operations: []BatchOperation{
		{Create: &CreateRequest{Collection: "records", Input: batchInput("three")}},
		{Update: &UpdateRequest{Collection: "records", ID: firstID, Input: map[string]json.RawMessage{"count": json.RawMessage(`4`)}}},
		{Delete: &DeleteRequest{Collection: "records", ID: secondID}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 3 || hook.before != 3 || hook.after != 3 {
		t.Fatalf("results/hooks=%d/%d/%d", len(results), hook.before, hook.after)
	}
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM app.records`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("remaining records=%d err=%v", count, err)
	}
	var updated int
	if err := pool.QueryRow(ctx, `SELECT count FROM app.records WHERE id=$1`, firstID).Scan(&updated); err != nil || updated != 4 {
		t.Fatalf("updated count=%d err=%v", updated, err)
	}
}

func TestBatchBeforeHookFailureRollsBackAllArtifacts(t *testing.T) {
	pool, _, records, principal := newRecordOperationsTest(t)
	records.SetHooks([]MutationHook{&countingHook{fail: errors.New("reject")}})
	_, err := records.Batch(context.Background(), BatchRequest{Principal: principal, RequestID: "batch-hook-failure", Operations: []BatchOperation{{Create: &CreateRequest{Collection: "records", Input: batchInput("never")}}}})
	if err == nil {
		t.Fatal("batch hook rejection succeeded")
	}
	for _, table := range []string{"app.records", "pgbase_meta.record_revisions", "pgbase_meta.record_audit_events", "pgbase_meta.record_outbox"} {
		var count int
		if err := pool.QueryRow(context.Background(), "SELECT count(*) FROM "+table).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s=%d err=%v", table, count, err)
		}
	}
}

func batchInput(title string) map[string]json.RawMessage {
	return map[string]json.RawMessage{"title": json.RawMessage(`"` + title + `"`)}
}

func batchRecordID(t *testing.T, result MutationResult) uuid.UUID {
	t.Helper()
	id, err := uuid.Parse(result.Record.Data["id"].(string))
	if err != nil {
		t.Fatal(err)
	}
	return id
}
