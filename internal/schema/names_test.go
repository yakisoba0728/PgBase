package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestNameAcceptsValidatedNames(t *testing.T) {
	for _, name := range []string{"posts", "post_comments2"} {
		if err := validateName("name", name); err != nil {
			t.Errorf("validateName(%q) error = %v, want nil", name, err)
		}
	}
}

func TestNameRejectsInvalidNames(t *testing.T) {
	cases := []struct {
		name string
		code string
	}{
		{name: "Posts", code: "invalid_name"},
		{name: "post comments", code: "invalid_name"},
		{name: `post"comments`, code: "invalid_name"},
		{name: strings.Repeat("a", 49), code: "invalid_name"},
		{name: "id", code: "reserved_name"},
		{name: "created_at", code: "reserved_name"},
		{name: "updated_at", code: "reserved_name"},
		{name: "ctid", code: "reserved_name"},
		{name: "xmin", code: "reserved_name"},
		{name: "xmax", code: "reserved_name"},
		{name: "cmin", code: "reserved_name"},
		{name: "cmax", code: "reserved_name"},
		{name: "tableoid", code: "reserved_name"},
		{name: "true", code: "reserved_name"},
		{name: "false", code: "reserved_name"},
		{name: "null", code: "reserved_name"},
		{name: "pgbase_internal", code: "reserved_name"},
	}

	for _, tc := range cases {
		err := validateName("fields.2.name", tc.name)
		var validation *ValidationError
		if !errors.As(err, &validation) || len(validation.Details) != 1 {
			t.Errorf("validateName(%q) error = %v, want one validation detail", tc.name, err)
			continue
		}
		if got := validation.Details[0]; got.Field != "fields.2.name" || got.Code != tc.code {
			t.Errorf("validateName(%q) detail = %#v, want fields.2.name -> %s", tc.name, got, tc.code)
		}
	}
}

// TestCatalogNameValidationToleratesInputOnlyReservations pins the split
// between the forward-only naming policy and catalog integrity. validateName
// runs inside validateCatalogIntegrity on every EnsureCurrent, so promoting an
// input-only reservation into reservedNames would make a database that already
// holds the name fail every read for every collection, not just the one that
// carries it.
func TestCatalogNameValidationToleratesInputOnlyReservations(t *testing.T) {
	for name := range reservedInputNames {
		if _, hard := reservedNames[name]; hard {
			t.Errorf("%q is reserved for input and for catalogs; catalogs written before the reservation would be unreadable", name)
		}
		if err := validateName("fields.2.name", name); err != nil {
			t.Errorf("validateName(%q) error = %v, want nil so legacy catalogs stay readable", name, err)
		}
		if err := validateInputName("fields.2.name", name); err == nil {
			t.Errorf("validateInputName(%q) error = nil, want the name closed to new schemas", name)
		}
	}
}

func TestInputNameRejectsExpandEnvelopeKey(t *testing.T) {
	err := validateInputName("fields.2.name", "expand")
	var validation *ValidationError
	if !errors.As(err, &validation) || len(validation.Details) != 1 {
		t.Fatalf("validateInputName(\"expand\") error = %v, want one validation detail", err)
	}
	if got := validation.Details[0]; got.Field != "fields.2.name" || got.Code != "reserved_name" {
		t.Fatalf("validateInputName(\"expand\") detail = %#v, want fields.2.name -> reserved_name", got)
	}
}
