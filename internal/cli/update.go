package cli

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/webisnoteasy/PgBase/internal/buildinfo"
	"github.com/webisnoteasy/PgBase/internal/selfupdate"
)

const updateUsage = "usage: pgbase update <check|apply|status|rollback> ..."
const updateJSONSchemaVersion = 1

// updateOperationalError preserves only the stable updater code for the
// shared executor; its Error string cannot leak paths, URLs, or key bytes.
type updateOperationalError struct{ cause error }

func (e updateOperationalError) Error() string { return selfupdate.ErrorCode(e.cause) }
func (e updateOperationalError) Unwrap() error { return e.cause }

func updateFailure(err error) error {
	if err == nil {
		return nil
	}
	return updateOperationalError{cause: err}
}

// runUpdate is private until integration composes it into commandTable. It
// does not read PgBase environment settings or contact PostgreSQL.
func runUpdate(ctx context.Context, args []string, _ environment, streams IO) error {
	if len(args) == 0 {
		return configurationError{err: errors.New("invalid update usage"), message: updateUsage}
	}
	switch args[0] {
	case "check":
		return runUpdateCheck(ctx, args[1:], streams)
	case "apply":
		return runUpdateApply(ctx, args[1:], streams)
	case "status":
		return runUpdateStatus(args[1:], streams)
	case "rollback":
		return runUpdateRollback(ctx, args[1:], streams)
	case "_windows_helper":
		return runWindowsUpdateHelper(args[1:])
	default:
		return configurationError{err: errors.New("invalid update operation"), message: updateUsage}
	}
}

type updateInput struct {
	source, manifest, signature, trustKey, channel, version, caFile, stateDir string
	timeout                                                                   time.Duration
	maxDownload                                                               int64
	allowDowngrade, allowInsecureLoopback                                     bool
}

func parseUpdateInput(operation string, args []string) (updateInput, error) {
	flags := flag.NewFlagSet("update "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	input := updateInput{}
	flags.StringVar(&input.source, "source", "", "")
	flags.StringVar(&input.manifest, "manifest", "", "")
	flags.StringVar(&input.signature, "signature", "", "")
	flags.StringVar(&input.trustKey, "trust-key", "", "")
	flags.StringVar(&input.channel, "channel", "stable", "")
	flags.StringVar(&input.version, "version", "", "")
	flags.StringVar(&input.caFile, "ca-file", "", "")
	flags.StringVar(&input.stateDir, "state-dir", "", "")
	flags.DurationVar(&input.timeout, "timeout", selfupdate.DefaultTimeout, "")
	maxDownload := flags.String("max-download", "512MiB", "")
	flags.BoolVar(&input.allowDowngrade, "allow-downgrade", false, "")
	flags.BoolVar(&input.allowInsecureLoopback, "allow-insecure-loopback", false, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return updateInput{}, configurationError{err: errors.New("invalid update usage"), message: updateUsage}
	}
	if (input.source == "") == (input.manifest == "") || input.trustKey == "" || input.timeout <= 0 {
		return updateInput{}, configurationError{err: errors.New("missing update input"), message: updateUsage}
	}
	if input.manifest != "" && input.signature == "" {
		return updateInput{}, configurationError{err: errors.New("missing detached signature"), message: updateUsage}
	}
	if input.source != "" && input.signature != "" {
		return updateInput{}, configurationError{err: errors.New("remote signature override"), message: updateUsage}
	}
	parsed, err := parseByteLimit(*maxDownload)
	if err != nil {
		return updateInput{}, configurationError{err: err, message: updateUsage}
	}
	input.maxDownload = parsed
	return input, nil
}

func runUpdateCheck(ctx context.Context, args []string, streams IO) error {
	input, err := parseUpdateInput("check", args)
	if err != nil {
		return err
	}
	target, state, err := updateTarget(input.stateDir)
	if err != nil {
		return updateFailure(err)
	}
	if status, statusErr := selfupdate.ReadStatus(target, state); statusErr != nil {
		return updateFailure(statusErr)
	} else if unresolvedUpdate(status) {
		return updateFailure(selfupdate.ErrRecoveryRequired)
	}
	reportUpdateProgress(streams, "checking signed update manifest")
	fetched, err := fetchUpdate(ctx, input)
	if errors.Is(err, selfupdate.ErrNoUpdate) {
		return json.NewEncoder(streams.stdout()).Encode(map[string]any{"schemaVersion": updateJSONSchemaVersion, "status": "no_update", "compatibility": "schema_unknown"})
	}
	if err != nil {
		return updateFailure(err)
	}
	return json.NewEncoder(streams.stdout()).Encode(map[string]any{
		"schemaVersion": updateJSONSchemaVersion, "status": "available", "version": fetched.Candidate.Release.Version,
		"commit": fetched.Candidate.Release.Commit, "goos": fetched.Candidate.Artifact.GOOS,
		"goarch": fetched.Candidate.Artifact.GOARCH, "size": fetched.Candidate.Artifact.Size, "compatibility": candidateCompatibility(currentBuild(), fetched.Candidate),
	})
}

func runUpdateApply(ctx context.Context, args []string, streams IO) error {
	input, err := parseUpdateInput("apply", args)
	if err != nil {
		return err
	}
	target, state, err := updateTarget(input.stateDir)
	if err != nil {
		return updateFailure(err)
	}
	if status, statusErr := selfupdate.Recover(ctx, target, state, nil); statusErr != nil {
		return updateFailure(statusErr)
	} else if unresolvedUpdate(status) {
		return updateFailure(selfupdate.ErrRecoveryRequired)
	} else if status.Recovered {
		return json.NewEncoder(streams.stdout()).Encode(map[string]any{"schemaVersion": updateJSONSchemaVersion, "status": status})
	}
	reportUpdateProgress(streams, "checking signed update manifest")
	fetched, err := fetchUpdate(ctx, input)
	if errors.Is(err, selfupdate.ErrNoUpdate) {
		return json.NewEncoder(streams.stdout()).Encode(map[string]any{"schemaVersion": updateJSONSchemaVersion, "status": "no_update", "compatibility": "schema_unknown"})
	}
	if err != nil {
		return updateFailure(err)
	}
	if compatibility := candidateCompatibility(currentBuild(), fetched.Candidate); compatibility != "compatible" {
		return updateFailure(selfupdate.ErrMigrationRequired)
	}
	key, err := loadTrustKey(input.trustKey)
	if err != nil {
		return updateFailure(err)
	}
	reportUpdateProgress(streams, "downloading and verifying update")
	status, err := selfupdate.Apply(ctx, selfupdate.ApplyOptions{Target: target, StateDir: state, Fetched: fetched, TrustKey: key, Current: currentBuild(), WindowsLaunch: selfupdate.LaunchWindowsHelper})
	if err != nil {
		return updateFailure(err)
	}
	return json.NewEncoder(streams.stdout()).Encode(map[string]any{"schemaVersion": updateJSONSchemaVersion, "status": status})
}

func runUpdateStatus(args []string, streams IO) error {
	flags := flag.NewFlagSet("update status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonOutput := flags.Bool("json", false, "")
	stateDir := flags.String("state-dir", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return configurationError{err: errors.New("invalid update status usage"), message: "usage: pgbase update status [--json] [--state-dir DIR]"}
	}
	target, state, err := updateTarget(*stateDir)
	if err != nil {
		return err
	}
	status, err := selfupdate.ReadStatus(target, state)
	if err != nil {
		return updateFailure(err)
	}
	if *jsonOutput {
		return json.NewEncoder(streams.stdout()).Encode(map[string]any{"schemaVersion": updateJSONSchemaVersion, "status": status})
	}
	_, err = fmt.Fprintf(streams.stdout(), "phase=%s current=%s candidate=%s\n", status.Phase, status.Current.Version, status.Candidate.Version)
	return err
}

func runUpdateRollback(ctx context.Context, args []string, streams IO) error {
	flags := flag.NewFlagSet("update rollback", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	stateDir := flags.String("state-dir", "", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return configurationError{err: errors.New("invalid update rollback usage"), message: "usage: pgbase update rollback [--state-dir DIR]"}
	}
	target, state, err := updateTarget(*stateDir)
	if err != nil {
		return updateFailure(err)
	}
	previous, err := selfupdate.ReadStatus(target, state)
	if err != nil {
		return updateFailure(err)
	}
	if previous.Phase == "idle" {
		return updateFailure(selfupdate.ErrRecoveryRequired)
	}
	compatibility := candidateCompatibility(currentBuild(), selfupdate.Candidate{Release: selfupdate.Release{Schema: schemaFromBuild(previous.Previous)}})
	if compatibility == "migration_required" {
		return updateFailure(selfupdate.ErrMigrationRequired)
	}
	if compatibility != "compatible" {
		return updateFailure(selfupdate.ErrRecoveryRequired)
	}
	reportUpdateProgress(streams, "restoring verified update backup")
	status, err := selfupdate.Rollback(ctx, target, state, nil)
	if err != nil {
		return updateFailure(err)
	}
	return json.NewEncoder(streams.stdout()).Encode(map[string]any{"schemaVersion": updateJSONSchemaVersion, "status": status})
}

func unresolvedUpdate(status selfupdate.Status) bool {
	return status.Phase != "idle" && status.Phase != "aborted" && status.Phase != "rolled_back" && status.Phase != "committed"
}

func reportUpdateProgress(streams IO, message string) { _, _ = fmt.Fprintln(streams.stderr(), message) }

func fetchUpdate(ctx context.Context, input updateInput) (selfupdate.Fetched, error) {
	key, err := loadTrustKey(input.trustKey)
	if err != nil {
		return selfupdate.Fetched{}, err
	}
	source, signature := input.source, ""
	if input.manifest != "" {
		source, signature = input.manifest, input.signature
	}
	client, err := updateHTTPClient(input.caFile)
	if err != nil {
		return selfupdate.Fetched{}, err
	}
	return selfupdate.Fetch(ctx, selfupdate.FetchOptions{
		Source: source, SignaturePath: signature, TrustKey: key, Current: currentBuild(),
		Channel: input.channel, Requested: input.version, AllowDowngrade: input.allowDowngrade,
		AllowInsecureLoopback: input.allowInsecureLoopback, Timeout: input.timeout,
		MaxArtifactBytes: input.maxDownload, HTTPClient: client,
	})
}

func currentBuild() selfupdate.Build {
	info := buildinfo.Current()
	return selfupdate.Build{Version: info.Version, Commit: info.Commit, BuiltAt: info.BuiltAt, GOOS: info.GOOS, GOARCH: info.GOARCH, SchemaMin: info.SchemaMin, SchemaMax: info.SchemaMax}
}

func candidateCompatibility(current selfupdate.Build, candidate selfupdate.Candidate) string {
	currentSchema, ok := schemaValue(current.SchemaMin, current.SchemaMax)
	if !ok {
		return "schema_unknown"
	}
	if candidate.Release.Schema.Min == 0 && candidate.Release.Schema.Max == 0 {
		return "schema_unknown"
	}
	if currentSchema < candidate.Release.Schema.Min || currentSchema > candidate.Release.Schema.Max {
		return "migration_required"
	}
	return "compatible"
}

func schemaFromBuild(build selfupdate.Build) selfupdate.Schema {
	minimum, maximum, ok := schemaRange(build.SchemaMin, build.SchemaMax)
	if !ok {
		return selfupdate.Schema{}
	}
	return selfupdate.Schema{Min: minimum, Max: maximum}
}

func schemaValue(minimum, maximum string) (int64, bool) {
	min, max, ok := schemaRange(minimum, maximum)
	return min, ok && min == max
}

func schemaRange(minimum, maximum string) (int64, int64, bool) {
	min, err := strconv.ParseInt(minimum, 10, 64)
	if err != nil || min < 0 {
		return 0, 0, false
	}
	max, err := strconv.ParseInt(maximum, 10, 64)
	if err != nil || max < min {
		return 0, 0, false
	}
	return min, max, true
}

func updateTarget(stateOverride string) (string, string, error) {
	target, err := os.Executable()
	if err != nil {
		return "", "", selfupdate.ErrInvalidInput
	}
	target, err = filepath.EvalSymlinks(target)
	if err != nil {
		return "", "", selfupdate.ErrInvalidInput
	}
	state := stateOverride
	if state == "" {
		state = filepath.Join(filepath.Dir(target), ".pgbase-update")
	}
	if !filepath.IsAbs(state) {
		return "", "", selfupdate.ErrInvalidInput
	}
	return target, state, nil
}

func loadTrustKey(path string) ([]byte, error) {
	raw, err := selfupdate.ReadRegularFile(path, selfupdate.SignatureMaxBytes)
	if err != nil {
		return nil, selfupdate.ErrInvalidInput
	}
	return selfupdate.ParseTrustKey(raw)
}

func updateHTTPClient(caFile string) (*http.Client, error) {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if caFile == "" {
		return &http.Client{Transport: transport}, nil
	}
	pem, err := selfupdate.ReadRegularFile(caFile, selfupdate.ManifestMaxBytes)
	if err != nil {
		return nil, selfupdate.ErrInvalidInput
	}
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, selfupdate.ErrInvalidInput
	}
	if transport.TLSClientConfig == nil {
		transport.TLSClientConfig = &tls.Config{}
	} else {
		transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	}
	transport.TLSClientConfig.RootCAs = pool
	return &http.Client{Transport: transport}, nil
}

func parseByteLimit(raw string) (int64, error) {
	units := []struct {
		suffix string
		value  int64
	}{{"GIB", 1 << 30}, {"MIB", 1 << 20}, {"KIB", 1 << 10}, {"B", 1}}
	upper := strings.ToUpper(raw)
	for _, unit := range units {
		if strings.HasSuffix(upper, unit.suffix) {
			value := strings.TrimSuffix(upper, unit.suffix)
			parsed, err := strconv.ParseInt(value, 10, 64)
			if err != nil || parsed <= 0 || parsed > (1<<63-1)/unit.value {
				return 0, fmt.Errorf("invalid byte limit")
			}
			return parsed * unit.value, nil
		}
	}
	return 0, fmt.Errorf("invalid byte limit")
}
