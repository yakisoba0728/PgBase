//go:build integration

package query

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

const (
	r0ExplainRootRows = 100_001
	// The full-sort plan this replaced read every root row and touched over
	// this many buffers; the page-index scan reads a single page's worth.
	r0ExplainFullSortExecutionBlocks = 900
	r0ExplainMaximumExecutionBlocks  = 100
	r0ExplainMaximumScannedRows      = 200
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

func TestRelationFilterWithRootDescendingSortUsesThePageIndexBackwardScan(t *testing.T) {
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
	// created_at is a NOT NULL system column, so descending pages can take the
	// btree's own NULLS FIRST placement. That is what lets the planner read the
	// (created_at, id) page index backward instead of sorting the collection.
	wantOrder := `ORDER BY "rq_root"."created_at" DESC NULLS FIRST, "rq_root"."id" DESC NULLS FIRST`
	if !strings.Contains(statement.SQL, wantOrder) {
		t.Fatalf("root DESC SQL = %q, want order %q", statement.SQL, wantOrder)
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
	// Resolve the page index by its definition rather than by name, so this
	// asserts the physical (created_at, id) index and not a naming rule.
	var pageIndex string
	if err := connection.QueryRow(ctx,
		`SELECT indexname FROM pg_indexes WHERE schemaname = 'app' AND tablename = $1
			AND indexdef LIKE '%(created_at, id)'`, root.TableName).Scan(&pageIndex); err != nil {
		t.Fatal(err)
	}
	top := explained[0].Plan
	backward := r0FindExplainNode(top, func(node r0ExplainNode) bool {
		return node.RelationName == root.TableName && node.IndexName == pageIndex &&
			node.ScanDirection == "Backward"
	})
	if backward == nil {
		t.Fatalf("plan did not scan %s backward: %#v", pageIndex, top)
	}
	if backward.ActualRows > r0ExplainMaximumScannedRows {
		t.Fatalf("backward scan read %v rows, want at most %d of the %d-row fixture",
			backward.ActualRows, r0ExplainMaximumScannedRows, r0ExplainRootRows)
	}
	if sortNode := r0FindExplainNode(top, func(node r0ExplainNode) bool {
		return node.NodeType == "Sort"
	}); sortNode != nil {
		t.Fatalf("plan still sorts instead of reading the page index in order: %#v", *sortNode)
	}
	if blocks := top.SharedHitBlocks + top.SharedReadBlocks; blocks > r0ExplainMaximumExecutionBlocks {
		t.Fatalf("execution blocks = %d, want at most %d; the full-sort plan needed over %d; plan=%#v",
			blocks, r0ExplainMaximumExecutionBlocks, r0ExplainFullSortExecutionBlocks, top)
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
