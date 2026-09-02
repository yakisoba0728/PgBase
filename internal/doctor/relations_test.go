package doctor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type relationDoctorFixture struct {
	pool                        *database.Pool
	sourceID, usersID, tagsID   uuid.UUID
	singleFieldID, multiFieldID uuid.UUID
	singleNames, multiNames     schema.RelationPhysicalNames
}

func newRelationDoctorFixture(t *testing.T) relationDoctorFixture {
	t.Helper()
	pool := newDoctorPool(t)
	ctx := context.Background()
	now := time.Now().UTC()
	store := schema.NewStore(pool)

	users := createEmptyDoctorCollection(t, ctx, pool, store, "users", now)
	tags := createEmptyDoctorCollection(t, ctx, pool, store, "tags", now)
	catalog := schema.Catalog{Collections: map[string]schema.Collection{
		users.Name: users,
		tags.Name:  tags,
	}}
	maximum := 5
	plan, err := (schema.Planner{}).Plan(catalog, schema.NewCreateChange(schema.CreateCollectionInput{
		Name: "posts",
		Fields: []schema.FieldInput{
			{
				Name: "author", Type: schema.TypeRelation, Unique: true,
				Relation: &schema.RelationInput{
					TargetCollection: "users", Cardinality: schema.RelationSingle,
					OnDelete: schema.RelationCascade,
				},
			},
			{
				Name: "tags", Type: schema.TypeRelation,
				Relation: &schema.RelationInput{
					TargetCollection: "tags", Cardinality: schema.RelationMultiple,
					MaxSelect: &maximum, OnDelete: schema.RelationSetNull,
				},
			},
		},
	}))
	if err != nil {
		t.Fatalf("plan relation fixture: %v", err)
	}
	for index, statement := range plan.Statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("execute relation fixture DDL %d: %v\n%s", index, err, statement)
		}
	}

	source := schema.Collection{ID: uuid.New(), Name: "posts", TableName: "posts", CreatedAt: now}
	for _, planned := range plan.Definition.Fields {
		source.Fields = append(source.Fields, schema.Field{
			ID: uuid.New(), Name: planned.Name, ColumnName: planned.Name, Type: planned.Type,
			Required: planned.Required, Unique: planned.Unique, Indexed: planned.Indexed,
			Ordinal: planned.Ordinal,
		})
	}
	if err := store.InsertCollection(ctx, source, plan.Definition, now); err != nil {
		t.Fatalf("insert relation fixture metadata: %v", err)
	}
	return relationDoctorFixture{
		pool: pool, sourceID: source.ID, usersID: users.ID, tagsID: tags.ID,
		singleFieldID: source.Fields[0].ID, multiFieldID: source.Fields[1].ID,
		singleNames: schema.NewRelationPhysicalNames("posts", "author"),
		multiNames:  schema.NewRelationPhysicalNames("posts", "tags"),
	}
}

func createEmptyDoctorCollection(t *testing.T, ctx context.Context, pool *database.Pool, store *schema.Store, name string, createdAt time.Time) schema.Collection {
	t.Helper()
	plan, err := (schema.Planner{}).Plan(schema.Catalog{Collections: map[string]schema.Collection{}}, schema.NewCreateChange(schema.CreateCollectionInput{Name: name}))
	if err != nil {
		t.Fatalf("plan empty target collection: %v", err)
	}
	for _, statement := range plan.Statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("create empty target collection: %v", err)
		}
	}
	collection := schema.Collection{ID: uuid.New(), Name: name, TableName: name, Fields: []schema.Field{}, CreatedAt: createdAt}
	if err := store.InsertCollection(ctx, collection, plan.Definition, createdAt); err != nil {
		t.Fatalf("insert empty target collection metadata: %v", err)
	}
	return collection
}

func execDoctorSQL(t *testing.T, pool *database.Pool, statements ...any) {
	t.Helper()
	if len(statements) == 0 {
		return
	}
	query, ok := statements[0].(string)
	if !ok {
		t.Fatal("doctor test SQL must start with a string")
	}
	if !strings.Contains(query, "$1") && len(statements) > 1 {
		queries := make([]string, 0, len(statements))
		for _, statement := range statements {
			text, isString := statement.(string)
			if !isString {
				queries = nil
				break
			}
			queries = append(queries, text)
		}
		if queries != nil {
			for _, statement := range queries {
				if _, err := pool.Exec(context.Background(), statement); err != nil {
					t.Fatalf("execute doctor fault injection: %v\n%s", err, statement)
				}
			}
			return
		}
	}
	args := statements[1:]
	if _, err := pool.Exec(context.Background(), query, args...); err != nil {
		t.Fatalf("execute doctor fault injection: %v\n%s", err, query)
	}
}

func replaceRelationFunctionWithNoop(t *testing.T, pool *database.Pool, name string) {
	t.Helper()
	function := pgx.Identifier{"pgbase_relation", name}.Sanitize()
	execDoctorSQL(t, pool, `create or replace function `+function+`() returns trigger language plpgsql as $$ begin return null; end $$`)
}

func assertRelationFindingsRedacted(t *testing.T, report Report, fixture relationDoctorFixture) {
	t.Helper()
	for _, item := range report.Findings {
		serialized := item.Code + " " + item.Message
		for _, secret := range []string{
			fixture.sourceID.String(), fixture.usersID.String(), fixture.tagsID.String(),
			fixture.singleFieldID.String(), fixture.multiFieldID.String(),
			fixture.singleNames.SingleForeignKey, fixture.singleNames.SingleReverseIndex,
			fixture.multiNames.MultiTable, fixture.multiNames.CardinalityFunction,
			fixture.multiNames.PositionFunction,
		} {
			if strings.Contains(serialized, secret) {
				t.Fatalf("finding leaked internal identifier %q: %#v", secret, item)
			}
		}
	}
}

func TestOversizedTextDefaultCheckIsBoundedAndTypeAware(t *testing.T) {
	text := string(schema.TypeText)
	jsonb := string(schema.TypeJSONB)
	present := true
	absent := false
	oversized := `"` + strings.Repeat("x", schema.MaximumTextBytes+1) + `"`
	bounded := `"` + strings.Repeat("x", schema.MaximumTextBytes) + `"`

	for _, test := range []struct {
		name    string
		catalog []catalogRow
		want    int
	}{
		{name: "no rows"},
		{
			name:    "collection row without a field",
			catalog: []catalogRow{{collection: "posts", table: "posts"}},
		},
		{
			name:    "text default at the bound",
			catalog: []catalogRow{{fieldType: &text, hasDefault: &present, defaultJSON: &bounded}},
		},
		{
			name:    "default column set without has_default",
			catalog: []catalogRow{{fieldType: &text, hasDefault: &absent, defaultJSON: &oversized}},
		},
		{
			name:    "oversized value on a non-text field",
			catalog: []catalogRow{{fieldType: &jsonb, hasDefault: &present, defaultJSON: &oversized}},
		},
		{
			name:    "oversized text default",
			catalog: []catalogRow{{fieldType: &text, hasDefault: &present, defaultJSON: &oversized}},
			want:    1,
		},
		{
			name: "many oversized text defaults report once",
			catalog: []catalogRow{
				{fieldType: &text, hasDefault: &present, defaultJSON: &oversized},
				{fieldType: &text, hasDefault: &present, defaultJSON: &oversized},
				{fieldType: &text, hasDefault: &present, defaultJSON: &oversized},
			},
			want: 1,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := Report{Findings: []Finding{}}
			checkOversizedTextDefaults(test.catalog, &report)
			if len(report.Findings) != test.want {
				t.Fatalf("findings = %#v, want %d", report.Findings, test.want)
			}
			for _, item := range report.Findings {
				if item.Code != "text_default_oversized" {
					t.Fatalf("finding code = %q", item.Code)
				}
			}
		})
	}
}
