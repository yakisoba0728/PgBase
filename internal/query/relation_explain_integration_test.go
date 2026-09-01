//go:build integration

package query

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const (
	r0ExplainRootRows               = 100_001
	r0ExplainMinimumExecutionBlocks = 900
)

type r0ExplainEnvelope struct {
	Plan r0ExplainNode `json:"Plan"`
}

type r0ExplainNode struct {
	NodeType         string          `json:"Node Type"`
	RelationName     string          `json:"Relation Name"`
	IndexName        string          `json:"Index Name"`
	ScanDirection    string          `json:"Scan Direction"`
	ActualRows       float64         `json:"Actual Rows"`
	SharedHitBlocks  int64           `json:"Shared Hit Blocks"`
	SharedReadBlocks int64           `json:"Shared Read Blocks"`
	Plans            []r0ExplainNode `json:"Plans"`
}

func TestCurrentRelationFilterRootDescendingSortUsesFullSortInsteadOfPageIndex(t *testing.T) {
	_, pool, schemas, records := newR0QueryServiceTest(t)
	ctx := context.Background()
	target := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{Name: "r0_explain_targets"})
	root := applyR0QueryCollection(t, schemas, schema.CreateCollectionInput{
		Name: "r0_explain_roots",
		Fields: []schema.FieldInput{
			{Name: "owner", Type: schema.TypeRelation, Relation: &schema.RelationInput{
				TargetCollection: target.Name, Cardinality: schema.RelationSingle, OnDelete: schema.RelationRestrict,
			}},
		},
	})
	targetRecord, err := records.Create(ctx, target.Name, map[string]json.RawMessage{})
	if err != nil {
		t.Fatal(err)
	}
	targetID := uuid.MustParse(targetRecord[systemFieldID].(string))
	rootTable := pgx.Identifier{"app", root.TableName}.Sanitize()
	if tag, err := pool.Exec(ctx, `INSERT INTO `+rootTable+` (id, created_at, updated_at, owner)
		SELECT md5('r0-explain-root-' || value::text)::uuid,
			'2020-01-01 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			'2020-01-01 00:00:00+00'::timestamptz + value * interval '1 microsecond',
			$1
		FROM generate_series(1, $2::integer) AS generated(value)`, targetID, r0ExplainRootRows); err != nil {
		t.Fatal(err)
	} else if tag.RowsAffected() != r0ExplainRootRows {
		t.Fatalf("root fixture rows = %d, want %d", tag.RowsAffected(), r0ExplainRootRows)
	}
	if _, err := pool.Exec(ctx, "ANALYZE "+rootTable); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, "ANALYZE "+pgx.Identifier{"app", target.TableName}.Sanitize()); err != nil {
		t.Fatal(err)
	}

	catalog, err := schemas.Catalog(ctx)
	if err != nil {
		t.Fatal(err)
	}
	filter := mustParseFilter(t, "owner = '"+targetID.String()+"'")
	requested, err := parseSort("-created_at")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := compileRelationListPlan(catalog, catalog.Collections[root.Name], filter, effectiveSortKeys(requested))
	if err != nil {
		t.Fatal(err)
	}
	statement, err := plan.statement(nil, 50)
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := `ORDER BY "rq_root"."created_at" DESC NULLS LAST, "rq_root"."id" DESC NULLS LAST`
	if !strings.Contains(statement.SQL, wantOrder) {
		t.Fatalf("root DESC SQL = %q, want current order %q", statement.SQL, wantOrder)
	}

	connection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Release()
	if _, err := connection.Exec(ctx, "SET max_parallel_workers_per_gather = 0"); err != nil {
		t.Fatal(err)
	}
	var raw []byte
	if err := connection.QueryRow(ctx,
		"EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON, COSTS OFF, TIMING OFF, SUMMARY OFF) "+statement.SQL,
		statement.Args...).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var explained []r0ExplainEnvelope
	if err := json.Unmarshal(raw, &explained); err != nil || len(explained) != 1 {
		t.Fatalf("decode EXPLAIN = %v envelopes=%d raw=%s", err, len(explained), raw)
	}
	top := explained[0].Plan
	sortNode := r0FindExplainNode(top, func(node r0ExplainNode) bool { return node.NodeType == "Sort" })
	rootScan := r0FindExplainNode(top, func(node r0ExplainNode) bool {
		return node.NodeType == "Seq Scan" && node.RelationName == root.TableName
	})
	if sortNode == nil || rootScan == nil || rootScan.ActualRows != float64(r0ExplainRootRows) {
		t.Fatalf("current plan missing full Sort/root Seq Scan: %#v", top)
	}
	if backward := r0FindExplainNode(top, func(node r0ExplainNode) bool {
		return node.RelationName == root.TableName && node.IndexName != "" && node.ScanDirection == "Backward"
	}); backward != nil {
		t.Fatalf("current plan unexpectedly used backward root index: %#v", *backward)
	}
	if blocks := top.SharedHitBlocks + top.SharedReadBlocks; blocks < r0ExplainMinimumExecutionBlocks {
		t.Fatalf("current execution blocks = %d, want at least fixed-fixture baseline %d; plan=%#v",
			blocks, r0ExplainMinimumExecutionBlocks, top)
	}
}

func r0FindExplainNode(root r0ExplainNode, match func(r0ExplainNode) bool) *r0ExplainNode {
	if match(root) {
		copy := root
		return &copy
	}
	for _, child := range root.Plans {
		if found := r0FindExplainNode(child, match); found != nil {
			return found
		}
	}
	return nil
}
