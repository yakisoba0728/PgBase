//go:build integration

package relation

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestHydrateSequentialVisitsSourcesInInputOrderWithOneQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newLiveFixture(t, ctx, testutil.NewDatabase(t))
	defer fixture.conn.Close(ctx)

	type visit struct {
		sourceID uuid.UUID
		labels   []string
		groups   []string
	}
	counter := &countingRowsQuerier{delegate: fixture.conn}
	var visits []visit
	visited, err := fixture.bindings.HydrateSequential(ctx, counter,
		[]uuid.UUID{fixture.sourceTwo, fixture.sourceOne},
		func(sourceID uuid.UUID, values map[string]any) bool {
			labels, labelsOK := values["labels"].([]string)
			groups, groupsOK := values["groups"].([]string)
			if !labelsOK || !groupsOK {
				t.Fatalf("hydrated relation value types = labels %T, groups %T", values["labels"], values["groups"])
			}
			visits = append(visits, visit{sourceID: sourceID, labels: labels, groups: groups})
			return true
		})
	if err != nil {
		t.Fatalf("hydrate sequential: %v", err)
	}
	if visited != 2 || counter.queries != 1 || counter.closes != 1 {
		t.Fatalf("visited/queries/closes = %d/%d/%d, want 2/1/1", visited, counter.queries, counter.closes)
	}
	want := []visit{
		{sourceID: fixture.sourceTwo, labels: []string{}, groups: []string{}},
		{
			sourceID: fixture.sourceOne,
			labels:   []string{fixture.targetOne.String()},
			groups:   []string{fixture.targetThree.String()},
		},
	}
	if !reflect.DeepEqual(visits, want) {
		t.Fatalf("visits = %#v, want %#v", visits, want)
	}
}

func TestReplayHydratedSequentialStreamsCompleteRecordsInInputOrderWithOneQuery(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newLiveFixture(t, ctx, testutil.NewDatabase(t))
	defer fixture.conn.Close(ctx)

	type visit struct {
		sourceID uuid.UUID
		labels   []string
		groups   []string
	}
	counter := &countingRowsQuerier{delegate: fixture.conn}
	var visits []visit
	visited, err := fixture.bindings.ReplayHydratedSequential(ctx, counter,
		[]uuid.UUID{fixture.sourceTwo, fixture.sourceOne}, decodeLiveReplayRecord,
		func(sourceID uuid.UUID, record map[string]any) bool {
			labels, labelsOK := record["labels"].([]string)
			groups, groupsOK := record["groups"].([]string)
			if !labelsOK || !groupsOK {
				t.Fatalf("hydrated relation value types = labels %T, groups %T", record["labels"], record["groups"])
			}
			visits = append(visits, visit{sourceID: sourceID, labels: labels, groups: groups})
			return true
		})
	if err != nil {
		t.Fatalf("replay hydrated sequential: %v", err)
	}
	if visited != 2 || counter.queries != 1 || counter.closes != 1 {
		t.Fatalf("visited/queries/closes = %d/%d/%d, want 2/1/1", visited, counter.queries, counter.closes)
	}
	want := []visit{
		{sourceID: fixture.sourceTwo, labels: []string{}, groups: []string{}},
		{
			sourceID: fixture.sourceOne,
			labels:   []string{fixture.targetOne.String()},
			groups:   []string{fixture.targetThree.String()},
		},
	}
	if !reflect.DeepEqual(visits, want) {
		t.Fatalf("visits = %#v, want %#v", visits, want)
	}
}

func TestReplayHydratedSequentialStopsBeforeLaterCardinalityDrift(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newLiveFixture(t, ctx, testutil.NewDatabase(t))
	defer fixture.conn.Close(ctx)

	bindings := bindLiveFirstFieldBounds(t, fixture, 1, 10, true)
	sourceIDs := []uuid.UUID{fixture.sourceOne, fixture.sourceTwo}
	stoppedCounter := &countingRowsQuerier{delegate: fixture.conn}
	visited, err := bindings.ReplayHydratedSequential(ctx, stoppedCounter, sourceIDs, decodeLiveReplayRecord,
		func(uuid.UUID, map[string]any) bool { return false })
	if err != nil || visited != 1 || stoppedCounter.queries != 1 || stoppedCounter.closes != 1 {
		t.Fatalf("stopped visited/error/queries/closes = %d/%v/%d/%d, want 1/nil/1/1",
			visited, err, stoppedCounter.queries, stoppedCounter.closes)
	}

	validationCounter := &countingRowsQuerier{delegate: fixture.conn}
	visited, err = bindings.ReplayHydratedSequential(ctx, validationCounter, sourceIDs, decodeLiveReplayRecord,
		func(uuid.UUID, map[string]any) bool { return true })
	if visited != 1 || !errors.Is(err, ErrInvalidStoredValue) || validationCounter.queries != 1 || validationCounter.closes != 1 {
		t.Fatalf("full traversal visited/error/queries/closes = %d/%v/%d/%d, want 1/invalid stored value/1/1",
			visited, err, validationCounter.queries, validationCounter.closes)
	}
}

func TestReplayHydratedSequentialRejectsPositionDrift(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newLiveFixture(t, ctx, testutil.NewDatabase(t))
	defer fixture.conn.Close(ctx)

	table := pgx.Identifier{"pgbase_relation", fixture.labelTable}.Sanitize()
	if _, err := fixture.conn.Exec(ctx, "update "+table+" set position = 1 where source_id = $1", fixture.sourceOne); err != nil {
		t.Fatal(err)
	}
	visited, err := fixture.bindings.ReplayHydratedSequential(ctx, fixture.conn,
		[]uuid.UUID{fixture.sourceOne}, decodeLiveReplayRecord,
		func(uuid.UUID, map[string]any) bool { return true })
	if visited != 0 || !errors.Is(err, ErrInvalidStoredValue) {
		t.Fatalf("position drift visited/error = %d/%v, want 0/invalid stored value", visited, err)
	}
}

func TestReplayHydratedSequentialRejectsMissingSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newLiveFixture(t, ctx, testutil.NewDatabase(t))
	defer fixture.conn.Close(ctx)

	missing := uuid.MustParse("0199aa21-0000-7000-8000-0000000001ff")
	counter := &countingRowsQuerier{delegate: fixture.conn}
	var visits []uuid.UUID
	visited, err := fixture.bindings.ReplayHydratedSequential(ctx, counter,
		[]uuid.UUID{fixture.sourceOne, missing}, decodeLiveReplayRecord,
		func(sourceID uuid.UUID, _ map[string]any) bool {
			visits = append(visits, sourceID)
			return true
		})
	if visited != 1 || !errors.Is(err, ErrInvalidStoredValue) || counter.queries != 1 || counter.closes != 1 {
		t.Fatalf("missing source visited/error/queries/closes = %d/%v/%d/%d, want 1/invalid stored value/1/1",
			visited, err, counter.queries, counter.closes)
	}
	if !reflect.DeepEqual(visits, []uuid.UUID{fixture.sourceOne}) {
		t.Fatalf("missing source visits = %v, want only %s", visits, fixture.sourceOne)
	}
}

func decodeLiveReplayRecord(row pgx.Row) (uuid.UUID, map[string]any, error) {
	var storedID pgtype.UUID
	var createdAt, updatedAt pgtype.Timestamptz
	if err := row.Scan(&storedID, &createdAt, &updatedAt); err != nil {
		return uuid.Nil, nil, err
	}
	id := uuid.UUID(storedID.Bytes)
	return id, map[string]any{
		"id": id.String(), "created_at": createdAt.Time, "updated_at": updatedAt.Time,
		"labels": []string{}, "groups": []string{},
	}, nil
}

func TestHydrateSequentialStopSkipsLaterInvalidSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	fixture := newLiveFixture(t, ctx, testutil.NewDatabase(t))
	defer fixture.conn.Close(ctx)

	// sourceOne satisfies the minimum with one label. sourceTwo has no labels,
	// so reaching it would fail stored-value validation.
	bindings := bindLiveFirstFieldBounds(t, fixture, 1, 10, true)
	sourceIDs := []uuid.UUID{fixture.sourceOne, fixture.sourceTwo}

	stoppedCounter := &countingRowsQuerier{delegate: fixture.conn}
	var stoppedVisits []uuid.UUID
	visited, err := bindings.HydrateSequential(ctx, stoppedCounter, sourceIDs,
		func(sourceID uuid.UUID, _ map[string]any) bool {
			stoppedVisits = append(stoppedVisits, sourceID)
			return false
		})
	if err != nil {
		t.Fatalf("stopped hydration validated a later source: %v", err)
	}
	if visited != 1 || stoppedCounter.queries != 1 || stoppedCounter.closes != 1 {
		t.Fatalf("stopped visited/queries/closes = %d/%d/%d, want 1/1/1",
			visited, stoppedCounter.queries, stoppedCounter.closes)
	}
	if !reflect.DeepEqual(stoppedVisits, []uuid.UUID{fixture.sourceOne}) {
		t.Fatalf("stopped visits = %v, want only %s", stoppedVisits, fixture.sourceOne)
	}

	validationCounter := &countingRowsQuerier{delegate: fixture.conn}
	var validationVisits []uuid.UUID
	visited, err = bindings.HydrateSequential(ctx, validationCounter, sourceIDs,
		func(sourceID uuid.UUID, _ map[string]any) bool {
			validationVisits = append(validationVisits, sourceID)
			return true
		})
	if visited != 1 || !errors.Is(err, ErrInvalidStoredValue) || validationCounter.queries != 1 || validationCounter.closes != 1 {
		t.Fatalf("full traversal visited/error/queries/closes = %d/%v/%d/%d, want 1/invalid stored value/1/1",
			visited, err, validationCounter.queries, validationCounter.closes)
	}
	if !reflect.DeepEqual(validationVisits, []uuid.UUID{fixture.sourceOne}) {
		t.Fatalf("validation visits = %v, want only %s before the invalid source", validationVisits, fixture.sourceOne)
	}
}
