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
