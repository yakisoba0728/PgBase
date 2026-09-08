package backup

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const FormatVersion = 1

var versionPattern = regexp.MustCompile(`(?:PostgreSQL\)?\s+)(\d+)\.`)

type File struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type Manifest struct {
	Format          int       `json:"format"`
	CreatedAt       time.Time `json:"createdAt"`
	PostgreSQLMajor int       `json:"postgresqlMajor"`
	Database        File      `json:"database"`
	Files           []File    `json:"files"`
}

func checksum(ctx context.Context, path, name string) (result File, resultErr error) {
	f, err := os.Open(path)
	if err != nil {
		return File{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, f.Close()) }()
	h := sha256.New()
	n, err := io.Copy(h, contextReader{ctx: ctx, reader: f})
	return File{Path: name, Size: n, SHA256: hex.EncodeToString(h.Sum(nil))}, err
}

func cleanRelative(name string) (string, error) {
	if name == "" || name == "." || filepath.IsAbs(name) || strings.ContainsRune(name, '\\') {
		return "", errors.New("invalid backup path")
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || filepath.ToSlash(clean) != name {
		return "", errors.New("invalid backup path")
	}
	return clean, nil
}

func safeTree(ctx context.Context, root string) ([]File, error) {
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("file source must be a real directory")
	}
	result := []File{}
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("file tree contains a symlink")
		}
		if entry.IsDir() {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("file tree contains a non-regular file")
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		name := filepath.ToSlash(relative)
		if _, err = cleanRelative(name); err != nil {
			return err
		}
		item, err := checksum(ctx, path, name)
		if err == nil {
			result = append(result, item)
		}
		return err
	})
	sort.Slice(result, func(i, j int) bool { return result[i].Path < result[j].Path })
	return result, err
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r contextReader) Read(buffer []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}

type postgresCommand struct {
	env     []string
	cleanup func()
}

func commandEnvironment(databaseURL string) (postgresCommand, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil || (parsed.Scheme != "postgres" && parsed.Scheme != "postgresql") || parsed.User == nil || parsed.Fragment != "" {
		return postgresCommand{}, errors.New("invalid PostgreSQL URL")
	}
	identityParameters := map[string]bool{"host": true, "hostaddr": true, "port": true, "user": true, "password": true, "dbname": true, "database": true, "service": true, "servicefile": true}
	for key := range parsed.Query() {
		if identityParameters[strings.ToLower(key)] {
			return postgresCommand{}, errors.New("backup PostgreSQL URL must keep connection identity in its authority and path")
		}
	}
	host := parsed.Hostname()
	if host == "" {
		return postgresCommand{}, errors.New("invalid PostgreSQL URL")
	}
	port := 5432
	if raw := parsed.Port(); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 1 || value > 65535 {
			return postgresCommand{}, errors.New("invalid PostgreSQL URL")
		}
		port = value
	}
	database := strings.TrimPrefix(parsed.EscapedPath(), "/")
	database, err = url.PathUnescape(database)
	if err != nil || database == "" {
		return postgresCommand{}, errors.New("invalid PostgreSQL URL")
	}
	user := parsed.User.Username()
	password, _ := parsed.User.Password()
	if user == "" {
		return postgresCommand{}, errors.New("invalid PostgreSQL URL")
	}
	dir, err := os.MkdirTemp("", "pgbase-pgpass-")
	if err != nil {
		return postgresCommand{}, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }
	escape := func(value string) string { return strings.NewReplacer("\\", "\\\\", ":", "\\:").Replace(value) }
	line := strings.Join([]string{escape(host), strconv.Itoa(port), escape(database), escape(user), escape(password)}, ":") + "\n"
	passfile := filepath.Join(dir, "pass")
	if err = os.WriteFile(passfile, []byte(line), 0600); err != nil {
		cleanup()
		return postgresCommand{}, err
	}
	replacements := map[string]string{"PGHOST": host, "PGPORT": strconv.Itoa(port), "PGDATABASE": database, "PGUSER": user, "PGPASSFILE": passfile}
	if mode := parsed.Query().Get("sslmode"); mode != "" {
		replacements["PGSSLMODE"] = mode
	}
	if parsed, parseErr := url.Parse(databaseURL); parseErr == nil && parsed.Scheme != "" {
		for query, environment := range map[string]string{"sslrootcert": "PGSSLROOTCERT", "sslcert": "PGSSLCERT", "sslkey": "PGSSLKEY", "sslcrl": "PGSSLCRL"} {
			if value := parsed.Query().Get(query); value != "" {
				replacements[environment] = value
			}
		}
	}
	env := make([]string, 0, len(os.Environ())+len(replacements))
	for _, item := range os.Environ() {
		key, _, _ := strings.Cut(item, "=")
		if _, replaced := replacements[key]; !replaced && !strings.HasPrefix(key, "PG") {
			env = append(env, item)
		}
	}
	for key, value := range replacements {
		env = append(env, key+"="+value)
	}
	return postgresCommand{env: env, cleanup: cleanup}, nil
}

func toolMajor(ctx context.Context, name string) (int, error) {
	output, err := exec.CommandContext(ctx, name, "--version").Output()
	if err != nil {
		return 0, fmt.Errorf("%s unavailable: %w", name, err)
	}
	match := versionPattern.FindSubmatch(output)
	if len(match) != 2 {
		return 0, fmt.Errorf("unrecognized %s version", name)
	}
	return strconv.Atoi(string(match[1]))
}

func serverState(ctx context.Context, databaseURL string) (major int, idle bool, err error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return 0, false, err
	}
	defer func() { err = errors.Join(err, conn.Close(context.Background())) }()
	var version, others int
	err = conn.QueryRow(ctx, `select current_setting('server_version_num')::integer,(select count(*) from pg_stat_activity where datname=current_database() and pid<>pg_backend_pid() and backend_type='client backend' and application_name<>'pgbase-backup-gate')`).Scan(&version, &others)
	return version / 10000, others == 0, err
}

func databaseEmpty(ctx context.Context, databaseURL string) (empty bool, resultErr error) {
	conn, err := pgx.Connect(ctx, databaseURL)
	if err != nil {
		return false, err
	}
	defer func() { resultErr = errors.Join(resultErr, conn.Close(context.Background())) }()
	resultErr = conn.QueryRow(ctx, `select not exists(select 1 from pg_class c join pg_namespace n on n.oid=c.relnamespace where n.nspname not in ('pg_catalog','information_schema') and n.nspname not like 'pg_toast%' and c.relkind in ('r','p','v','m','S','f'))`).Scan(&empty)
	return empty, resultErr
}

func run(ctx context.Context, name string, args, env []string) error {
	command := exec.CommandContext(ctx, name, args...)
	command.Env = env
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return fmt.Errorf("%s failed: %s", name, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func Create(ctx context.Context, destination, databaseURL, files string) (resultErr error) {
	if destination == "" {
		return errors.New("backup directory is required")
	}
	if _, err := os.Lstat(destination); !os.IsNotExist(err) {
		return errors.New("backup directory already exists")
	}
	command, err := commandEnvironment(databaseURL)
	if err != nil {
		return err
	}
	defer command.cleanup()
	serverMajor, idle, err := serverState(ctx, databaseURL)
	if err != nil {
		return err
	}
	if !idle {
		return errors.New("database has active sessions")
	}
	dumpMajor, err := toolMajor(ctx, "pg_dump")
	if err != nil {
		return err
	}
	if dumpMajor != serverMajor {
		return fmt.Errorf("pg_dump major %d does not match PostgreSQL major %d", dumpMajor, serverMajor)
	}
	parent := filepath.Dir(destination)
	if err = os.MkdirAll(parent, 0700); err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(parent, ".pgbase-backup-")
	if err != nil {
		return err
	}
	complete := false
	defer func() {
		if !complete {
			resultErr = errors.Join(resultErr, os.RemoveAll(temporary))
		}
	}()
	if err = os.Mkdir(filepath.Join(temporary, "files"), 0700); err != nil {
		return err
	}
	dump := filepath.Join(temporary, "database.dump")
	if err = run(ctx, "pg_dump", []string{"--format=custom", "--no-owner", "--no-acl", "--schema=pgbase_meta", "--schema=app", "--schema=pgbase_relation", "--file", dump}, command.env); err != nil {
		return err
	}
	databaseFile, err := checksum(ctx, dump, "database.dump")
	if err != nil {
		return err
	}
	entries := []File{}
	if files != "" {
		entries, err = safeTree(ctx, files)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			target := filepath.Join(temporary, "files", filepath.FromSlash(entry.Path))
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			if err = copyRegular(ctx, filepath.Join(files, filepath.FromSlash(entry.Path)), target); err != nil {
				return err
			}
		}
	}
	manifest := Manifest{Format: FormatVersion, CreatedAt: time.Now().UTC(), PostgreSQLMajor: serverMajor, Database: databaseFile, Files: entries}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(temporary, "manifest.json"), append(encoded, '\n'), 0600); err != nil {
		return err
	}
	if _, err = Verify(temporary); err != nil {
		return err
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = os.Rename(temporary, destination); err != nil {
		return err
	}
	complete = true
	return nil
}

func copyRegular(ctx context.Context, source, target string) (resultErr error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, in.Close()) }()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, contextReader{ctx: ctx, reader: in})
	return errors.Join(copyErr, out.Close())
}

func Verify(directory string) (Manifest, error) {
	var manifest Manifest
	root, err := os.Lstat(directory)
	if err != nil || !root.IsDir() || root.Mode()&os.ModeSymlink != 0 {
		return manifest, errors.New("backup must be a real directory")
	}
	manifestPath := filepath.Join(directory, "manifest.json")
	manifestInfo, err := os.Lstat(manifestPath)
	if err != nil || !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return manifest, errors.New("missing or unsafe backup manifest")
	}
	encoded, err := os.ReadFile(manifestPath)
	if err != nil {
		return manifest, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&manifest); err != nil || manifest.Format != FormatVersion || manifest.PostgreSQLMajor < 10 || manifest.Database.Path != "database.dump" {
		return Manifest{}, errors.New("invalid backup manifest")
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return Manifest{}, errors.New("invalid backup manifest")
	}
	seen := make(map[string]bool)
	all := append([]File{manifest.Database}, manifest.Files...)
	for index, entry := range all {
		clean, err := cleanRelative(entry.Path)
		digest, digestErr := hex.DecodeString(entry.SHA256)
		if err != nil || entry.Size < 0 || digestErr != nil || len(digest) != sha256.Size {
			return Manifest{}, errors.New("invalid backup manifest")
		}
		if index > 0 {
			if seen[entry.Path] {
				return Manifest{}, errors.New("duplicate backup path")
			}
			seen[entry.Path] = true
		}
		path := filepath.Join(directory, clean)
		if index > 0 {
			path = filepath.Join(directory, "files", clean)
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			return Manifest{}, errors.New("missing or unsafe backup artifact")
		}
		actual, err := checksum(context.Background(), path, entry.Path)
		if err != nil || actual.Size != entry.Size || actual.SHA256 != entry.SHA256 {
			return Manifest{}, errors.New("backup artifact hash mismatch")
		}
	}
	actualFiles, err := safeTree(context.Background(), filepath.Join(directory, "files"))
	if err != nil || len(actualFiles) != len(manifest.Files) {
		return Manifest{}, errors.New("backup file inventory mismatch")
	}
	for index := range actualFiles {
		if actualFiles[index] != manifest.Files[index] {
			return Manifest{}, errors.New("backup file inventory mismatch")
		}
	}
	return manifest, nil
}

func Restore(ctx context.Context, directory, databaseURL, files string) (resultErr error) {
	manifest, err := Verify(directory)
	if err != nil {
		return err
	}
	command, err := commandEnvironment(databaseURL)
	if err != nil {
		return err
	}
	defer command.cleanup()
	serverMajor, idle, err := serverState(ctx, databaseURL)
	if err != nil {
		return err
	}
	if !idle {
		return errors.New("database has active sessions")
	}
	restoreMajor, err := toolMajor(ctx, "pg_restore")
	if err != nil {
		return err
	}
	if restoreMajor != serverMajor || manifest.PostgreSQLMajor != serverMajor {
		return errors.New("backup, pg_restore, and PostgreSQL major versions must match")
	}
	empty, err := databaseEmpty(ctx, databaseURL)
	if err != nil {
		return err
	}
	if !empty {
		return errors.New("restore target database is not empty")
	}
	if files == "" && len(manifest.Files) != 0 {
		return errors.New("backup contains local files but local file storage is not configured")
	}
	var staging string
	installedFiles := false
	databaseCommitted := false
	defer func() {
		if installedFiles && !databaseCommitted {
			resultErr = errors.Join(resultErr, os.RemoveAll(files))
		}
	}()
	if files != "" {
		if _, err = os.Lstat(files); !os.IsNotExist(err) {
			return errors.New("restore file directory already exists")
		}
		parent := filepath.Dir(files)
		if err = os.MkdirAll(parent, 0700); err != nil {
			return err
		}
		staging, err = os.MkdirTemp(parent, ".pgbase-files-")
		if err != nil {
			return err
		}
		defer func() {
			if staging != "" {
				resultErr = errors.Join(resultErr, os.RemoveAll(staging))
			}
		}()
		for _, entry := range manifest.Files {
			target := filepath.Join(staging, filepath.FromSlash(entry.Path))
			if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
				return err
			}
			if err = copyRegular(ctx, filepath.Join(directory, "files", filepath.FromSlash(entry.Path)), target); err != nil {
				return err
			}
		}
		if err = os.Rename(staging, files); err != nil {
			return err
		}
		staging = ""
		installedFiles = true
	}
	if err = run(ctx, "pg_restore", []string{"--dbname", "", "--single-transaction", "--no-owner", "--no-acl", "--exit-on-error", filepath.Join(directory, "database.dump")}, command.env); err != nil {
		return err
	}
	databaseCommitted = true
	return nil
}
