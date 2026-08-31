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

func TestRunAcceptsHealthySingleAndMultipleRelations(t *testing.T) {
	fixture := newRelationDoctorFixture(t)
	report := runDoctor(t, fixture.pool)
	if !report.OK || len(report.Findings) != 0 {
		t.Fatalf("report = %#v, want healthy relation schema", report)
	}
}

func TestRunReportsSingleRelationPhysicalDrift(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		breakSchema func(*testing.T, relationDoctorFixture)
	}{
		{
			name: "column missing",
			code: "relation_single_column_missing",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `alter table app.posts drop column author cascade`)
			},
		},
		{
			name: "column nullability",
			code: "relation_single_column_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `alter table app.posts alter column author set not null`)
			},
		},
		{
			name: "foreign key restrict action",
			code: "relation_single_foreign_key_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				names := fixture.singleNames
				execDoctorSQL(t, fixture.pool,
					`alter table app.posts drop constraint `+pgx.Identifier{names.SingleForeignKey}.Sanitize(),
					`alter table app.posts add constraint `+pgx.Identifier{names.SingleForeignKey}.Sanitize()+
						` foreign key (author) references app.users(id) on delete restrict deferrable initially deferred`)
			},
		},
		{
			name: "foreign key not deferred",
			code: "relation_single_foreign_key_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				names := fixture.singleNames
				execDoctorSQL(t, fixture.pool,
					`alter table app.posts drop constraint `+pgx.Identifier{names.SingleForeignKey}.Sanitize(),
					`alter table app.posts add constraint `+pgx.Identifier{names.SingleForeignKey}.Sanitize()+
						` foreign key (author) references app.users(id) on delete no action not deferrable`)
			},
		},
		{
			name: "reverse index exact shape",
			code: "relation_single_index_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				names := fixture.singleNames
				execDoctorSQL(t, fixture.pool,
					`drop index app.`+pgx.Identifier{names.SingleReverseIndex}.Sanitize(),
					`create index `+pgx.Identifier{names.SingleReverseIndex}.Sanitize()+` on app.posts(author)`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelationDoctorFixture(t)
			test.breakSchema(t, fixture)
			report := runDoctor(t, fixture.pool)
			assertFindingCode(t, report, test.code)
			assertRelationFindingsRedacted(t, report, fixture)
		})
	}
}

func TestRunReportsMultipleRelationPhysicalDrift(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		breakSchema func(*testing.T, relationDoctorFixture)
	}{
		{
			name: "table missing",
			code: "relation_multi_table_missing",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `drop table pgbase_relation.`+pgx.Identifier{fixture.multiNames.MultiTable}.Sanitize()+` cascade`)
			},
		},
		{
			name: "orphaned table",
			code: "relation_table_orphaned",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `create table pgbase_relation.orphaned_edges (source_id uuid)`)
			},
		},
		{
			name: "orphaned function",
			code: "relation_function_orphaned",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `create function pgbase_relation.orphaned_relation() returns trigger language plpgsql as $$ begin return null; end $$`)
			},
		},
		{
			name: "unexpected column",
			code: "relation_multi_columns_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `alter table pgbase_relation.`+pgx.Identifier{fixture.multiNames.MultiTable}.Sanitize()+` add column legacy integer`)
			},
		},
		{
			name: "primary key order",
			code: "relation_multi_primary_key_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				table := pgx.Identifier{"pgbase_relation", fixture.multiNames.MultiTable}.Sanitize()
				constraint := pgx.Identifier{fixture.multiNames.MultiPrimaryKey}.Sanitize()
				execDoctorSQL(t, fixture.pool,
					`alter table `+table+` drop constraint `+constraint,
					`alter table `+table+` add constraint `+constraint+` primary key (target_id, source_id)`)
			},
		},
		{
			name: "position unique order",
			code: "relation_multi_position_unique_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				table := pgx.Identifier{"pgbase_relation", fixture.multiNames.MultiTable}.Sanitize()
				constraint := pgx.Identifier{fixture.multiNames.MultiPositionUnique}.Sanitize()
				execDoctorSQL(t, fixture.pool,
					`alter table `+table+` drop constraint `+constraint,
					`alter table `+table+` add constraint `+constraint+` unique (position, source_id)`)
			},
		},
		{
			name: "position check expression",
			code: "relation_multi_position_check_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				table := pgx.Identifier{"pgbase_relation", fixture.multiNames.MultiTable}.Sanitize()
				constraint := pgx.Identifier{fixture.multiNames.MultiPositionCheck}.Sanitize()
				execDoctorSQL(t, fixture.pool,
					`alter table `+table+` drop constraint `+constraint,
					`alter table `+table+` add constraint `+constraint+` check (position >= -1)`)
			},
		},
		{
			name: "source foreign key action",
			code: "relation_multi_source_foreign_key_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				table := pgx.Identifier{"pgbase_relation", fixture.multiNames.MultiTable}.Sanitize()
				constraint := pgx.Identifier{fixture.multiNames.MultiSourceForeignKey}.Sanitize()
				execDoctorSQL(t, fixture.pool,
					`alter table `+table+` drop constraint `+constraint,
					`alter table `+table+` add constraint `+constraint+` foreign key (source_id) references app.posts(id) on delete restrict deferrable initially deferred`)
			},
		},
		{
			name: "target foreign key action",
			code: "relation_multi_target_foreign_key_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				table := pgx.Identifier{"pgbase_relation", fixture.multiNames.MultiTable}.Sanitize()
				constraint := pgx.Identifier{fixture.multiNames.MultiTargetForeignKey}.Sanitize()
				execDoctorSQL(t, fixture.pool,
					`alter table `+table+` drop constraint `+constraint,
					`alter table `+table+` add constraint `+constraint+` foreign key (target_id) references app.tags(id) on delete restrict deferrable initially deferred`)
			},
		},
		{
			name: "reverse index exact shape",
			code: "relation_multi_reverse_index_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				table := pgx.Identifier{"pgbase_relation", fixture.multiNames.MultiTable}.Sanitize()
				index := pgx.Identifier{fixture.multiNames.MultiReverseIndex}.Sanitize()
				execDoctorSQL(t, fixture.pool, `drop index pgbase_relation.`+index, `create index `+index+` on `+table+` (target_id)`)
			},
		},
		{
			name: "position function body",
			code: "relation_multi_position_function_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				replaceRelationFunctionWithNoop(t, fixture.pool, fixture.multiNames.PositionFunction)
			},
		},
		{
			name: "cardinality function body",
			code: "relation_multi_cardinality_function_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				replaceRelationFunctionWithNoop(t, fixture.pool, fixture.multiNames.CardinalityFunction)
			},
		},
		{
			name: "position trigger properties",
			code: "relation_multi_position_trigger_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				table := pgx.Identifier{"pgbase_relation", fixture.multiNames.MultiTable}.Sanitize()
				trigger := pgx.Identifier{fixture.multiNames.PositionTrigger}.Sanitize()
				function := pgx.Identifier{"pgbase_relation", fixture.multiNames.PositionFunction}.Sanitize()
				execDoctorSQL(t, fixture.pool, `drop trigger `+trigger+` on `+table,
					`create trigger `+trigger+` after insert on `+table+` for each row execute function `+function+`()`)
			},
		},
		{
			name: "source cardinality trigger properties",
			code: "relation_multi_source_cardinality_trigger_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				trigger := pgx.Identifier{fixture.multiNames.SourceCardinalityTrigger}.Sanitize()
				function := pgx.Identifier{"pgbase_relation", fixture.multiNames.CardinalityFunction}.Sanitize()
				execDoctorSQL(t, fixture.pool, `drop trigger `+trigger+` on app.posts`,
					`create trigger `+trigger+` after insert on app.posts for each row execute function `+function+`()`)
			},
		},
		{
			name: "edge cardinality trigger properties",
			code: "relation_multi_edge_cardinality_trigger_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				table := pgx.Identifier{"pgbase_relation", fixture.multiNames.MultiTable}.Sanitize()
				trigger := pgx.Identifier{fixture.multiNames.EdgeCardinalityTrigger}.Sanitize()
				function := pgx.Identifier{"pgbase_relation", fixture.multiNames.CardinalityFunction}.Sanitize()
				execDoctorSQL(t, fixture.pool, `drop trigger `+trigger+` on `+table,
					`create trigger `+trigger+` after insert on `+table+` for each row execute function `+function+`()`)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelationDoctorFixture(t)
			test.breakSchema(t, fixture)
			report := runDoctor(t, fixture.pool)
			assertFindingCode(t, report, test.code)
			assertRelationFindingsRedacted(t, report, fixture)
		})
	}
}

func TestRunReportsRelationMetadataDrift(t *testing.T) {
	tests := []struct {
		name        string
		code        string
		breakSchema func(*testing.T, relationDoctorFixture)
	}{
		{
			name: "missing binding",
			code: "relation_metadata_missing",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `delete from pgbase_meta.field_relations where field_id = $1`, fixture.singleFieldID)
			},
		},
		{
			name: "unexpected scalar binding",
			code: "relation_metadata_unexpected",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				fieldID := uuid.New()
				execDoctorSQL(t, fixture.pool, `alter table app.posts add column legacy uuid`)
				execDoctorSQL(t, fixture.pool, `insert into pgbase_meta.fields
					(id, collection_id, name, column_name, field_type, required, is_unique, is_indexed, has_default, ordinal, created_at)
					values ($1, $2, 'legacy', 'legacy', 'uuid', false, false, false, false, 2, current_timestamp)`, fieldID, fixture.sourceID)
				execDoctorSQL(t, fixture.pool, `insert into pgbase_meta.field_relations
					(field_id, source_collection_id, target_collection_id, cardinality, min_select, max_select,
					 on_delete, value_codec_version, storage_kind, storage_name, created_at)
					values ($1, $2, $3, 'single', 0, 1, 'restrict', 1, 'inline_fk_v1', 'legacy', current_timestamp)`,
					fieldID, fixture.sourceID, fixture.usersID)
			},
		},
		{
			name: "orphaned binding",
			code: "relation_metadata_orphaned",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `alter table pgbase_meta.field_relations drop constraint field_relations_source_field_fk`)
				execDoctorSQL(t, fixture.pool, `insert into pgbase_meta.field_relations
					(field_id, source_collection_id, target_collection_id, cardinality, min_select, max_select,
					 on_delete, value_codec_version, storage_kind, storage_name, created_at)
					values ($1, $2, $3, 'single', 0, 1, 'restrict', 1, 'inline_fk_v1', 'orphaned', current_timestamp)`,
					uuid.New(), fixture.sourceID, fixture.usersID)
			},
		},
		{
			name: "storage name mismatch",
			code: "relation_metadata_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `update pgbase_meta.field_relations set storage_name = 'wrong_storage' where field_id = $1`, fixture.multiFieldID)
			},
		},
		{
			name: "field minimum mismatch",
			code: "relation_metadata_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `update pgbase_meta.fields set required = true where id = $1`, fixture.singleFieldID)
			},
		},
		{
			name: "unsupported codec",
			code: "relation_metadata_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `alter table pgbase_meta.field_relations drop constraint field_relations_value_codec_version_check`)
				execDoctorSQL(t, fixture.pool, `update pgbase_meta.field_relations set value_codec_version = 2 where field_id = $1`, fixture.singleFieldID)
			},
		},
		{
			name: "target binding differs from foreign key",
			code: "relation_single_foreign_key_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `update pgbase_meta.field_relations set target_collection_id = $2 where field_id = $1`, fixture.singleFieldID, fixture.tagsID)
			},
		},
		{
			name: "cardinality bound differs from function",
			code: "relation_multi_cardinality_function_mismatch",
			breakSchema: func(t *testing.T, fixture relationDoctorFixture) {
				execDoctorSQL(t, fixture.pool, `update pgbase_meta.field_relations set max_select = 6 where field_id = $1`, fixture.multiFieldID)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newRelationDoctorFixture(t)
			test.breakSchema(t, fixture)
			report := runDoctor(t, fixture.pool)
			assertFindingCode(t, report, test.code)
			assertRelationFindingsRedacted(t, report, fixture)
		})
	}
}

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
	plan, err := (schema.Planner{}).PlanCreateWithCatalog(0, catalog, schema.CreateCollectionInput{
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
	})
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
	plan, err := (schema.Planner{}).PlanCreateWithCatalog(0, schema.Catalog{Collections: map[string]schema.Collection{}}, schema.CreateCollectionInput{Name: name})
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
