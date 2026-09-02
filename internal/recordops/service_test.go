package recordops

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/recordartifact"
	"github.com/webisnoteasy/PgBase/internal/schema"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

const (
	testAdministratorID = "019551d0-e9c0-7000-8000-000000000001"
	testRequestID       = "request_12345678"
)

func TestIdempotencyKeyValidationIsBounded(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{
		{strings.Repeat("a", 15), false},
		{strings.Repeat("a", 16), true},
		{"Abcdefghijklmnop._~-", true},
		{strings.Repeat("z", 128), true},
		{strings.Repeat("z", 129), false},
		{"abcdefghijklmn p", false},
		{"abcdefghijklmn/p", false},
		{"abcdefghijklmn\u007fp", false},
	} {
		if got := validIdempotencyKey(test.value); got != test.valid {
			t.Errorf("validIdempotencyKey(%q)=%t want %t", test.value, got, test.valid)
		}
	}
}

func TestRelationOperationOutcomeIsBounded(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want observability.Outcome
	}{
		{name: "success", want: observability.OutcomeSuccess},
		{name: "validation", err: &query.ValidationError{Details: []query.ValidationDetail{{Field: "secret-field", Code: "relation_invalid_value"}}}, want: observability.OutcomeValidation},
		{name: "budget", err: &query.ValidationError{Details: []query.ValidationDetail{{Field: "expand", Code: "expand_budget_exceeded"}}}, want: observability.OutcomeBudget},
		{name: "response budget", err: &query.ValidationError{Details: []query.ValidationDetail{{Field: "response", Code: "response_too_large"}}}, want: observability.OutcomeBudget},
		{name: "conflict", err: &Error{Code: CodePreconditionFailed, CurrentETag: "secret-etag"}, want: observability.OutcomeConflict},
		{name: "membership unavailable", err: &Error{Code: CodeUnavailable}, want: observability.OutcomeUnavailable},
		{name: "membership canceled", err: unavailableMembership(context.Canceled), want: observability.OutcomeCanceled},
		{name: "catalog conflict", err: schema.ErrConflict, want: observability.OutcomeConflict},
		{name: "canceled", err: context.Canceled, want: observability.OutcomeCanceled},
		{name: "failure", err: errors.New("secret failure"), want: observability.OutcomeFailure},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := relationOperationOutcome(test.err); got != test.want {
				t.Fatalf("relationOperationOutcome() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestEntityTagSetValidationAndStrongComparison(t *testing.T) {
	current := `"pgb1.abc"`
	for _, test := range []struct {
		name    string
		tags    EntityTagSet
		valid   bool
		matches bool
	}{
		{"missing", EntityTagSet{}, true, false},
		{"any", EntityTagSet{Present: true, Any: true}, true, true},
		{"strong", EntityTagSet{Present: true, Values: []string{current}}, true, true},
		{"weak", EntityTagSet{Present: true, Values: []string{"W/" + current}}, true, false},
		{"other", EntityTagSet{Present: true, Values: []string{`"other"`, current}}, true, true},
		{"empty", EntityTagSet{Present: true}, false, false},
		{"mixed any", EntityTagSet{Present: true, Any: true, Values: []string{current}}, false, false},
		{"malformed", EntityTagSet{Present: true, Values: []string{"no-quotes"}}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateEntityTagSet(test.tags)
			if (err == nil) != test.valid {
				t.Fatalf("validation error=%v valid=%t", err, test.valid)
			}
			if err == nil && test.tags.matches(current) != test.matches {
				t.Fatalf("matches=%t want %t", test.tags.matches(current), test.matches)
			}
		})
	}
	overflow := EntityTagSet{Present: true, Values: make([]string, maximumEntityTags+1)}
	for index := range overflow.Values {
		overflow.Values[index] = `"x"`
	}
	if !IsCode(validateEntityTagSet(overflow), CodeInvalidPrecondition) {
		t.Fatal("too many tags did not return invalid_precondition")
	}
}

func TestCanonicalInputPreservesNumbersAndIgnoresObjectFormatting(t *testing.T) {
	left, err := canonicalInput(map[string]json.RawMessage{
		"payload": json.RawMessage(`{"b": [1.2300, true], "a": 9007199254740993}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	right, err := canonicalInput(map[string]json.RawMessage{
		"payload": json.RawMessage(`{"a":9007199254740993,"b":[1.2300,true]}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(left, right) || !bytes.Contains(left, []byte("9007199254740993")) || !bytes.Contains(left, []byte("1.2300")) {
		t.Fatalf("canonical inputs differ or lost number lexeme: %s / %s", left, right)
	}
}

func TestSchemaDigestIsOrderIndependentAndRepresentationSensitive(t *testing.T) {
	firstID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000011")
	secondID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000012")
	collection := schema.Collection{Fields: []schema.Field{
		{ID: secondID, Name: "count", Type: schema.TypeInteger, Ordinal: 1},
		{ID: firstID, Name: "title", Type: schema.TypeText, Ordinal: 0, Required: true},
	}}
	left, err := recordartifact.SchemaDigest(collection)
	if err != nil {
		t.Fatal(err)
	}
	collection.Fields[0], collection.Fields[1] = collection.Fields[1], collection.Fields[0]
	right, err := recordartifact.SchemaDigest(collection)
	if err != nil {
		t.Fatal(err)
	}
	if left != right {
		t.Fatal("schema digest depended on slice order")
	}
	collection.Fields[0].Name = "headline"
	changed, err := recordartifact.SchemaDigest(collection)
	if err != nil {
		t.Fatal(err)
	}
	if changed == left {
		t.Fatal("schema representation change did not invalidate digest")
	}
}

func TestSchemaDigestIncludesRelationSemanticsButNotPhysicalBindingName(t *testing.T) {
	targetID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000013")
	fieldID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000014")
	collection := schema.Collection{Fields: []schema.Field{{
		ID: fieldID, Name: "owners", ColumnName: "owners", Type: schema.TypeRelation,
		Ordinal: 0, Indexed: true, Relation: &schema.RelationSpec{
			TargetCollectionID: targetID, Cardinality: schema.RelationMultiple,
			MinSelect: 0, MaxSelect: 10, OnDelete: schema.RelationRestrict,
			ValueCodecVersion: schema.RelationValueCodecVersion,
			Storage:           schema.RelationStorage{Kind: schema.RelationStorageEdgeTableV1, Name: "rel_tbl_first"},
		},
	}}}
	baseline, err := recordartifact.SchemaDigest(collection)
	if err != nil {
		t.Fatal(err)
	}

	physicalRename := collection
	physicalRename.Fields = append([]schema.Field(nil), collection.Fields...)
	physicalSpec := *collection.Fields[0].Relation
	physicalSpec.Storage.Name = "rel_tbl_second"
	physicalRename.Fields[0].Relation = &physicalSpec
	physicalDigest, err := recordartifact.SchemaDigest(physicalRename)
	if err != nil {
		t.Fatal(err)
	}
	if physicalDigest != baseline {
		t.Fatal("physical relation binding name changed the semantic schema digest")
	}

	tests := []struct {
		name   string
		change func(*schema.RelationSpec)
	}{
		{name: "target", change: func(spec *schema.RelationSpec) { spec.TargetCollectionID = uuid.New() }},
		{name: "cardinality", change: func(spec *schema.RelationSpec) { spec.Cardinality = schema.RelationSingle }},
		{name: "minimum", change: func(spec *schema.RelationSpec) { spec.MinSelect = 1 }},
		{name: "maximum", change: func(spec *schema.RelationSpec) { spec.MaxSelect = 11 }},
		{name: "delete action", change: func(spec *schema.RelationSpec) { spec.OnDelete = schema.RelationCascade }},
		{name: "codec", change: func(spec *schema.RelationSpec) { spec.ValueCodecVersion++ }},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			changed := collection
			changed.Fields = append([]schema.Field(nil), collection.Fields...)
			spec := *collection.Fields[0].Relation
			test.change(&spec)
			changed.Fields[0].Relation = &spec
			digest, err := recordartifact.SchemaDigest(changed)
			if err != nil {
				t.Fatal(err)
			}
			if digest == baseline {
				t.Fatalf("%s change did not invalidate schema digest", test.name)
			}
		})
	}
}

func TestLegacyETagIsStrongAndRevisionETagUsesIndependentValidator(t *testing.T) {
	collectionID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000021")
	recordID := uuid.MustParse("019551d0-e9c0-7000-8000-000000000022")
	digest := recordartifact.Digest("schema")
	legacy, err := recordartifact.ETag(collectionID, recordID, digest, map[string]any{"id": recordID.String(), "value": 1}, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(legacy, `"pgb0.`) || strings.HasPrefix(legacy, "W/") {
		t.Fatalf("legacy ETag=%q", legacy)
	}
	first := recordartifact.RevisionETag(collectionID, recordID, digest, bytes.Repeat([]byte{1}, 32))
	second := recordartifact.RevisionETag(collectionID, recordID, digest, bytes.Repeat([]byte{2}, 32))
	if first == second || !strings.HasPrefix(first, `"pgb1.`) {
		t.Fatalf("revision tags first=%q second=%q", first, second)
	}
}

func newRecordOperationsTest(t testing.TB) (*database.Pool, *schema.Service, *Service, Principal) {
	_, pool, schemas, records, principal := newRecordOperationsTestWithURL(t)
	return pool, schemas, records, principal
}

func newRecordOperationsTestWithURL(t testing.TB) (string, *database.Pool, *schema.Service, *Service, Principal) {
	t.Helper()
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	administratorID := uuid.MustParse(testAdministratorID)
	if _, err := pool.Exec(ctx, `INSERT INTO pgbase_meta.admins
		(id, email, password_hash, active, created_at, updated_at)
		VALUES ($1, 'recordops@example.test', 'test', true, transaction_timestamp(), transaction_timestamp())`, administratorID); err != nil {
		t.Fatal(err)
	}
	schemas := schema.NewService(pool, &schema.Cache{}, schema.Planner{}, nil)
	definition := schema.CreateCollectionInput{Name: "records", Fields: []schema.FieldInput{
		{Name: "title", Type: schema.TypeText, Required: true, Unique: true},
		{Name: "count", Type: schema.TypeInteger, Default: json.RawMessage(`"0"`)},
		{Name: "metadata", Type: schema.TypeJSONB, Default: json.RawMessage(`{}`)},
	}}
	plan, err := schemas.PlanCreate(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.ApplyCreate(ctx, definition, plan.Checksum, plan.BaseVersion, administratorID); err != nil {
		t.Fatal(err)
	}
	records := NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, nil)
	return databaseURL, pool, schemas, records, Principal{Kind: PrincipalAdministrator, ID: administratorID}
}

func TestStableOperationErrorsDoNotExposeSensitiveContext(t *testing.T) {
	err := &Error{Code: CodeIdempotencyConflict, CurrentETag: `"pgb1.safe"`}
	if got := err.Error(); got != string(CodeIdempotencyConflict) || strings.Contains(got, "secret") {
		t.Fatalf("Error()=%q", got)
	}
	if !IsCode(err, CodeIdempotencyConflict) || IsCode(err, CodePreconditionFailed) {
		t.Fatal("IsCode classification failed")
	}
	notFound := fmt.Errorf("read record: %w", query.ErrRecordNotFound)
	if !errors.Is(notFound, query.ErrRecordNotFound) || errors.Is(notFound, query.ErrCollectionNotFound) {
		t.Fatal("record sentinel no longer survives wrapping as its own cause")
	}
	if got := relationOperationOutcome(notFound); got != observability.OutcomeValidation {
		t.Fatalf("wrapped record sentinel outcome = %q, want %q", got, observability.OutcomeValidation)
	}
}

func TestRelationReadTelemetryFollowsRequestedRelationWork(t *testing.T) {
	for _, test := range []struct {
		name   string
		expand string
		filter string
		sort   string
		want   bool
	}{
		{name: "scalar only", filter: "title='ada'", sort: "-created_at"},
		{name: "expand", expand: "owner", want: true},
		{name: "relation sort", sort: "-owner.name", want: true},
		{name: "relation filter", filter: "owner.name='ada' && count>2", want: true},
		{name: "quoted dotted literal", filter: "title='owner.name'"},
		{name: "quoted escaped literal", filter: "title='it''s owner.name' && count>2"},
		{name: "decimal literal", filter: "count>1.5"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := readTouchesRelationField(test.expand, test.filter, test.sort); got != test.want {
				t.Fatalf("readTouchesRelationField(%q, %q, %q) = %t, want %t",
					test.expand, test.filter, test.sort, got, test.want)
			}
		})
	}

	// The flag must be wired into List itself: a relation sort rejection has to
	// leave a relation sample even though the request carries no expand, and it
	// must not be charged to expansion, whose budgets and alerts are separate.
	recorder := &relationTelemetryRecorder{}
	records := NewService(nil, nil, []byte("0123456789abcdef0123456789abcdef"), nil, recorder, nil)
	principal := Principal{Kind: PrincipalAdministrator, ID: uuid.MustParse(testAdministratorID)}
	ctx := context.Background()
	for _, request := range []ListRequest{{Sort: "owner.name"}, {Sort: "-created_at"}, {Expand: "owner"}} {
		request.Principal, request.RequestID, request.Collection = principal, testRequestID, "articles"
		if _, err := records.List(ctx, request); err == nil {
			t.Fatalf("List(sort=%q, expand=%q) without a pool returned no error", request.Sort, request.Expand)
		}
	}
	events := recorder.snapshot()
	if len(events) != 2 {
		t.Fatalf("relation read telemetry = %#v, want two relation samples", events)
	}
	for index, want := range []observability.Operation{observability.OperationRelationRead, observability.OperationRelationExpand} {
		if events[index].Component() != observability.ComponentRelation || events[index].Operation() != want {
			t.Fatalf("relation read telemetry[%d] = %#v, want operation %q", index, events[index], want)
		}
	}
}

func TestIdempotencyCleanupFailureIsDiagnosable(t *testing.T) {
	var logs bytes.Buffer
	cleaner := &IdempotencyCleaner{
		logger:     slog.New(slog.NewTextHandler(&logs, nil)),
		interval:   defaultCleanerInterval,
		batchSize:  defaultCleanerBatch,
		batchYield: defaultCleanerYield,
	}
	cleaner.drainWithoutAffectingReadiness(context.Background())
	if !strings.Contains(logs.String(), "record idempotency cleanup failed") ||
		!strings.Contains(logs.String(), "record idempotency cleaner is unavailable") {
		t.Fatalf("cleanup failure did not record its cause: %q", logs.String())
	}
}
