package config

import (
	"fmt"

	"github.com/yakisoba0728/PgBase/pgbase"
)

var sqlConsoleEnvironment = []string{"PGBASE_SQL_CONSOLE_COMPILER_URL", "PGBASE_SQL_CONSOLE_READ_URL", "PGBASE_SQL_CONSOLE_WRITER_URL"}

// loadSQLConsole enables the read-only administrator SQL console only when all
// three dedicated role URLs are set. Write targets stay an embedding option.
func loadSQLConsole(lookup func(string) (string, bool), opts *pgbase.Options) error {
	urls := make([]string, len(sqlConsoleEnvironment))
	configured := 0
	for index, name := range sqlConsoleEnvironment {
		urls[index], _ = value(lookup, name)
		if urls[index] != "" {
			configured++
		}
	}
	if configured == 0 {
		return nil
	}
	if configured != len(urls) {
		return fmt.Errorf("PGBASE_SQL_CONSOLE_COMPILER_URL, PGBASE_SQL_CONSOLE_READ_URL, PGBASE_SQL_CONSOLE_WRITER_URL: set all three or none")
	}
	opts.SQLConsole = &pgbase.SQLConsoleOptions{CompilerURL: urls[0], ReadURL: urls[1], WriterURL: urls[2]}
	return nil
}
