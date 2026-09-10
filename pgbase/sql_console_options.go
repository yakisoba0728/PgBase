package pgbase

import (
	"context"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/sqlconsole"
)

// SQLConsoleOptions enables the administrator SQL console when attached to
// Options. The three URLs must authenticate as distinct, dedicated database
// roles. An empty WriteTargets list keeps the console read-only.
type SQLConsoleOptions struct {
	CompilerURL    string
	ReadURL        string
	WriterURL      string
	WriteTargets   []SQLConsoleWriteTarget
	DefaultTimeout time.Duration
	MaximumTimeout time.Duration
}

// SQLConsoleWriteTarget names one extension-owned base table and the exact
// columns the SQL console writer may mutate.
type SQLConsoleWriteTarget struct {
	Schema  string
	Table   string
	Columns []string
}

func (options SQLConsoleOptions) normalized(allowInsecureDatabase bool) (SQLConsoleOptions, error) {
	urls := []struct {
		name  string
		value string
	}{
		{name: "CompilerURL", value: options.CompilerURL},
		{name: "ReadURL", value: options.ReadURL},
		{name: "WriterURL", value: options.WriterURL},
	}
	roles := make(map[string]string, len(urls))
	for _, configured := range urls {
		if configured.value == "" {
			return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.%s: is required", configured.name)
		}
		if err := validateDatabaseURL(configured.value, allowInsecureDatabase); err != nil {
			return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.%s: %w", configured.name, err)
		}
		parsed, err := url.Parse(configured.value)
		if err != nil || parsed.User == nil || parsed.User.Username() == "" {
			return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.%s: must identify its dedicated database role", configured.name)
		}
		role := parsed.User.Username()
		if prior, duplicate := roles[role]; duplicate {
			return SQLConsoleOptions{}, fmt.Errorf("SQLConsole database roles must be distinct: %s and %s use the same role", prior, configured.name)
		}
		roles[role] = configured.name
	}
	if options.DefaultTimeout == 0 {
		options.DefaultTimeout = sqlconsole.DefaultTimeout
	}
	if options.MaximumTimeout == 0 {
		options.MaximumTimeout = sqlconsole.MaximumTimeout
	}
	if options.MaximumTimeout < time.Millisecond || options.MaximumTimeout > sqlconsole.MaximumTimeout {
		return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.MaximumTimeout: must be between 1ms and %s", sqlconsole.MaximumTimeout)
	}
	if options.DefaultTimeout < time.Millisecond || options.DefaultTimeout > options.MaximumTimeout {
		return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.DefaultTimeout: must be between 1ms and MaximumTimeout")
	}

	seenTargets := make(map[string]struct{}, len(options.WriteTargets))
	targets := make([]SQLConsoleWriteTarget, len(options.WriteTargets))
	for index, target := range options.WriteTargets {
		if !validSQLConsoleIdentifier(target.Schema) || !validSQLConsoleIdentifier(target.Table) {
			return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.WriteTargets[%d]: schema and table must be valid PostgreSQL identifiers", index)
		}
		schema := strings.ToLower(target.Schema)
		if schema == "app" || schema == "pgbase_meta" || schema == "information_schema" || schema == "pg_catalog" || strings.HasPrefix(schema, "pg_") {
			return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.WriteTargets[%d]: %s.%s is not an extension-owned target", index, target.Schema, target.Table)
		}
		key := target.Schema + "\x00" + target.Table
		if _, duplicate := seenTargets[key]; duplicate {
			return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.WriteTargets[%d]: duplicate target", index)
		}
		seenTargets[key] = struct{}{}
		if len(target.Columns) == 0 {
			return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.WriteTargets[%d].Columns: must be non-empty", index)
		}
		columns := append([]string(nil), target.Columns...)
		for _, column := range columns {
			if !validSQLConsoleIdentifier(column) {
				return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.WriteTargets[%d].Columns: contains an invalid PostgreSQL identifier", index)
			}
		}
		slices.Sort(columns)
		for columnIndex := 1; columnIndex < len(columns); columnIndex++ {
			if columns[columnIndex] == columns[columnIndex-1] {
				return SQLConsoleOptions{}, fmt.Errorf("SQLConsole.WriteTargets[%d].Columns: must be unique", index)
			}
		}
		targets[index] = SQLConsoleWriteTarget{Schema: target.Schema, Table: target.Table, Columns: columns}
	}
	options.WriteTargets = targets
	return options, nil
}

func validSQLConsoleIdentifier(value string) bool {
	return value != "" && len(value) <= 63 && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}

func (options SQLConsoleOptions) writeEnabled() bool { return len(options.WriteTargets) != 0 }

func (options SQLConsoleOptions) poolOptions(catalog schema.Catalog, catalogVersion func(context.Context) (int64, error), allowInsecureDatabase bool) sqlconsole.PoolOptions {
	targets := make([]sqlconsole.WriteTarget, len(options.WriteTargets))
	for index, target := range options.WriteTargets {
		targets[index] = sqlconsole.WriteTarget{Schema: target.Schema, Table: target.Table, Columns: append([]string(nil), target.Columns...)}
	}
	return sqlconsole.PoolOptions{
		CompilerURL: options.CompilerURL, ReadURL: options.ReadURL, WriterURL: options.WriterURL,
		AllowInsecureDatabase: allowInsecureDatabase, Catalog: catalog, CatalogVersion: catalogVersion, WriteTargets: targets,
	}
}

func (options SQLConsoleOptions) serviceOptions() sqlconsole.ServiceOptions {
	return sqlconsole.ServiceOptions{
		WriteEnabled: options.writeEnabled(), DefaultTimeout: options.DefaultTimeout, MaximumTimeout: options.MaximumTimeout,
	}
}

func (options SQLConsoleOptions) handlerOptions() api.SQLConsoleHandlerOptions {
	targets := make([]api.SQLConsoleWriteTarget, len(options.WriteTargets))
	for index, target := range options.WriteTargets {
		targets[index] = api.SQLConsoleWriteTarget{
			Schema: target.Schema, Table: target.Table, Columns: append([]string(nil), target.Columns...),
		}
	}
	return api.SQLConsoleHandlerOptions{
		WriteTargets: targets, DefaultTimeout: options.DefaultTimeout, MaximumTimeout: options.MaximumTimeout,
	}
}
