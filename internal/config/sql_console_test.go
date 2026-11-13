package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestLoadEnvSQLConsoleIsOffByDefaultAndNeedsAllThreeRoles(t *testing.T) {
	env := map[string]string{
		"PGBASE_DATABASE_URL": "postgres://postgres:postgres@localhost/pgbase",
		"PGBASE_TOKEN_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 32)),
	}
	lookup := func(key string) (string, bool) { value, ok := env[key]; return value, ok }
	options, err := LoadEnv(lookup)
	if err != nil || options.SQLConsole != nil {
		t.Fatalf("default SQL console = %#v, %v", options.SQLConsole, err)
	}
	env["PGBASE_SQL_CONSOLE_COMPILER_URL"] = "postgres://compiler@localhost/pgbase"
	env["PGBASE_SQL_CONSOLE_READ_URL"] = "postgres://reader@localhost/pgbase"
	if _, err := LoadEnv(lookup); err == nil || !strings.Contains(err.Error(), "set all three or none") {
		t.Fatalf("partial SQL console error = %v", err)
	}
	env["PGBASE_SQL_CONSOLE_WRITER_URL"] = "postgres://writer@localhost/pgbase"
	options, err = LoadEnv(lookup)
	if err != nil || options.SQLConsole == nil || options.SQLConsole.ReadURL != env["PGBASE_SQL_CONSOLE_READ_URL"] || len(options.SQLConsole.WriteTargets) != 0 {
		t.Fatalf("SQL console = %#v, %v", options.SQLConsole, err)
	}
}
