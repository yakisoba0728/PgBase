package query

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

// Expected macro values are from PocketBase v0.40.3
// tools/search/identifier_macros_test.go. PocketBase timestamps have millisecond
// precision, including the inclusive .999 end of day.
func TestPocketBaseTimeMacroUTCPrecision(t *testing.T) {
	now := time.Date(2023, 2, 3, 4, 5, 6, 0, time.FixedZone("east", 9*60*60))
	tests := map[string]time.Time{
		"@now":        time.Date(2023, 2, 2, 19, 5, 6, 0, time.UTC),
		"@todayStart": time.Date(2023, 2, 2, 0, 0, 0, 0, time.UTC),
		"@todayEnd":   time.Date(2023, 2, 2, 23, 59, 59, 999*time.Millisecond, time.UTC),
	}
	for macro, want := range tests {
		got, ok := filterTimeMacro(macro, now)
		parsed, err := time.Parse(time.RFC3339Nano, got.text)
		if !ok || err != nil || !parsed.Equal(want) {
			t.Errorf("%s=%q ok=%v err=%v want=%s", macro, got.text, ok, err, want.Format(time.RFC3339Nano))
		}
	}
}

// These invalid forms mirror the argument/type boundaries in PocketBase
// v0.40.3 tools/search/token_functions_test.go. Parsing and typed compilation
// may reject at different stages, but no form may become an executable rule.
func TestPocketBaseRuleRejectsInvalidFunctionsAndModifiers(t *testing.T) {
	collection := schema.Collection{ID: uuid.New(), Name: "checks", TableName: "checks", Fields: []schema.Field{
		{Name: "title", ColumnName: "title", Type: schema.TypeText},
		{Name: "occurred", ColumnName: "occurred", Type: schema.TypeTimestampTZ},
		{Name: "place", ColumnName: "place", Type: schema.TypeGeoPoint},
	}}
	resolver, err := accessrule.NewCatalogResolver(schema.Catalog{Collections: map[string]schema.Collection{"checks": collection}}, collection, "checks")
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{
		`geoDistance(place.lon, place.lat, 127) < 1`,
		`geoDistance(place.lon, place.lat, 127, 'north') < 1`,
		`strftime() = 'x'`,
		`strftime(2024, occurred) = 'x'`,
		`strftime('%Y', occurred, unknownField) = 'x'`,
		`title:unknown = 'x'`,
		`title:lower:each = 'x'`,
		`@request.body.title:changed:lower = true`,
	} {
		rule, err := ParsePocketBaseAccessRule(source)
		if err == nil {
			_, _, err = accessrule.CompilePocketBase(rule, accessrule.PBCompileContext{Resolver: resolver, Principal: identity.Subject{}})
		}
		if err == nil {
			t.Errorf("accepted invalid PocketBase rule %q", source)
		}
	}
}
