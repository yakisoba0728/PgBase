//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/membership"
	"github.com/yakisoba0728/PgBase/internal/query"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// An owned update or delete resolves its record ID inside the mutation
// transaction, so it neither opens a separate catalog transaction nor changes
// which error a bad ID reports.
func TestOwnedUpdateDeleteResolveRecordIDInOneTransaction(t *testing.T) {
	_, pool, schemas, _, principal := newRecordOperationsTestWithURL(t)
	ctx := context.Background()
	// An injected runtime keeps lease refreshes out of the connection count.
	runtime, err := membership.RegisterWriter(ctx, membership.New(pool), membership.WriterC)
	if err != nil {
		t.Fatal(err)
	}
	records := NewService(pool, schemas, []byte("0123456789abcdef0123456789abcdef"), nil, nil, runtime)
	definition := schema.CreateCollectionInput{
		Name:     "mutation_text_ids",
		RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 32, Pattern: `^[A-Za-z0-9]+$`},
		Fields:   []schema.FieldInput{{Name: "title", Type: schema.TypeText, Required: true}},
	}
	plan, err := schemas.Preview(ctx, schema.NewCreateChange(definition))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := schemas.Apply(ctx, schema.LegacyApply(schema.NewCreateChange(definition), plan, principal.ID)); err != nil {
		t.Fatal(err)
	}
	title := func(value string) map[string]json.RawMessage {
		return map[string]json.RawMessage{"title": json.RawMessage(`"` + value + `"`)}
	}
	textRecord, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "mutation-id-text-seed", Collection: definition.Name,
		Input: map[string]json.RawMessage{"id": json.RawMessage(`"TextOne"`), "title": json.RawMessage(`"seed"`)}})
	if err != nil {
		t.Fatal(err)
	}
	uuidRecord, err := records.Create(ctx, CreateRequest{Principal: principal, RequestID: "mutation-id-uuid-seed", Collection: "records", Input: title("uuid-seed")})
	if err != nil {
		t.Fatal(err)
	}
	uuidID := recordResultID(t, uuidRecord)
	malformed := map[string]json.RawMessage{"title": json.RawMessage(`{`)}

	invalidID := func(err error) bool {
		var operationError *Error
		return errors.As(err, &operationError) && operationError.Code == CodeInvalidRecordID
	}
	for _, test := range []struct {
		name string
		run  func() error
		want func(error) bool
	}{
		{"update missing text id", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u1", Collection: definition.Name, PublicID: "Missing01", Input: title("x")})
			return err
		}, func(err error) bool { return errors.Is(err, query.ErrRecordNotFound) }},
		{"update malformed text id", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u2", Collection: definition.Name, PublicID: "bad-id!", Input: title("x")})
			return err
		}, invalidID},
		{"update text collection by internal id", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u3", Collection: definition.Name, ID: textRecord.InternalID, Input: title("x")})
			return err
		}, invalidID},
		{"update malformed uuid", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u4", Collection: "records", PublicID: "not-a-uuid", Input: title("x")})
			return err
		}, invalidID},
		{"update missing uuid", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u5", Collection: "records", PublicID: uuid.NewString(), Input: title("x")})
			return err
		}, func(err error) bool { return errors.Is(err, query.ErrRecordNotFound) }},
		{"update unknown collection", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u6", Collection: "mutation_absent", PublicID: "TextOne", Input: title("x")})
			return err
		}, func(err error) bool { return errors.Is(err, query.ErrCollectionNotFound) }},
		// A record ID error still outranks an input error found before the transaction.
		{"update missing id outranks malformed input", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u7", Collection: definition.Name, PublicID: "Missing01", Input: malformed})
			return err
		}, func(err error) bool { return errors.Is(err, query.ErrRecordNotFound) }},
		{"update invalid id outranks malformed input", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u8", Collection: "records", PublicID: "not-a-uuid", Input: malformed})
			return err
		}, invalidID},
		{"update unknown collection outranks malformed input", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u9", Collection: "mutation_absent", PublicID: "TextOne", Input: malformed})
			return err
		}, func(err error) bool { return errors.Is(err, query.ErrCollectionNotFound) }},
		{"update existing id reports malformed input", func() error {
			_, err := records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-u10", Collection: definition.Name, PublicID: "TextOne", Input: malformed})
			return err
		}, func(err error) bool {
			var validation *query.ValidationError
			return errors.As(err, &validation)
		}},
		{"delete missing text id", func() error {
			_, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-d1", Collection: definition.Name, PublicID: "Missing01"})
			return err
		}, func(err error) bool { return errors.Is(err, query.ErrRecordNotFound) }},
		{"delete malformed text id", func() error {
			_, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-d2", Collection: definition.Name, PublicID: "bad-id!"})
			return err
		}, invalidID},
		{"delete text collection by internal id", func() error {
			_, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-d3", Collection: definition.Name, ID: textRecord.InternalID})
			return err
		}, invalidID},
		{"delete malformed uuid", func() error {
			_, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-d4", Collection: "records", PublicID: "not-a-uuid"})
			return err
		}, invalidID},
		{"delete without id", func() error {
			_, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-d5", Collection: "records"})
			return err
		}, invalidID},
		{"delete missing uuid", func() error {
			_, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-d6", Collection: "records", ID: uuid.New()})
			return err
		}, func(err error) bool { return errors.Is(err, query.ErrRecordNotFound) }},
		{"delete unknown collection", func() error {
			_, err := records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-d7", Collection: "mutation_absent", PublicID: "TextOne"})
			return err
		}, func(err error) bool { return errors.Is(err, query.ErrCollectionNotFound) }},
	} {
		if err := test.run(); !test.want(err) {
			t.Fatalf("%s: unexpected error %T %v", test.name, err, err)
		}
	}

	oneTransaction := func(name string, run func() (MutationResult, error)) MutationResult {
		t.Helper()
		before := pool.Stats().AcquireCount
		result, err := run()
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if acquired := pool.Stats().AcquireCount - before; acquired != 1 {
			t.Fatalf("%s acquired %d pool connections, want its one transaction", name, acquired)
		}
		return result
	}
	updated := oneTransaction("text update", func() (MutationResult, error) {
		return records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-text-update", Collection: definition.Name, PublicID: "TextOne", Input: title("text-updated")})
	})
	if updated.InternalID != textRecord.InternalID || updated.Record.Data["id"] != "TextOne" || updated.Record.Data["title"] != "text-updated" {
		t.Fatalf("text update = %#v", updated)
	}
	updated = oneTransaction("uuid update by public id", func() (MutationResult, error) {
		return records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-uuid-update", Collection: "records", PublicID: uuidID.String(), Input: title("uuid-updated")})
	})
	if updated.InternalID != uuidID || updated.Record.Data["title"] != "uuid-updated" {
		t.Fatalf("uuid update = %#v", updated)
	}
	updated = oneTransaction("uuid update by internal id", func() (MutationResult, error) {
		return records.Update(ctx, UpdateRequest{Principal: principal, RequestID: "mutation-id-uuid-update-internal", Collection: "records", ID: uuidID, Input: title("uuid-updated-again")})
	})
	if updated.InternalID != uuidID || updated.Record.Data["title"] != "uuid-updated-again" {
		t.Fatalf("uuid internal update = %#v", updated)
	}
	oneTransaction("text delete", func() (MutationResult, error) {
		return records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-text-delete", Collection: definition.Name, PublicID: "TextOne"})
	})
	oneTransaction("uuid delete", func() (MutationResult, error) {
		return records.Delete(ctx, DeleteRequest{Principal: principal, RequestID: "mutation-id-uuid-delete", Collection: "records", PublicID: uuidID.String()})
	})
	if _, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "mutation-id-text-gone", Collection: definition.Name, PublicID: "TextOne"}); !errors.Is(err, query.ErrRecordNotFound) {
		t.Fatalf("deleted text record error = %v", err)
	}
	if _, err := records.Get(ctx, GetRequest{Principal: principal, RequestID: "mutation-id-uuid-gone", Collection: "records", ID: uuidID}); !errors.Is(err, query.ErrRecordNotFound) {
		t.Fatalf("deleted uuid record error = %v", err)
	}
}
