package recordops

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/passwordfield"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestPreparePasswordValidatesAndHashes(t *testing.T) {
	minimum, maximum := 3, 4
	field := schema.Field{ID: uuid.New(), Name: "pin", Type: schema.TypePassword, Required: true,
		Options: schema.FieldOptions{MinLength: &minimum, MaxLength: &maximum, Pattern: `^[0-9]+$`}}
	raw, err := preparePassword(field, json.RawMessage(`"1234"`))
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if json.Unmarshal(raw, &hash) != nil || !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("stored value = %s", raw)
	}
	if err := passwordfield.Verify(hash, "1234"); err != nil {
		t.Fatalf("verify stored value: %v", err)
	}
	for _, raw := range []json.RawMessage{json.RawMessage(`""`), json.RawMessage(`null`), json.RawMessage(`"12"`), json.RawMessage(`"12345"`), json.RawMessage(`"abc"`), json.RawMessage(`7`)} {
		if _, err := preparePassword(field, raw); err == nil {
			t.Errorf("preparePassword(%s) succeeded", raw)
		}
	}
}

func TestPreparePasswordOptionalClearAndDefaultMaximum(t *testing.T) {
	field := schema.Field{Name: "secret", Type: schema.TypePassword}
	for _, raw := range []json.RawMessage{json.RawMessage(`""`), json.RawMessage(`null`)} {
		got, err := preparePassword(field, raw)
		if err != nil || string(got) != "null" {
			t.Fatalf("preparePassword(%s) = %s, %v", raw, got, err)
		}
	}
	if _, err := preparePassword(field, json.RawMessage(`"`+strings.Repeat("界", 71)+`"`)); err != nil {
		t.Fatalf("71 runes rejected: %v", err)
	}
	if _, err := preparePassword(field, json.RawMessage(`"`+strings.Repeat("界", 72)+`"`)); err == nil {
		t.Fatal("72 runes accepted")
	}
}
