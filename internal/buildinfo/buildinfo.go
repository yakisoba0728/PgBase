// Package buildinfo exposes immutable metadata embedded in a PgBase binary.
package buildinfo

import (
	"runtime"

	"github.com/yakisoba0728/PgBase/pgbase"
)

// The release build injects these values with -ldflags. Source builds retain
// their intentionally non-release values and cannot silently select "latest".
var (
	Commit    = "unknown"
	BuiltAt   = "unknown"
	SchemaMin = "0"
	SchemaMax = "0"
)

// Info is the stable record emitted by `pgbase version --json`.
type Info struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"builtAt"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	SchemaMin string `json:"schemaMin"`
	SchemaMax string `json:"schemaMax"`
}

// Current returns metadata for the currently running executable.
func Current() Info {
	return Info{
		Version: pgbase.Version, Commit: Commit, BuiltAt: BuiltAt,
		GOOS: runtime.GOOS, GOARCH: runtime.GOARCH,
		SchemaMin: SchemaMin, SchemaMax: SchemaMax,
	}
}
