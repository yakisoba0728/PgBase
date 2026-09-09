package usermigrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var validName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:_[a-z0-9]+)*$`)

// Generate writes a compilable Go migration whose exact source bytes are
// embedded into its registration. O_EXCL keeps repeated invocations harmless.
func Generate(directory, name string, now time.Time) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !validName.MatchString(name) {
		return "", errors.New("migration name must use lowercase letters, digits, and underscores")
	}
	id := now.UTC().Format("20060102150405") + "_" + name
	filename := id + ".go"
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return "", fmt.Errorf("create migration directory: %w", err)
	}
	path := filepath.Join(directory, filename)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return "", fmt.Errorf("create migration file: %w", err)
	}
	variable := "source" + now.UTC().Format("20060102150405") + "_" + name
	source := fmt.Sprintf(`package migrations

import (
	"context"
	_ "embed"

	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/pgbase"
)

//go:embed %s
var %s []byte

func init() {
	pgbase.RegisterUserMigration(pgbase.UserMigration{
		ID: %q, Source: %s,
		Up: func(ctx context.Context, tx pgx.Tx) error {
			// Write the forward migration with tx.Exec(ctx, ...).
			return nil
		},
		Down: func(ctx context.Context, tx pgx.Tx) error {
			// Reverse every change made by Up.
			return nil
		},
	})
}
`, filename, variable, id, variable)
	if _, err := file.WriteString(source); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write migration file: %w", err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close migration file: %w", err)
	}
	return path, nil
}
