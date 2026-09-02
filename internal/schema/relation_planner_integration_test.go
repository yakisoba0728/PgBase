//go:build integration

package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

func TestRelationPlannerDeferredTriggersHandleInsertAndDeleteLive(t *testing.T) {
	pool, _, _ := newSchemaServiceTest(t, Planner{})
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `CREATE TABLE "app"."tags" ("id" uuid PRIMARY KEY)`); err != nil {
		t.Fatalf("create target table: %v", err)
	}

	field := relationPlannerField("tags", "tags", RelationMultiple, false, 3, RelationSetNull)
	plan, err := planCreateIn(0, relationPlannerCatalog(), CreateCollectionInput{
		Name:   "posts",
		Fields: []FieldInput{field},
	})
	if err != nil {
		t.Fatal(err)
	}
	for index, statement := range plan.Statements {
		if _, err := pool.Exec(ctx, statement); err != nil {
			t.Fatalf("execute relation DDL statement %d: %v\n%s", index, err, statement)
		}
	}

	postID := uuid.MustParse("019b0000-0000-7000-8000-000000000100")
	firstTag := uuid.MustParse("019b0000-0000-7000-8000-000000000101")
	secondTag := uuid.MustParse("019b0000-0000-7000-8000-000000000102")
	if _, err := pool.Exec(ctx, `INSERT INTO "app"."tags" ("id") VALUES ($1), ($2)`, firstTag, secondTag); err != nil {
		t.Fatal(err)
	}
	names := NewRelationPhysicalNames("posts", "tags")
	edge := pgx.Identifier{"pgbase_relation", names.MultiTable}.Sanitize()
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO "app"."posts" ("id", "created_at", "updated_at")
			VALUES ($1, statement_timestamp(), statement_timestamp())`, postID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, "INSERT INTO "+edge+` ("source_id", "target_id", "position")
			VALUES ($1, $2, 0), ($1, $3, 1)`, postID, firstTag, secondTag)
		return err
	}); err != nil {
		t.Fatalf("commit valid INSERT-trigger transaction: %v", err)
	}

	if _, err := pool.Exec(ctx, "DELETE FROM "+edge+` WHERE "source_id" = $1 AND "position" = 1`, postID); err != nil {
		t.Fatalf("commit valid DELETE-trigger transaction: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM "app"."posts" WHERE "id" = $1`, postID); err != nil {
		t.Fatalf("commit source cascade through DELETE trigger: %v", err)
	}
	var remaining int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+edge).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("remaining relation edges = %d, want 0", remaining)
	}
}

func TestRelationPlannerDeferredNoActionAllowsRequiredCrossTableCycleDeleteLive(t *testing.T) {
	pool, _, _ := newSchemaServiceTest(t, Planner{})
	ctx := context.Background()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE "app"."cycle_alpha" (
			"id" uuid PRIMARY KEY,
			"beta" uuid NOT NULL
		);
		CREATE TABLE "app"."cycle_beta" (
			"id" uuid PRIMARY KEY,
			"alpha" uuid NOT NULL
		)`); err != nil {
		t.Fatalf("create cycle tables: %v", err)
	}

	alphaCollectionID := uuid.MustParse("019b0000-0000-7000-8000-000000000201")
	betaCollectionID := uuid.MustParse("019b0000-0000-7000-8000-000000000202")
	requiredSingle := func(name, target string, targetID uuid.UUID) PlannedField {
		return PlannedField{
			Name: name, Type: TypeRelation, Required: true, Indexed: true,
			Relation: &PlannedRelation{
				TargetCollection: target, TargetCollectionID: &targetID,
				Cardinality: RelationSingle, MinSelect: 1, MaxSelect: 1,
				OnDelete: RelationCascade, ValueCodecVersion: RelationValueCodecVersion,
				Storage: RelationStorage{Kind: RelationStorageInlineFKV1, Name: name},
			},
		}
	}
	for _, fixture := range []struct {
		source string
		field  PlannedField
	}{
		{source: "cycle_alpha", field: requiredSingle("beta", "cycle_beta", betaCollectionID)},
		{source: "cycle_beta", field: requiredSingle("alpha", "cycle_alpha", alphaCollectionID)},
	} {
		for _, statement := range createSingleRelationStatements(fixture.source, fixture.field, false) {
			if _, err := pool.Exec(ctx, statement); err != nil {
				t.Fatalf("create %s target constraint: %v\n%s", fixture.source, err, statement)
			}
		}
	}

	alphaRecordID := uuid.MustParse("019b0000-0000-7000-8000-000000000211")
	betaRecordID := uuid.MustParse("019b0000-0000-7000-8000-000000000212")
	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `INSERT INTO "app"."cycle_alpha" ("id", "beta") VALUES ($1, $2)`, alphaRecordID, betaRecordID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `INSERT INTO "app"."cycle_beta" ("id", "alpha") VALUES ($1, $2)`, betaRecordID, alphaRecordID)
		return err
	}); err != nil {
		t.Fatalf("insert deferred required cycle: %v", err)
	}

	// Physical integrity still rejects a transaction that leaves a dangling
	// target, but the check happens at commit so the logical delete graph can
	// remove every record in a required cross-table cycle first.
	err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `DELETE FROM "app"."cycle_alpha" WHERE "id" = $1`, alphaRecordID)
		return err
	})
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "23503" {
		t.Fatalf("partial cycle delete error = %v, want deferred 23503", err)
	}

	if err := pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, `DELETE FROM "app"."cycle_alpha" WHERE "id" = $1`, alphaRecordID); err != nil {
			return err
		}
		_, err := tx.Exec(ctx, `DELETE FROM "app"."cycle_beta" WHERE "id" = $1`, betaRecordID)
		return err
	}); err != nil {
		t.Fatalf("delete complete required cycle: %v", err)
	}

	var remaining int
	if err := pool.QueryRow(ctx, `
		SELECT (SELECT count(*) FROM "app"."cycle_alpha") +
		       (SELECT count(*) FROM "app"."cycle_beta")`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("remaining cycle records = %d, want 0", remaining)
	}
}
