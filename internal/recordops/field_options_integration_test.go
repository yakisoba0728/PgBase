//go:build integration

package recordops

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/schema"
	validationdomain "github.com/yakisoba0728/PgBase/internal/validation"
)

func TestCreateEnforcesEditorMaximumSize(t *testing.T) {
	_, schemas, records, principal := newRecordOperationsTest(t)
	ctx := context.Background()
	maximum := int64(3)
	createGeneratedFieldsCollection(t, ctx, schemas, principal.ID, "editor_limits", []schema.FieldInput{{
		Name: "body", Type: schema.TypeEditor, Options: &schema.FieldOptions{MaxSize: &maximum},
	}})

	_, err := records.Create(ctx, CreateRequest{
		Principal: principal, RequestID: "editor-size-rejected", Collection: "editor_limits",
		Input: map[string]json.RawMessage{"body": json.RawMessage(`"four"`)},
	})
	var validation *validationdomain.Error
	if !errors.As(err, &validation) || len(validation.Details) != 1 || validation.Details[0].Field != "body" || validation.Details[0].Code != "value_too_long" {
		t.Fatalf("create error=%v", err)
	}
}
