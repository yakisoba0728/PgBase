//go:build integration

package pgbasecli_test

import (
	"context"
	"encoding/base64"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/internal/usermigrate"
)

func TestExternalModuleCanRunCompiledMigrationCLI(t *testing.T) {
	ctx := context.Background()
	databaseURL := testutil.NewDatabase(t)
	pool, err := database.Open(ctx, databaseURL, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		pool.Close()
		t.Fatal(err)
	}
	pool.Close()

	directory := t.TempDir()
	migrationDirectory := filepath.Join(directory, "migrations")
	if _, err := usermigrate.Generate(migrationDirectory, "external_command", time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)); err != nil {
		t.Fatal(err)
	}
	repository, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{
		"go.mod": "module external.test\n\ngo 1.26.0\n\nrequire github.com/yakisoba0728/PgBase v0.0.0\nreplace github.com/yakisoba0728/PgBase => " + repository + "\n",
		"main.go": `package main
import (
 "context"
 "os"
 _ "external.test/migrations"
 "github.com/yakisoba0728/PgBase/pgbasecli"
)
func main() { os.Exit(pgbasecli.Execute(context.Background(), os.Args[1:])) }
`,
	}
	for name, contents := range files {
		if err := os.WriteFile(filepath.Join(directory, name), []byte(contents), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "run", "-mod=mod", ".", "migration", "list")
	command.Dir = directory
	command.Env = append(os.Environ(),
		"GOTOOLCHAIN=go1.26.8",
		"PGBASE_DATABASE_URL="+databaseURL,
		"PGBASE_TOKEN_SECRET="+base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))),
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("external migration CLI failed: %v\n%s", err, output)
	}
	if !strings.Contains(string(output), "pending 20260909120000_external_command") {
		t.Fatalf("external migration CLI output=%q", output)
	}
}
