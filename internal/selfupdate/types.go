// Package selfupdate implements the signed, local-file updater used by the
// PgBase CLI. It owns no database state and never starts or stops a server.
package selfupdate

import (
	"errors"
	"fmt"
)

var (
	ErrInvalidInput        = errors.New("invalid update input")
	ErrUntrustedManifest   = errors.New("untrusted manifest")
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	ErrNoUpdate            = errors.New("no update available")
	ErrDowngradeRefused    = errors.New("downgrade refused")
	ErrDownloadInterrupted = errors.New("download interrupted")
	ErrDownloadLimit       = errors.New("download limit exceeded")
	ErrReplaceBusy         = errors.New("update replacement is busy")
	ErrReplaceFailed       = errors.New("update replacement failed")
	ErrProbeFailed         = errors.New("update probe failed")
	ErrRollbackFailed      = errors.New("update rollback failed")
	ErrRecoveryRequired    = errors.New("update recovery required")
	ErrMigrationRequired   = errors.New("update migration required")
)

// ErrorCode is stable for JSON CLI consumers. Human diagnostics must not
// expose raw paths, source URLs, credentials, or key material.
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrInvalidInput):
		return "invalid_input"
	case errors.Is(err, ErrUntrustedManifest):
		return "untrusted_manifest"
	case errors.Is(err, ErrUnsupportedPlatform):
		return "unsupported_platform"
	case errors.Is(err, ErrNoUpdate):
		return "no_update"
	case errors.Is(err, ErrDowngradeRefused):
		return "downgrade_refused"
	case errors.Is(err, ErrDownloadInterrupted):
		return "download_interrupted"
	case errors.Is(err, ErrDownloadLimit):
		return "download_limit"
	case errors.Is(err, ErrReplaceBusy):
		return "replace_busy"
	case errors.Is(err, ErrReplaceFailed):
		return "replace_failed"
	case errors.Is(err, ErrProbeFailed):
		return "probe_failed"
	case errors.Is(err, ErrRollbackFailed):
		return "rollback_failed"
	case errors.Is(err, ErrRecoveryRequired):
		return "recovery_required"
	case errors.Is(err, ErrMigrationRequired):
		return "migration_required"
	default:
		return "update_failed"
	}
}

// Build identifies one executable without opening a database.
type Build struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuiltAt   string `json:"builtAt,omitempty"`
	GOOS      string `json:"goos"`
	GOARCH    string `json:"goarch"`
	SchemaMin string `json:"schemaMin,omitempty"`
	SchemaMax string `json:"schemaMax,omitempty"`
}

// WindowsHelperRequest contains only local canonical paths and the staged
// basename. It never transports a manifest, trust key, or artifact URL.
type WindowsHelperRequest struct {
	ParentPID uint32
	StateDir  string
	Target    string
	Candidate string
}

func (b Build) String() string { return fmt.Sprintf("%s %s/%s", b.Version, b.GOOS, b.GOARCH) }

// Manifest is the exact JSON payload authenticated by its detached signature.
type Manifest struct {
	SchemaVersion int       `json:"schemaVersion"`
	Channel       string    `json:"channel"`
	Releases      []Release `json:"releases"`
}

type Release struct {
	Version           string     `json:"version"`
	Commit            string     `json:"commit"`
	MinUpdaterVersion string     `json:"minUpdaterVersion"`
	Schema            Schema     `json:"schema"`
	Artifacts         []Artifact `json:"artifacts"`
}

type Schema struct {
	Min int64 `json:"min"`
	Max int64 `json:"max"`
}

type Artifact struct {
	GOOS   string `json:"goos"`
	GOARCH string `json:"goarch"`
	URL    string `json:"url"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Candidate is a verified manifest choice before its artifact is downloaded.
type Candidate struct {
	Release  Release
	Artifact Artifact
}
