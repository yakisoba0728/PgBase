//go:build integration

package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/testutil"
)

func TestJavaScriptMigrationCLIUsesOneFrozenSourceForListUpHistoryAndDown(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	env := migrationEnv(databaseURL)
	var systemOutput, systemError bytes.Buffer
	if code := Execute(context.Background(), []string{"migrate", "up"}, env, testIO(nil, &systemOutput, &systemError)); code != 0 {
		t.Fatalf("system migrate up exit=%d stdout=%q stderr=%q", code, systemOutput.String(), systemError.String())
	}

	directory := t.TempDir()
	manifest := `{"format":"pgbase.scripts/v1","migrations":[{"id":"202609100020_cli_widget","module":"migration.js","up":"up","down":"down"}]}`
	source := `const pg=require("pgbase");
exports.up=function(){
 pg.migration.exec({sql:"create schema n6_cli"});
 pg.migration.exec({sql:"create table n6_cli.widgets(id bigint primary key,label text not null)"});
 pg.migration.exec({sql:"insert into n6_cli.widgets(id,label) values($1::bigint,$2)",args:[pg.exact("9007199254740993"),"cli"]});
 return {applied:true};
};
exports.down=function(){pg.migration.exec({sql:"drop schema n6_cli cascade"});return {reverted:true};};`
	for name, contents := range map[string]string{"pgbase.scripts.json": manifest, "migration.js": source} {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	run := func(arguments ...string) string {
		t.Helper()
		var stdout, stderr bytes.Buffer
		if code := Execute(context.Background(), append([]string{"migration"}, arguments...), env, testIO(nil, &stdout, &stderr)); code != 0 {
			t.Fatalf("migration %v exit=%d stdout=%q stderr=%q", arguments, code, stdout.String(), stderr.String())
		}
		return stdout.String()
	}
	if output := run("list", "--js-dir", directory); !strings.Contains(output, "pending 202609100020_cli_widget") {
		t.Fatalf("pending list = %q", output)
	}
	if output := run("up", "--js-dir", directory); !strings.Contains(output, "up 202609100020_cli_widget") {
		t.Fatalf("up output = %q", output)
	}
	if output := run("history", "--js-dir", directory); !strings.Contains(output, "applied 202609100020_cli_widget") {
		t.Fatalf("history output = %q", output)
	}

	connection, err := pgx.Connect(context.Background(), databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close(context.Background())
	var id, label string
	if err := connection.QueryRow(context.Background(), `select id::text,label from n6_cli.widgets`).Scan(&id, &label); err != nil {
		t.Fatal(err)
	}
	if id != "9007199254740993" || label != "cli" {
		t.Fatalf("migrated row = (%q,%q)", id, label)
	}
	if output := run("down", "--js-dir", directory); !strings.Contains(output, "down 202609100020_cli_widget") {
		t.Fatalf("down output = %q", output)
	}
	var exists bool
	if err := connection.QueryRow(context.Background(), `select to_regclass('n6_cli.widgets') is not null`).Scan(&exists); err != nil {
		t.Fatal(err)
	}
	if exists {
		t.Fatal("JavaScript migration Down left table behind")
	}
	if output := run("list", "--js-dir", directory); !strings.Contains(output, "pending 202609100020_cli_widget") {
		t.Fatalf("post-down list = %q", output)
	}
}
