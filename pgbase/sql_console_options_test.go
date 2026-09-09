package pgbase

import (
	"strings"
	"testing"
	"time"
)

func TestSQLConsoleOptionsNormalizeReadOnlyDefaultsAndOwnCopies(t *testing.T) {
	input := SQLConsoleOptions{
		CompilerURL: "postgres://compiler:secret@127.0.0.1:5432/app?sslmode=disable",
		ReadURL:     "postgres://reader:secret@127.0.0.1:5432/app?sslmode=disable",
		WriterURL:   "postgres://writer:secret@127.0.0.1:5432/app?sslmode=disable",
		WriteTargets: []SQLConsoleWriteTarget{{
			Schema: "extension", Table: "counters", Columns: []string{"value", "id"},
		}},
	}
	normalized, err := input.normalized(true)
	if err != nil {
		t.Fatal(err)
	}
	if normalized.DefaultTimeout != 5*time.Second || normalized.MaximumTimeout != 30*time.Second {
		t.Fatalf("timeouts = %s/%s", normalized.DefaultTimeout, normalized.MaximumTimeout)
	}
	if normalized.WriteTargets[0].Columns[0] != "id" || normalized.WriteTargets[0].Columns[1] != "value" {
		t.Fatalf("columns = %#v", normalized.WriteTargets[0].Columns)
	}
	input.WriteTargets[0].Columns[0] = "changed"
	if normalized.WriteTargets[0].Columns[1] != "value" {
		t.Fatal("normalization retained caller-owned target storage")
	}
	if !normalized.writeEnabled() {
		t.Fatal("configured target did not enable write mode")
	}
	service := normalized.serviceOptions()
	if !service.WriteEnabled || service.DefaultTimeout != 5*time.Second || service.MaximumTimeout != 30*time.Second {
		t.Fatalf("service options = %#v", service)
	}

	readOnly := normalized
	readOnly.WriteTargets = nil
	if readOnly.writeEnabled() {
		t.Fatal("an empty target list enabled writes")
	}
}

func TestSQLConsoleOptionsRejectUnsafeOrAmbiguousConfiguration(t *testing.T) {
	base := SQLConsoleOptions{
		CompilerURL: "postgres://compiler:secret@127.0.0.1:5432/app?sslmode=disable",
		ReadURL:     "postgres://reader:secret@127.0.0.1:5432/app?sslmode=disable",
		WriterURL:   "postgres://writer:secret@127.0.0.1:5432/app?sslmode=disable",
	}
	tests := []struct {
		name   string
		mutate func(*SQLConsoleOptions)
		want   string
	}{
		{name: "missing role URL", mutate: func(value *SQLConsoleOptions) { value.WriterURL = "" }, want: "WriterURL"},
		{name: "same role", mutate: func(value *SQLConsoleOptions) { value.WriterURL = value.ReadURL }, want: "distinct"},
		{name: "remote insecure", mutate: func(value *SQLConsoleOptions) {
			value.ReadURL = "postgres://reader:secret@db.example/app?sslmode=disable"
		}, want: "verify-full"},
		{name: "managed target", mutate: func(value *SQLConsoleOptions) {
			value.WriteTargets = []SQLConsoleWriteTarget{{Schema: "app", Table: "accounts", Columns: []string{"name"}}}
		}, want: "extension-owned"},
		{name: "duplicate target", mutate: func(value *SQLConsoleOptions) {
			target := SQLConsoleWriteTarget{Schema: "ext", Table: "items", Columns: []string{"id"}}
			value.WriteTargets = []SQLConsoleWriteTarget{target, target}
		}, want: "duplicate"},
		{name: "duplicate column", mutate: func(value *SQLConsoleOptions) {
			value.WriteTargets = []SQLConsoleWriteTarget{{Schema: "ext", Table: "items", Columns: []string{"id", "id"}}}
		}, want: "unique"},
		{name: "timeout inversion", mutate: func(value *SQLConsoleOptions) {
			value.DefaultTimeout, value.MaximumTimeout = 10*time.Second, time.Second
		}, want: "DefaultTimeout"},
		{name: "hard timeout expansion", mutate: func(value *SQLConsoleOptions) { value.MaximumTimeout = 31 * time.Second }, want: "30s"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			value := base
			test.mutate(&value)
			_, err := value.normalized(test.name != "remote insecure")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
		})
	}
}
