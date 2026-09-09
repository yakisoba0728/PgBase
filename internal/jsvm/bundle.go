// Package jsvm loads and executes trusted, local PgBase JavaScript extensions.
// It confines module loading to one immutable source snapshot; it is not an
// operating-system sandbox for hostile code.
package jsvm

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

const (
	ManifestFormat      = "pgbase.scripts/v1"
	maximumManifestSize = 1 << 20
	maximumModuleSize   = 1 << 20
	maximumBundleSize   = 8 << 20
	maximumSourceFiles  = 128
)

type HandlerRef struct {
	Module string `json:"module"`
	Export string `json:"export"`
}

type Route struct {
	Method               string     `json:"method"`
	Pattern              string     `json:"pattern"`
	Access               string     `json:"access"`
	Handler              HandlerRef `json:"handler"`
	Capabilities         []string   `json:"capabilities,omitempty"`
	MaximumRequestBytes  int64      `json:"maximumRequestBytes,omitempty"`
	MaximumResponseBytes int        `json:"maximumResponseBytes,omitempty"`
}

type Hook struct {
	Event        string      `json:"event"`
	Priority     int         `json:"priority,omitempty"`
	Before       *HandlerRef `json:"before,omitempty"`
	After        *HandlerRef `json:"after,omitempty"`
	Capabilities []string    `json:"capabilities,omitempty"`
}

type Job struct {
	Name         string     `json:"name"`
	Schedule     string     `json:"schedule"`
	Timezone     string     `json:"timezone,omitempty"`
	Handler      HandlerRef `json:"handler"`
	Capabilities []string   `json:"capabilities,omitempty"`
}

type Migration struct {
	ID                 string          `json:"id"`
	Module             string          `json:"module"`
	Up                 string          `json:"up"`
	Down               string          `json:"down,omitempty"`
	Dependencies       []string        `json:"dependencies,omitempty"`
	BeforeSnapshot     json.RawMessage `json:"beforeSnapshot,omitempty"`
	AfterSnapshot      json.RawMessage `json:"afterSnapshot,omitempty"`
	IrreversibleReason string          `json:"irreversibleReason,omitempty"`
}

type Manifest struct {
	Format     string      `json:"format"`
	Routes     []Route     `json:"routes,omitempty"`
	Hooks      []Hook      `json:"hooks,omitempty"`
	Jobs       []Job       `json:"jobs,omitempty"`
	Migrations []Migration `json:"migrations,omitempty"`
}

type Bundle struct {
	root     string
	manifest Manifest
	sources  map[string][]byte
	digest   string
}

type migrationSourceFile struct {
	Path   string `json:"path"`
	Length int    `json:"length"`
	SHA256 string `json:"sha256"`
	Source []byte `json:"source"`
}

type migrationSourceEnvelope struct {
	Format    string                `json:"format"`
	Migration Migration             `json:"migration"`
	Sources   []migrationSourceFile `json:"sources"`
}

// MigrationBundle freezes one migration and only its declared module graph.
// The returned envelope is the checksum authority stored by UserMigration.
func (b *Bundle) MigrationBundle(id string) (*Bundle, []byte, error) {
	if b == nil {
		return nil, nil, errors.New("JavaScript bundle is nil")
	}
	var migration Migration
	found := false
	for _, candidate := range b.manifest.Migrations {
		if candidate.ID == id {
			migration, found = candidate, true
			break
		}
	}
	if !found {
		return nil, nil, fmt.Errorf("JavaScript migration %q is not in the manifest", id)
	}
	names := append([]string{migration.Module}, migration.Dependencies...)
	for index, name := range names {
		canonical, err := b.canonicalModule(name)
		if err != nil {
			return nil, nil, fmt.Errorf("JavaScript migration %q source: %w", id, err)
		}
		names[index] = canonical
	}
	slices.Sort(names)
	names = slices.Compact(names)
	sources := make(map[string][]byte, len(names))
	files := make([]migrationSourceFile, 0, len(names))
	for _, name := range names {
		source := append([]byte(nil), b.sources[name]...)
		digest := sha256.Sum256(source)
		sources[name] = source
		files = append(files, migrationSourceFile{Path: name, Length: len(source), SHA256: hex.EncodeToString(digest[:]), Source: source})
	}
	envelope, err := json.Marshal(migrationSourceEnvelope{Format: "pgbase.js-migration-source/v1", Migration: migration, Sources: files})
	if err != nil {
		return nil, nil, err
	}
	manifest := Manifest{Format: ManifestFormat, Migrations: []Migration{migration}}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return nil, nil, err
	}
	result := &Bundle{root: b.root, manifest: manifest, sources: sources}
	if err := result.validateReferences(); err != nil {
		return nil, nil, err
	}
	result.digest = bundleDigest(manifestBytes, sources)
	return result, envelope, nil
}

func LoadBundle(directory string) (*Bundle, error) {
	if directory == "" || !filepath.IsAbs(directory) || strings.IndexByte(directory, 0) >= 0 {
		return nil, errors.New("JavaScript directory must be an absolute path")
	}
	root, err := filepath.Clean(directory), error(nil)
	info, err := os.Lstat(root)
	if err != nil {
		return nil, fmt.Errorf("inspect JavaScript directory: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return nil, errors.New("JavaScript directory must be a real directory, not a symlink")
	}
	manifestBytes, err := readRegularFile(filepath.Join(root, "pgbase.scripts.json"), maximumManifestSize)
	if err != nil {
		return nil, fmt.Errorf("read JavaScript manifest: %w", err)
	}
	var manifest Manifest
	decoder := json.NewDecoder(bytes.NewReader(manifestBytes))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return nil, fmt.Errorf("decode JavaScript manifest: %w", err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return nil, fmt.Errorf("decode JavaScript manifest: %w", err)
	}
	if manifest.Format != ManifestFormat {
		return nil, fmt.Errorf("JavaScript manifest format must be %q", ManifestFormat)
	}

	sources := make(map[string][]byte)
	total := 0
	err = filepath.WalkDir(root, func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if filename == root {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("JavaScript source cannot be a symlink: %s", filepath.Base(filename))
		}
		if entry.IsDir() {
			return nil
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("JavaScript source must be a regular file: %s", filepath.Base(filename))
		}
		relative, err := filepath.Rel(root, filename)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if name == "pgbase.scripts.json" || (!strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".json")) {
			return nil
		}
		if len(sources) >= maximumSourceFiles {
			return fmt.Errorf("JavaScript bundle contains more than %d source files", maximumSourceFiles)
		}
		contents, err := readRegularFile(filename, maximumModuleSize)
		if err != nil {
			return fmt.Errorf("read JavaScript source %s: %w", name, err)
		}
		total += len(contents)
		if total > maximumBundleSize {
			return fmt.Errorf("JavaScript bundle exceeds %d bytes", maximumBundleSize)
		}
		sources[name] = contents
		return nil
	})
	if err != nil {
		return nil, err
	}
	bundle := &Bundle{root: root, manifest: manifest, sources: sources}
	if err := bundle.validateReferences(); err != nil {
		return nil, err
	}
	bundle.digest = bundleDigest(manifestBytes, sources)
	return bundle, nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("unexpected trailing JSON value")
		}
		return err
	}
	return nil
}

func readRegularFile(filename string, maximum int64) ([]byte, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maximum {
		return nil, errors.New("file is not regular or exceeds its size limit")
	}
	contents, err := io.ReadAll(io.LimitReader(file, maximum+1))
	if err != nil {
		return nil, err
	}
	if int64(len(contents)) > maximum {
		return nil, errors.New("file exceeds its size limit")
	}
	return contents, nil
}

func (b *Bundle) validateReferences() error {
	refs := make([]HandlerRef, 0, len(b.manifest.Routes)+len(b.manifest.Jobs)+len(b.manifest.Hooks)*2)
	for _, route := range b.manifest.Routes {
		refs = append(refs, route.Handler)
	}
	for _, hook := range b.manifest.Hooks {
		if hook.Before != nil {
			refs = append(refs, *hook.Before)
		}
		if hook.After != nil {
			refs = append(refs, *hook.After)
		}
		if hook.Before == nil && hook.After == nil {
			return fmt.Errorf("JavaScript hook %q has no before or after handler", hook.Event)
		}
	}
	for _, job := range b.manifest.Jobs {
		refs = append(refs, job.Handler)
	}
	for _, migration := range b.manifest.Migrations {
		refs = append(refs, HandlerRef{Module: migration.Module, Export: migration.Up})
		if migration.Down != "" {
			refs = append(refs, HandlerRef{Module: migration.Module, Export: migration.Down})
		} else if migration.IrreversibleReason == "" {
			return fmt.Errorf("JavaScript migration %q needs down or irreversibleReason", migration.ID)
		}
		for _, dependency := range migration.Dependencies {
			if _, err := b.canonicalModule(dependency); err != nil {
				return fmt.Errorf("JavaScript migration %q dependency: %w", migration.ID, err)
			}
		}
	}
	for _, ref := range refs {
		if ref.Export == "" || len(ref.Export) > 128 || strings.ContainsAny(ref.Export, "\x00\r\n") {
			return errors.New("JavaScript handler export is invalid")
		}
		if _, err := b.canonicalModule(ref.Module); err != nil {
			return fmt.Errorf("JavaScript handler module %q: %w", ref.Module, err)
		}
	}
	return nil
}

func (b *Bundle) canonicalModule(name string) (string, error) {
	if name == "" || strings.Contains(name, "\\") || strings.IndexByte(name, 0) >= 0 || path.IsAbs(name) || path.Clean(name) != name || name == "." || name == ".." || strings.HasPrefix(name, "../") {
		return "", errors.New("module path must be a canonical relative path")
	}
	if _, ok := b.sources[name]; ok {
		return name, nil
	}
	if !strings.HasSuffix(name, ".js") && !strings.HasSuffix(name, ".json") {
		for _, suffix := range []string{".js", ".json"} {
			if _, ok := b.sources[name+suffix]; ok {
				return name + suffix, nil
			}
		}
	}
	return "", errors.New("module is not in the immutable source snapshot")
}

func bundleDigest(manifest []byte, sources map[string][]byte) string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)
	hash := sha256.New()
	hash.Write([]byte("pgbase-js-bundle-v1\x00"))
	hash.Write(manifest)
	for _, name := range names {
		fmt.Fprintf(hash, "\x00%d:%s\x00%d:", len(name), name, len(sources[name]))
		hash.Write(sources[name])
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func (b *Bundle) Manifest() Manifest {
	manifest := b.manifest
	manifest.Routes = append([]Route(nil), manifest.Routes...)
	manifest.Hooks = append([]Hook(nil), manifest.Hooks...)
	manifest.Jobs = append([]Job(nil), manifest.Jobs...)
	manifest.Migrations = append([]Migration(nil), manifest.Migrations...)
	return manifest
}

func (b *Bundle) Digest() string {
	if b == nil {
		return ""
	}
	return b.digest
}

func (b *Bundle) Source(name string) []byte {
	if b == nil {
		return nil
	}
	canonical, err := b.canonicalModule(name)
	if err != nil {
		return nil
	}
	return append([]byte(nil), b.sources[canonical]...)
}
