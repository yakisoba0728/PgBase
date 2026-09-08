//go:build restore

// Package restore is the backup/restore drill. It replaces the workflow's
// inline shell so the same procedure runs locally and in release mode:
//
//	go test -tags=restore ./tests/restore
//	PGBASE_RESTORE_CANDIDATE_IMAGE=registry/name@sha256:... \
//	  PGBASE_RESTORE_EXPECTED_VERSION=vX.Y.Z go test -tags=restore ./tests/restore
package restore

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

const (
	databaseUser     = "pgbase"
	sourceDatabase   = "pgbase"
	restoredDatabase = "pgbase_restore"
)

// supportedPostgresMajors is the coverage the drill owes every run: a restore
// that only proves one major says nothing about the other two.
var supportedPostgresMajors = []string{"16", "17", "18"}

// The drill covers every PostgreSQL major pgBase supports. Overriding the list
// is for local iteration only; a release drill rejects the override so the
// candidate is always restored and started on the full matrix.
func postgresMatrix() []postgresImage {
	if override := os.Getenv("PGBASE_RESTORE_POSTGRES_IMAGES"); override != "" {
		var images []postgresImage
		for _, reference := range strings.Split(override, ",") {
			reference = strings.TrimSpace(reference)
			if reference == "" {
				continue
			}
			images = append(images, postgresImage{major: majorFromReference(reference), reference: reference})
		}
		return images
	}
	return []postgresImage{
		{major: "16", reference: "postgres:16.13-bookworm@sha256:472efd9a66f2b2f1a5aeb18b28de74332e6ef88c2b93a1a5d812fb6db67a5f60"},
		{major: "17", reference: "postgres:17.9-bookworm@sha256:47f917f7409eacd22fc5dfb1dee634e1b55cf0c01d1a7eb701be2227a03e0641"},
		{major: "18", reference: "postgres:18.3-bookworm@sha256:80630f83606d8db77d30b3851b16a9f78be2d0d4dda6f7b82a1fdca5ebe3acba"},
	}
}

type postgresImage struct {
	major     string
	reference string
}

func majorFromReference(reference string) string {
	name := reference
	if index := strings.Index(name, "@"); index >= 0 {
		name = name[:index]
	}
	_, tag, found := strings.Cut(name, ":")
	if !found {
		return "unknown"
	}
	major, _, _ := strings.Cut(tag, ".")
	return major
}

// candidate is the pgBase build under test. Release mode runs one digest-pinned
// image and has no source-build fallback at all; local mode builds from the
// working tree so the drill stays runnable during development.
type candidate struct {
	release     bool
	image       string
	version     string
	binary      string
	network     string
	password    string
	tokenSecret string
}

var (
	digestReference  = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
	canonicalVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$`)
)

// resolveCandidateMode reads the release contract without touching Docker or the
// working tree, so the refusal rules are testable on their own.
func resolveCandidateMode(image, version string) (release bool, err error) {
	if image == "" && version == "" {
		return false, nil
	}
	if image == "" || version == "" {
		return false, errors.New("PGBASE_RESTORE_CANDIDATE_IMAGE and PGBASE_RESTORE_EXPECTED_VERSION must be set together")
	}
	if !digestReference.MatchString(image) {
		return false, fmt.Errorf("PGBASE_RESTORE_CANDIDATE_IMAGE %q must be an image@sha256 digest reference", image)
	}
	name := image[:strings.LastIndex(image, "@")]
	if strings.Contains(name[strings.LastIndex(name, "/")+1:], ":") {
		return false, fmt.Errorf("PGBASE_RESTORE_CANDIDATE_IMAGE %q must not carry a mutable tag", image)
	}
	if !canonicalVersion.MatchString(version) {
		return false, fmt.Errorf("PGBASE_RESTORE_EXPECTED_VERSION %q is not a supported canonical release version", version)
	}
	return true, nil
}

func newCandidate(t *testing.T) *candidate {
	t.Helper()
	image := os.Getenv("PGBASE_RESTORE_CANDIDATE_IMAGE")
	version := os.Getenv("PGBASE_RESTORE_EXPECTED_VERSION")
	release, err := resolveCandidateMode(image, version)
	if err != nil {
		t.Fatal(err)
	}
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		t.Fatal(err)
	}
	drill := &candidate{
		release:     release,
		image:       image,
		version:     version,
		password:    randomHex(t, 24),
		tokenSecret: base64.StdEncoding.EncodeToString(secret),
		network:     "pgbase-restore-" + randomHex(t, 6),
	}
	if release {
		// A release drill must run the published artifact or nothing; pulling
		// here keeps the failure at setup instead of mid-procedure.
		if output, err := dockerRun(context.Background(), 10*time.Minute, "pull", image); err != nil {
			t.Fatalf("pull the digest-pinned restore candidate: %v\n%s", err, output)
		}
	} else {
		drill.binary = filepath.Join(t.TempDir(), "pgbase")
		build := exec.Command("go", "build", "-o", drill.binary, "./cmd/pgbase")
		build.Dir = repositoryRoot(t)
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build the local restore candidate: %v\n%s", err, output)
		}
	}
	if output, err := dockerRun(context.Background(), time.Minute, "network", "create", drill.network); err != nil {
		t.Fatalf("create the drill network: %v\n%s", err, output)
	}
	t.Cleanup(func() {
		if output, err := dockerRun(context.Background(), time.Minute, "network", "rm", drill.network); err != nil {
			t.Errorf("remove the drill network: %v\n%s", err, output)
		}
	})
	return drill
}

func (c *candidate) describe() string {
	if c.release {
		return "release candidate " + c.image
	}
	return "locally built binary"
}

// databaseURL resolves the same logical database from wherever the candidate
// runs: inside the drill network for a release image, on the published host
// port for a locally built binary.
func (c *candidate) databaseURL(server *postgresServer, database string) string {
	if c.release {
		return fmt.Sprintf("postgres://%s:%s@%s:5432/%s?sslmode=disable", databaseUser, c.password, server.alias, database)
	}
	return fmt.Sprintf("postgres://%s:%s@127.0.0.1:%d/%s?sslmode=disable", databaseUser, c.password, server.hostPort, database)
}

func (c *candidate) environment(databaseURL, httpAddress, publicURL string) []string {
	environment := []string{
		"PGBASE_DATABASE_URL=" + databaseURL,
		"PGBASE_TOKEN_SECRET=" + c.tokenSecret,
		"PGBASE_HTTP_ADDRESS=" + httpAddress,
		"PGBASE_PUBLIC_URL=" + publicURL,
		"PGBASE_USER_REGISTRATION=true",
	}
	if c.release {
		// The release candidate reaches the drill database over the dedicated,
		// disposable Docker network. Production-like remote targets remain on
		// the default verified-TLS policy.
		environment = append(environment, "PGBASE_ALLOW_INSECURE_DATABASE=true")
	}
	return environment
}

// command runs a one-shot pgBase subcommand such as `migrate up` or `doctor`.
func (c *candidate) command(ctx context.Context, databaseURL, stdin string, arguments ...string) (string, error) {
	environment := c.environment(databaseURL, "127.0.0.1:8090", "http://127.0.0.1:8090")
	if c.release {
		dockerArguments := []string{"run", "--rm", "--interactive", "--network", c.network}
		for _, value := range environment {
			dockerArguments = append(dockerArguments, "--env", value)
		}
		dockerArguments = append(dockerArguments, c.image)
		dockerArguments = append(dockerArguments, arguments...)
		return dockerRunWithInput(ctx, 5*time.Minute, stdin, dockerArguments...)
	}
	command := exec.CommandContext(ctx, c.binary, arguments...)
	command.Env = append(os.Environ(), environment...)
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

// server is a running pgBase instance, in whichever form the candidate takes.
type server struct {
	candidate *candidate
	baseURL   string
	container string
	process   *exec.Cmd
	logs      *bytes.Buffer
	stopped   bool
}

func (c *candidate) serve(t *testing.T, ctx context.Context, databaseURL, name string) *server {
	t.Helper()
	port := freePort(t)
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
	instance := &server{candidate: c, baseURL: baseURL}
	if c.release {
		instance.container = name
		arguments := []string{
			"run", "--detach", "--name", name, "--network", c.network,
			"--publish", fmt.Sprintf("127.0.0.1:%d:8090", port),
		}
		for _, value := range c.environment(databaseURL, ":8090", baseURL) {
			arguments = append(arguments, "--env", value)
		}
		arguments = append(arguments, c.image, "serve")
		if output, err := dockerRun(ctx, time.Minute, arguments...); err != nil {
			t.Fatalf("start the candidate server: %v\n%s", err, output)
		}
	} else {
		instance.logs = &bytes.Buffer{}
		command := exec.Command(c.binary, "serve")
		command.Env = append(os.Environ(), c.environment(databaseURL, fmt.Sprintf("127.0.0.1:%d", port), baseURL)...)
		command.Stdout = instance.logs
		command.Stderr = instance.logs
		if err := command.Start(); err != nil {
			t.Fatalf("start the candidate server: %v", err)
		}
		instance.process = command
	}
	t.Cleanup(func() { instance.stop(t) })
	instance.waitReady(t, ctx)
	return instance
}

// stop drains the instance the same way an orchestrator would, so a later dump
// never observes a half-written transaction from a killed process.
func (s *server) stop(t *testing.T) {
	t.Helper()
	if s.stopped {
		return
	}
	s.stopped = true
	if s.container != "" {
		if output, err := dockerRun(context.Background(), time.Minute, "stop", "--timeout", "30", s.container); err != nil {
			t.Errorf("stop the candidate server: %v\n%s", err, output)
		}
		if output, err := dockerRun(context.Background(), time.Minute, "rm", "--force", s.container); err != nil {
			t.Errorf("remove the candidate server: %v\n%s", err, output)
		}
		return
	}
	if s.process == nil || s.process.Process == nil {
		return
	}
	if err := s.process.Process.Signal(syscall.SIGTERM); err != nil {
		t.Errorf("signal the candidate server: %v", err)
		return
	}
	done := make(chan error, 1)
	go func() { done <- s.process.Wait() }()
	select {
	case <-done:
	case <-time.After(45 * time.Second):
		_ = s.process.Process.Kill()
		t.Error("the candidate server did not exit within its shutdown budget")
	}
}

func (s *server) diagnostics() string {
	if s.container != "" {
		output, _ := dockerRun(context.Background(), 30*time.Second, "logs", s.container)
		return output
	}
	if s.logs == nil {
		return ""
	}
	return s.logs.String()
}

func (s *server) waitReady(t *testing.T, ctx context.Context) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Minute)
	client := newAdminClient(s.baseURL)
	for time.Now().Before(deadline) {
		if err := client.ready(ctx); err == nil {
			return
		}
		if ctx.Err() != nil {
			t.Fatalf("wait for readiness: %v\n%s", ctx.Err(), s.diagnostics())
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatalf("the candidate server never became ready\n%s", s.diagnostics())
}

// postgresServer is a disposable PostgreSQL container that also supplies the
// server-matched pg_dump and pg_restore clients the drill requires.
type postgresServer struct {
	container string
	alias     string
	hostPort  int
	major     string
}

func startPostgres(t *testing.T, ctx context.Context, drill *candidate, image postgresImage) *postgresServer {
	t.Helper()
	instance := &postgresServer{
		container: "pgbase-restore-postgres-" + randomHex(t, 6),
		alias:     "postgres",
		hostPort:  freePort(t),
		major:     image.major,
	}
	arguments := []string{
		"run", "--detach", "--name", instance.container,
		"--network", drill.network, "--network-alias", instance.alias,
		"--publish", fmt.Sprintf("127.0.0.1:%d:5432", instance.hostPort),
		"--env", "POSTGRES_USER=" + databaseUser,
		"--env", "POSTGRES_PASSWORD=" + drill.password,
		"--env", "POSTGRES_DB=" + sourceDatabase,
		image.reference,
	}
	if output, err := dockerRun(ctx, 10*time.Minute, arguments...); err != nil {
		t.Fatalf("start PostgreSQL %s: %v\n%s", image.major, err, output)
	}
	t.Cleanup(func() {
		if output, err := dockerRun(context.Background(), time.Minute, "rm", "--force", "--volumes", instance.container); err != nil {
			t.Errorf("remove PostgreSQL %s: %v\n%s", image.major, err, output)
		}
	})
	// The entrypoint runs a temporary initialization server that listens on the
	// Unix socket only, so a socket probe reports ready while every TCP client
	// still gets EOF. The drill connects over TCP, so it waits for TCP.
	deadline := time.Now().Add(2 * time.Minute)
	for time.Now().Before(deadline) {
		if _, err := instance.exec(ctx, "psql", "--host", "127.0.0.1", "--port", "5432",
			"--username", databaseUser, "--dbname", sourceDatabase,
			"--no-psqlrc", "--tuples-only", "--no-align", "--command", "select 1"); err == nil {
			return instance
		}
		time.Sleep(250 * time.Millisecond)
	}
	logs, _ := dockerRun(ctx, 30*time.Second, "logs", instance.container)
	t.Fatalf("PostgreSQL %s never became ready\n%s", image.major, logs)
	return nil
}

func (p *postgresServer) exec(ctx context.Context, arguments ...string) (string, error) {
	return dockerRun(ctx, 10*time.Minute, append([]string{"exec", p.container}, arguments...)...)
}

// query returns one scalar so callers assert on values rather than on psql
// formatting; ON_ERROR_STOP keeps a failed statement from returning empty text.
func (p *postgresServer) query(t *testing.T, ctx context.Context, database, sql string) string {
	t.Helper()
	output, err := p.exec(ctx, "psql", "--username", databaseUser, "--dbname", database,
		"--no-psqlrc", "--tuples-only", "--no-align", "--set", "ON_ERROR_STOP=1", "--command", sql)
	if err != nil {
		t.Fatalf("query %s: %v\n%s", database, err, output)
	}
	return strings.TrimSpace(output)
}

func (p *postgresServer) execute(t *testing.T, ctx context.Context, database, sql string) {
	t.Helper()
	output, err := p.exec(ctx, "psql", "--username", databaseUser, "--dbname", database,
		"--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--command", sql)
	if err != nil {
		t.Fatalf("execute against %s: %v\n%s", database, err, output)
	}
}

func dockerRun(ctx context.Context, timeout time.Duration, arguments ...string) (string, error) {
	return dockerRunWithInput(ctx, timeout, "", arguments...)
}

func dockerRunWithInput(ctx context.Context, timeout time.Duration, stdin string, arguments ...string) (string, error) {
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, "docker", arguments...)
	if stdin != "" {
		command.Stdin = strings.NewReader(stdin)
	}
	output, err := command.CombinedOutput()
	return string(output), err
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func freePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func randomHex(t *testing.T, size int) string {
	t.Helper()
	value := make([]byte, size)
	if _, err := rand.Read(value); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(value)
}

func mustAtoi(t *testing.T, raw string) int {
	t.Helper()
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	return value
}
