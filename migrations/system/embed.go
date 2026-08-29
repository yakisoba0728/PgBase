// Package system provides the embedded pgBase system schema migrations.
package system

import (
	"crypto/sha256"
	_ "embed"
)

// Migration is one ordered, embedded system schema change.
type Migration struct {
	Version int64
	Name    string
	SQL     string
	SHA256  [32]byte
}

//go:embed 000001_initial.sql
var initialSQL string

//go:embed 000002_record_page_indexes.sql
var recordPageIndexesSQL string

//go:embed 000003_collection_update_delete.sql
var collectionUpdateDeleteSQL string

var migrations = []Migration{
	{
		Version: 1,
		Name:    "initial",
		SQL:     initialSQL,
		SHA256:  sha256.Sum256([]byte(initialSQL)),
	},
	{
		Version: 2,
		Name:    "record_page_indexes",
		SQL:     recordPageIndexesSQL,
		SHA256:  sha256.Sum256([]byte(recordPageIndexesSQL)),
	},
	{
		Version: 3,
		Name:    "collection_update_delete",
		SQL:     collectionUpdateDeleteSQL,
		SHA256:  sha256.Sum256([]byte(collectionUpdateDeleteSQL)),
	},
}

// Migrations returns the ordered system migrations. The returned slice is a
// copy, so callers cannot change the embedded migration ordering or contents.
func Migrations() []Migration {
	return append([]Migration(nil), migrations...)
}
