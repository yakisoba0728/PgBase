package recordidentity

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

type failedProjectionDB struct {
	DB
	err error
}

func (db failedProjectionDB) Query(context.Context, string, ...any) (pgx.Rows, error) {
	return nil, db.err
}

func TestProjectionFailureLeavesEveryInputUnchanged(t *testing.T) {
	source := schema.Collection{ID: uuid.New(), Name: "source", RecordID: schema.RecordIDPolicy{Kind: schema.RecordIDText}}
	target := schema.Collection{ID: uuid.New(), Name: "target", RecordID: source.RecordID}
	source.Fields = []schema.Field{{Name: "targets", Relation: &schema.RelationSpec{TargetCollectionID: target.ID}}}
	catalog := schema.Catalog{Collections: map[string]schema.Collection{source.Name: source, target.Name: target}}
	sentinel := errors.New("identity query failed")
	for _, malformed := range []bool{false, true} {
		record := map[string]any{"id": strings.ToUpper(uuid.NewString()), "targets": []any{uuid.NewString()}}
		if malformed {
			record["targets"] = []any{uuid.NewString(), 12}
		}
		before, err := json.Marshal(record)
		if err != nil {
			t.Fatal(err)
		}
		err = New(nil).Project(context.Background(), failedProjectionDB{err: sentinel}, catalog, source, record)
		if malformed && !errors.Is(err, ErrInvalid) || !malformed && !errors.Is(err, sentinel) {
			t.Fatalf("malformed=%t: error=%v", malformed, err)
		}
		after, err := json.Marshal(record)
		if err != nil || string(after) != string(before) {
			t.Fatalf("failed projection mutated input: before=%s after=%s err=%v", before, after, err)
		}
	}
}
