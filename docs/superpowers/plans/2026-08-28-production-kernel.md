# Production Kernel Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a production-oriented pgBase kernel that boots with PostgreSQL, creates the first administrator, creates real PostgreSQL-backed collections, and exposes administrator-only record CRUD.

**Architecture:** A single Go modular monolith owns HTTP, CLI, schema planning, migrations, and PostgreSQL access. PostgreSQL is the only durable state; `pgbase_meta` stores system metadata and `app` stores physical collection tables. A React/Vite administrator UI is built into the release binary.

**Tech Stack:** Go 1.26.7, `net/http`, pgx v5.10.0, `golang.org/x/crypto` v0.55.0, `google/uuid` v1.6.0, PostgreSQL 16.13+, React 19.2.8, Vite 8.2.2, TypeScript 7.0.2, Vitest 4.1.11, Node.js 24.

**Spec:** `docs/superpowers/specs/2026-08-28-production-kernel-design.md`

## Global Constraints

- The Go module path is exactly `github.com/yakisoba0728/PgBase`, matching the configured Git remote.
- One pgBase project owns one PostgreSQL database; multiple stateless pgBase processes may share that database.
- Support PostgreSQL 16 and newer, with PostgreSQL 16.13 as the minimum CI image.
- Persist pgBase-owned state only in `pgbase_meta`; create user collection tables only in `app`.
- Use physical PostgreSQL tables, typed columns, constraints, and transactions. Do not add an ORM or a generic JSON record table.
- All record endpoints remain administrator-only until the later authentication and rules phase.
- Accept only collection and field names matching `^[a-z][a-z0-9_]{0,47}$`; reserve `id`, `created_at`, `updated_at`, and the `pgbase_` prefix.
- Bind all record values as PostgreSQL parameters. Produce identifiers only from validated catalog metadata and quote them with `pgx.Identifier.Sanitize`.
- Accept only typed literal defaults plus the `now` sentinel for `timestamptz`; never accept arbitrary SQL defaults.
- Encode `bigint` and `numeric` as decimal JSON strings, timestamps as UTC RFC 3339 strings, and UUIDs as canonical strings.
- Use the common JSON error envelope from the spec and never expose SQL, SQLSTATE, DSNs, passwords, setup tokens, cookies, or session tokens.
- System migration application is explicit by default. Only the development Compose configuration enables `PGBASE_AUTO_MIGRATE=true`.
- Every implementation task follows red-green-refactor, runs focused tests and the affected package suite, and ends in its own commit.
- Do not implement relationships, advanced filters, public user auth, realtime, files, TypeScript SDK, dynamic OpenAPI, online DDL, replicas, or sharding in this plan.

## File and Package Map

- `cmd/pgbase/main.go`: process entry point only.
- `pgbase/options.go`: public construction options and validation.
- `pgbase/app.go`: public `App` lifecycle and component wiring.
- `internal/cli/`: standard-library subcommand parsing and exit-code mapping.
- `internal/config/`: environment parsing into public options.
- `internal/database/`: pgx pool, transactions, error classification, and testable query interfaces.
- `migrations/system/`: embedded ordered system SQL and checksums.
- `internal/migrate/`: bootstrap, status, advisory locking, and migration application.
- `internal/adminauth/`: password hashing, token hashing, rate limits, administrators, and sessions.
- `internal/schema/`: names, types, catalog, planning, application, cache, and LISTEN/NOTIFY synchronization.
- `internal/query/`: JSON/PostgreSQL codecs, CRUD SQL compilation, and signed cursors.
- `internal/api/`: middleware, error envelope, health, administrator, schema, record, and UI routes.
- `internal/observability/`: JSON logging and secret redaction.
- `internal/testutil/`: per-test PostgreSQL database creation and cleanup.
- `ui/admin/`: React application, tests, Vite build, and Go embed package.
- `deployments/`: Dockerfile and Compose configuration.
- `tests/e2e/`: black-box bootstrap, CRUD, restart, multi-instance, and shutdown tests.
- `.github/workflows/`: backend, frontend, and E2E verification.

---

### Task 1: Repository Baseline, Public Options, and CLI Shell

**Files:**
- Create: `.gitignore`
- Create: `.gitattributes`
- Create: `go.mod`
- Create: `pgbase/options.go`
- Create: `pgbase/options_test.go`
- Create: `pgbase/version.go`
- Create: `internal/config/env.go`
- Create: `internal/config/env_test.go`
- Create: `internal/cli/run.go`
- Create: `internal/cli/run_test.go`
- Create: `cmd/pgbase/main.go`

**Interfaces:**
- Produces:

```go
type Options struct {
    DatabaseURL      string
    HTTPAddress      string
    PublicURL        *url.URL
    SetupToken       string
    TokenSecret      []byte
    AutoMigrate      bool
    TrustedProxies   []netip.Prefix
    CORSAllowedOrigins []string
    AdminSessionTTL  time.Duration
    StatementTimeout time.Duration
    ShutdownTimeout  time.Duration
    Logger           *slog.Logger
}

func (o Options) Normalized() (Options, error)
func LoadEnv(lookup func(string) (string, bool)) (pgbase.Options, error)

type Command func(context.Context, []string, func(string) (string, bool), io.Writer, io.Writer) error
type CommandSet struct {
    Serve   Command
    Migrate Command
    Admin   Command
    Doctor  Command
    Healthcheck Command
}

func Run(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer, commands CommandSet) int
```

- Later tasks consume `pgbase.Options` without importing `internal/config`.

- [ ] **Step 1: Write option and environment validation tests**

```go
func TestLoadEnvRejectsShortTokenSecret(t *testing.T) {
    env := map[string]string{
        "PGBASE_DATABASE_URL": "postgres://postgres:postgres@localhost/pgbase",
        "PGBASE_TOKEN_SECRET": base64.StdEncoding.EncodeToString(make([]byte, 31)),
    }
    _, err := LoadEnv(func(key string) (string, bool) {
        value, ok := env[key]
        return value, ok
    })
    if err == nil || !strings.Contains(err.Error(), "PGBASE_TOKEN_SECRET") {
        t.Fatalf("expected token-secret validation error, got %v", err)
    }
}

func TestOptionsDefaults(t *testing.T) {
    opts, err := (Options{
        DatabaseURL: "postgres://postgres:postgres@localhost/pgbase",
        TokenSecret: make([]byte, 32),
    }).Normalized()
    if err != nil {
        t.Fatal(err)
    }
    if opts.AdminSessionTTL != 12*time.Hour {
        t.Fatalf("AdminSessionTTL = %s", opts.AdminSessionTTL)
    }
}
```

- [ ] **Step 2: Run the focused tests and verify RED**

Run: `go test ./pgbase ./internal/config -run 'TestLoadEnv|TestOptions'`

Expected: FAIL because `Options` and `LoadEnv` do not exist.

- [ ] **Step 3: Implement options and environment parsing**

Use these defaults: `:8090`, 12-hour administrator session, 5-second statement timeout, and 15-second shutdown timeout. Require a PostgreSQL URL and a base64 token secret decoding to at least 32 bytes. If a setup token is present, require at least 32 characters. Validate `PGBASE_PUBLIC_URL` and every `PGBASE_CORS_ALLOWED_ORIGINS` entry as an exact `http` or `https` origin without path, query, fragment, or wildcard; parse trusted proxies as comma-separated CIDR prefixes; parse booleans and durations with standard-library functions.

```go
func (o Options) Normalized() (Options, error) {
    if o.HTTPAddress == "" {
        o.HTTPAddress = ":8090"
    }
    if o.AdminSessionTTL == 0 {
        o.AdminSessionTTL = 12 * time.Hour
    }
    if o.StatementTimeout == 0 {
        o.StatementTimeout = 5 * time.Second
    }
    if o.ShutdownTimeout == 0 {
        o.ShutdownTimeout = 15 * time.Second
    }
    if o.Logger == nil {
        o.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
    }
    if err := validateOptions(o); err != nil {
        return Options{}, err
    }
    return o, nil
}
```

Create `go.mod` with `module github.com/yakisoba0728/PgBase`, `go 1.26.0`, and `toolchain go1.26.7`. Set `pgbase.Version` to `"dev"`; release builds replace it through `-ldflags -X`.

Use this repository hygiene:

```gitignore
.env
.env.local
coverage/
*.test
ui/admin/node_modules/
```

```gitattributes
* text=auto eol=lf
*.bat text eol=crlf
```

Do not ignore `ui/admin/dist`; the generated administrator UI is part of the single-binary source release.

- [ ] **Step 4: Write CLI dispatch tests**

```go
func TestRunRejectsUnknownCommand(t *testing.T) {
    var stderr bytes.Buffer
    code := Run(context.Background(), []string{"unknown"}, emptyEnv, io.Discard, &stderr, CommandSet{})
    if code != 2 {
        t.Fatalf("exit code = %d", code)
    }
    if !strings.Contains(stderr.String(), "serve, migrate, admin, doctor") {
        t.Fatalf("stderr = %q", stderr.String())
    }
}
```

- [ ] **Step 5: Implement the CLI shell and process entry point**

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()
    os.Exit(cli.Run(ctx, os.Args[1:], os.LookupEnv, os.Stdout, os.Stderr, cli.DefaultCommands()))
}
```

The four public commands and hidden container `healthcheck` command dispatch through `CommandSet`; tests supply fakes and later tasks wire real handlers. A nil known-command handler is a programmer error that returns exit code 1. Unknown commands return usage exit code 2.

- [ ] **Step 6: Format and verify**

Run: `gofmt -w cmd pgbase internal/config internal/cli`

Run: `go test ./pgbase ./internal/config ./internal/cli`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add .gitignore .gitattributes go.mod cmd pgbase internal/config internal/cli
git commit -m "chore: bootstrap pgbase command and configuration"
```

### Task 2: PostgreSQL Pool, Transactions, and Isolated Test Databases

**Files:**
- Create: `internal/database/interfaces.go`
- Create: `internal/database/pool.go`
- Create: `internal/database/tx.go`
- Create: `internal/database/errors.go`
- Create: `internal/database/pool_test.go`
- Create: `internal/database/tx_test.go`
- Create: `internal/testutil/postgres.go`

**Interfaces:**
- Consumes: `pgbase.Options.DatabaseURL`, `pgbase.Options.StatementTimeout`.
- Produces:

```go
type DBTX interface {
    Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
    Query(context.Context, string, ...any) (pgx.Rows, error)
    QueryRow(context.Context, string, ...any) pgx.Row
}

type Kind uint8

const (
    KindUnknown Kind = iota
    KindValidation
    KindConflict
    KindUnavailable
    KindCanceled
)

type Pool struct{ inner *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string, statementTimeout time.Duration) (*Pool, error)
func (p *Pool) Ping(ctx context.Context) error
func (p *Pool) Close()
func (p *Pool) InTx(ctx context.Context, options pgx.TxOptions, fn func(pgx.Tx) error) error
func (p *Pool) Acquire(ctx context.Context) (*pgxpool.Conn, error)
func (p *Pool) Exec(context.Context, string, ...any) (pgconn.CommandTag, error)
func (p *Pool) Query(context.Context, string, ...any) (pgx.Rows, error)
func (p *Pool) QueryRow(context.Context, string, ...any) pgx.Row
func Classify(err error) Kind
func testutil.NewDatabase(t *testing.T) string
```

- [ ] **Step 1: Add exact Go dependencies**

Run:

```bash
go get github.com/jackc/pgx/v5@v5.10.0
go get github.com/google/uuid@v1.6.0
go get golang.org/x/crypto@v0.55.0
```

Expected: `go.mod` and `go.sum` pin the listed versions.

- [ ] **Step 2: Write pool and rollback integration tests**

```go
func TestInTxRollsBackOnError(t *testing.T) {
    databaseURL := testutil.NewDatabase(t)
    pool, err := Open(context.Background(), databaseURL, time.Second)
    if err != nil {
        t.Fatal(err)
    }
    defer pool.Close()

    _, err = pool.Exec(context.Background(), "create table rollback_probe (id integer primary key)")
    if err != nil {
        t.Fatal(err)
    }
    sentinel := errors.New("stop")
    err = pool.InTx(context.Background(), pgx.TxOptions{}, func(tx pgx.Tx) error {
        if _, execErr := tx.Exec(context.Background(), "insert into rollback_probe values (1)"); execErr != nil {
            return execErr
        }
        return sentinel
    })
    if !errors.Is(err, sentinel) {
        t.Fatalf("error = %v", err)
    }
    var count int
    if err := pool.QueryRow(context.Background(), "select count(*) from rollback_probe").Scan(&count); err != nil {
        t.Fatal(err)
    }
    if count != 0 {
        t.Fatalf("count = %d", count)
    }
}
```

- [ ] **Step 3: Run the integration test and verify RED**

Set `PGBASE_TEST_ADMIN_URL=postgres://postgres:postgres@localhost:5432/postgres?sslmode=disable`.

Run: `go test ./internal/database -run TestInTxRollsBackOnError -v`

Expected: FAIL because the database package is absent.

- [ ] **Step 4: Implement isolated database creation**

`testutil.NewDatabase` must generate `pgbase_test_` followed by lowercase UUID hex, connect through `PGBASE_TEST_ADMIN_URL`, quote the generated identifier with `pgx.Identifier.Sanitize`, create the database, and register cleanup that terminates remaining connections and drops only that exact generated database. If the environment variable is absent, call `t.Skip`.

- [ ] **Step 5: Implement the pool and transaction wrapper**

Parse with `pgxpool.ParseConfig`, set `AfterConnect` to apply `statement_timeout`, `application_name='pgbase'`, and UTC timezone, construct with `pgxpool.NewWithConfig`, then call `Ping` before returning. `InTx` must always attempt rollback on callback error or panic and preserve the original error.

- [ ] **Step 6: Add PostgreSQL error classification tests and implementation**

Cover at least `23505 -> KindConflict`, `23502/23514/22P02 -> KindValidation`, `55P03/57014/57P01 -> KindUnavailable`, context cancellation, and unknown errors.

Run: `go test ./internal/database -v`

Expected: PASS against the test PostgreSQL instance.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/database internal/testutil
git commit -m "feat: add PostgreSQL transaction foundation"
```

### Task 3: Embedded System Migrations and Migration CLI

**Files:**
- Create: `migrations/system/embed.go`
- Create: `migrations/system/000001_initial.sql`
- Create: `internal/migrate/runner.go`
- Create: `internal/migrate/status.go`
- Create: `internal/migrate/runner_test.go`
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Consumes: `*database.Pool`, `database.DBTX`.
- Produces:

The migration source package defines:

```go
type Migration struct {
    Version int64
    Name    string
    SQL     string
    SHA256  [32]byte
}

type Status struct {
    Current int64
    Latest  int64
    Dirty   bool
}

type Runner struct {
    pool       *database.Pool
    logger     *slog.Logger
    migrations []system.Migration
}

func New(pool *database.Pool, logger *slog.Logger) *Runner
func (r *Runner) Up(ctx context.Context) error
func (r *Runner) Status(ctx context.Context) (Status, error)
```

- [ ] **Step 1: Write migration integration tests**

```go
func TestUpIsIdempotentAndCreatesSchemas(t *testing.T) {
    databaseURL := testutil.NewDatabase(t)
    pool, err := database.Open(context.Background(), databaseURL, time.Second)
    if err != nil {
        t.Fatal(err)
    }
    defer pool.Close()
    runner := New(pool, slog.New(slog.NewTextHandler(io.Discard, nil)))
    if err := runner.Up(context.Background()); err != nil {
        t.Fatal(err)
    }
    if err := runner.Up(context.Background()); err != nil {
        t.Fatal(err)
    }
    var count int
    err := pool.QueryRow(context.Background(),
        "select count(*) from pgbase_meta.system_migrations where version = 1").Scan(&count)
    if err != nil || count != 1 {
        t.Fatalf("count=%d err=%v", count, err)
    }
}
```

- [ ] **Step 2: Run the test and verify RED**

Run: `go test ./internal/migrate -run TestUpIsIdempotentAndCreatesSchemas -v`

Expected: FAIL because the runner and SQL do not exist.

- [ ] **Step 3: Create the ordered embedded migration**

`migrations/system/embed.go` must expose an immutable ordered slice. Migration 1 must create:

- schemas `pgbase_meta` and `app`;
- singleton `schema_state(singleton boolean primary key default true check (singleton), version bigint not null, updated_at timestamptz not null)`;
- `admins`, `admin_sessions`, and `admin_rate_limits`;
- `collections`, `fields`, and `collection_indexes`;
- `schema_migrations`;
- foreign keys with explicit cascade behavior and unique constraints described by the spec.

Use this bootstrap table before reading embedded migrations:

```sql
CREATE SCHEMA IF NOT EXISTS pgbase_meta;
CREATE TABLE IF NOT EXISTS pgbase_meta.system_migrations (
    version bigint PRIMARY KEY,
    name text NOT NULL,
    checksum bytea NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now(),
    execution_ms bigint NOT NULL CHECK (execution_ms >= 0)
);
```

The embedded `000001_initial.sql` contains these exact logical columns and constraints:

```sql
CREATE TABLE pgbase_meta.schema_state (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    version bigint NOT NULL CHECK (version >= 0),
    updated_at timestamptz NOT NULL
);
INSERT INTO pgbase_meta.schema_state (singleton, version, updated_at)
VALUES (true, 0, CURRENT_TIMESTAMP);

CREATE TABLE pgbase_meta.admins (
    id uuid PRIMARY KEY,
    email text NOT NULL UNIQUE,
    password_hash text NOT NULL,
    active boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE pgbase_meta.admin_sessions (
    id uuid PRIMARY KEY,
    admin_id uuid NOT NULL REFERENCES pgbase_meta.admins(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE CHECK (octet_length(token_hash) = 32),
    expires_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL
);
CREATE INDEX admin_sessions_admin_id_idx ON pgbase_meta.admin_sessions(admin_id);
CREATE INDEX admin_sessions_expires_at_idx ON pgbase_meta.admin_sessions(expires_at);

CREATE TABLE pgbase_meta.admin_rate_limits (
    subject_hash bytea PRIMARY KEY CHECK (octet_length(subject_hash) = 32),
    window_started_at timestamptz NOT NULL,
    failures integer NOT NULL CHECK (failures >= 0),
    blocked_until timestamptz,
    updated_at timestamptz NOT NULL
);

CREATE TABLE pgbase_meta.collections (
    id uuid PRIMARY KEY,
    name text NOT NULL UNIQUE,
    table_name text NOT NULL UNIQUE,
    status text NOT NULL CHECK (status = 'active'),
    created_at timestamptz NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE pgbase_meta.fields (
    id uuid PRIMARY KEY,
    collection_id uuid NOT NULL REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    name text NOT NULL,
    column_name text NOT NULL,
    field_type text NOT NULL CHECK (field_type IN ('boolean','integer','bigint','numeric','text','timestamptz','uuid','jsonb')),
    required boolean NOT NULL,
    is_unique boolean NOT NULL,
    is_indexed boolean NOT NULL,
    has_default boolean NOT NULL,
    default_json jsonb,
    ordinal integer NOT NULL CHECK (ordinal >= 0),
    created_at timestamptz NOT NULL,
    UNIQUE (collection_id, name),
    UNIQUE (collection_id, column_name),
    UNIQUE (collection_id, ordinal)
);

CREATE TABLE pgbase_meta.collection_indexes (
    id uuid PRIMARY KEY,
    collection_id uuid NOT NULL REFERENCES pgbase_meta.collections(id) ON DELETE CASCADE,
    field_id uuid NOT NULL REFERENCES pgbase_meta.fields(id) ON DELETE CASCADE,
    index_name text NOT NULL UNIQUE,
    is_unique boolean NOT NULL,
    created_at timestamptz NOT NULL,
    UNIQUE (collection_id, field_id)
);

CREATE TABLE pgbase_meta.schema_migrations (
    version bigint PRIMARY KEY CHECK (version > 0),
    operation_id uuid NOT NULL UNIQUE,
    operation text NOT NULL CHECK (operation = 'collection_create'),
    checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
    plan_json jsonb NOT NULL,
    sql_statements text[] NOT NULL,
    applied_by uuid NOT NULL REFERENCES pgbase_meta.admins(id) ON DELETE RESTRICT,
    applied_at timestamptz NOT NULL,
    execution_ms bigint NOT NULL CHECK (execution_ms >= 0)
);
```

- [ ] **Step 4: Implement transactional application and checksum enforcement**

Use the fixed advisory key `0x7067626173650001`. Inside one transaction, execute `SET LOCAL lock_timeout = '5s'`, call `pg_advisory_xact_lock`, re-read applied rows, reject a changed checksum, execute each missing SQL file, and insert its history row. Never mark a migration applied before its SQL succeeds.

- [ ] **Step 5: Add a two-runner concurrency test**

Start two `Runner.Up` calls against the same fresh database with a barrier. Assert both return nil and every version has one history row.

Run: `go test ./internal/migrate -v`

Expected: PASS.

- [ ] **Step 6: Wire `pgbase migrate status` and `pgbase migrate up`**

The CLI must load options, open and ping the pool, run the requested operation, print human-readable status to stdout, map configuration errors to exit 2, and operational failures to exit 1.

Run: `go test ./internal/cli -run TestMigrate -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add migrations/system internal/migrate internal/cli
git commit -m "feat: add transactional system migrations"
```

### Task 4: Application Lifecycle, Health, and HTTP Safety Boundary

**Files:**
- Create: `pgbase/app.go`
- Create: `pgbase/app_test.go`
- Create: `internal/api/router.go`
- Create: `internal/api/json.go`
- Create: `internal/api/errors.go`
- Create: `internal/api/middleware.go`
- Create: `internal/api/health.go`
- Create: `internal/api/health_test.go`
- Create: `internal/api/test_helpers_test.go`
- Create: `internal/observability/logger.go`
- Create: `internal/observability/redact.go`
- Create: `internal/observability/redact_test.go`
- Modify: `internal/cli/run.go`

**Interfaces:**
- Consumes: `pgbase.Options`, `database.Open`, `migrate.Runner`.
- Produces:

```go
type App struct {
    options    Options
    pool       *database.Pool
    handler    http.Handler
    httpServer *http.Server
    readiness *readinessState
    closeOnce  sync.Once
}

type readinessState struct {
    accepting   atomic.Bool
    dependencies []api.Readiness
}

func New(ctx context.Context, options Options) (*App, error)
func (a *App) Start(ctx context.Context) error
func (a *App) Handler() http.Handler
func (a *App) Run(ctx context.Context) error
func (a *App) Close(ctx context.Context) error

type Readiness interface {
    Ready(context.Context) error
}

type Dependencies struct {
    Readiness Readiness
    Logger    *slog.Logger
}

func NewRouter(Dependencies) http.Handler

type APIError struct {
    Status  int
    Code    string
    Message string
    Details map[string][]string
}

type readinessFunc func(context.Context) error
func (f readinessFunc) Ready(ctx context.Context) error { return f(ctx) }

func assertErrorCode(t *testing.T, body []byte, want string)
```

- [ ] **Step 1: Write live and ready endpoint tests**

```go
func TestReadyFailsWhenDependencyFails(t *testing.T) {
    handler := NewRouter(Dependencies{
        Readiness: readinessFunc(func(context.Context) error { return errors.New("db down") }),
    })
    request := httptest.NewRequest(http.MethodGet, "/health/ready", nil)
    response := httptest.NewRecorder()
    handler.ServeHTTP(response, request)
    if response.Code != http.StatusServiceUnavailable {
        t.Fatalf("status = %d", response.Code)
    }
    assertErrorCode(t, response.Body.Bytes(), "not_ready")
}
```

`/health/live` depends only on process state. `/health/ready` uses a one-second child context and requires accepting state, PostgreSQL ping, current system migration status, loaded catalog, and listener readiness; it returns only component names and safe status, never raw dependency errors.

- [ ] **Step 2: Run the test and verify RED**

Run: `go test ./internal/api -run 'TestLive|TestReady'`

Expected: FAIL because the router does not exist.

- [ ] **Step 3: Implement middleware and the error envelope**

Use Go 1.26 `http.ServeMux` method patterns. Middleware order is recovery, request ID, access log, body limit, security headers, then route authentication. Generate 128-bit random request IDs and accept an inbound ID only when it matches `^[A-Za-z0-9_-]{8,128}$`. Limit JSON request bodies to 1 MiB.

CORS is disabled when `CORSAllowedOrigins` is empty. Otherwise, answer preflight only for exact configured origins, methods, and headers; add `Vary: Origin`; never combine credentialed requests with wildcard origins. Add tests for rejected wildcard configuration, unlisted origin, listed origin, and cache-safe `Vary` headers.

```json
{
  "error": {
    "code": "not_ready",
    "message": "Service is not ready",
    "details": {},
    "requestId": "req_01..."
  }
}
```

- [ ] **Step 4: Write and implement secret-redaction tests**

Test that URL passwords, `Authorization`, `Cookie`, `Set-Cookie`, setup tokens, and session tokens are replaced with `[REDACTED]`. The logger accepts typed attributes rather than logging raw request structs.

Run: `go test ./internal/observability -v`

Expected: PASS.

- [ ] **Step 5: Implement `App` lifecycle**

`New` calls `Options.Normalized`, opens and pings the pool, checks or applies migrations according to `AutoMigrate`, and builds the router. It starts no pgBase worker or HTTP listener, although the constructed `pgxpool` can run its own maintenance goroutines. An unreachable database or a behind/incompatible system schema fails construction when auto-migrate is false. `Start` activates schema/listener/cleaner lifecycle for a caller-owned server before `Handler` is served. `Run` activates the same components and owns the built-in HTTP server; an app cannot use both paths. Both mark readiness true only after startup. `Close` lowers readiness, drains, and is idempotent. The CLI passes a JSON logger writing to stderr; library callers that omit `Logger` receive the discard default and no global logger is mutated.

- [ ] **Step 6: Wire `pgbase serve` and verify lifecycle tests**

Use an ephemeral listener in tests, cancel the context, and assert the listener closes and the database pool rejects later acquisition.

Run: `go test ./pgbase ./internal/api ./internal/observability ./internal/cli`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add pgbase internal/api internal/observability internal/cli
git commit -m "feat: add server lifecycle and health endpoints"
```

### Task 5: Administrator Passwords, Tokens, Persistence, and Rate Limits

**Files:**
- Create: `internal/adminauth/types.go`
- Create: `internal/adminauth/password.go`
- Create: `internal/adminauth/password_test.go`
- Create: `internal/adminauth/token.go`
- Create: `internal/adminauth/token_test.go`
- Create: `internal/adminauth/store.go`
- Create: `internal/adminauth/store_test.go`
- Create: `internal/adminauth/service.go`
- Create: `internal/adminauth/service_test.go`
- Create: `internal/adminauth/cleaner.go`
- Create: `internal/adminauth/cleaner_test.go`

**Interfaces:**
- Consumes: `database.DBTX`, `database.Pool.InTx`, migrated administrator tables.
- Produces:

```go
type Administrator struct {
    ID        uuid.UUID
    Email     string
    Active    bool
    CreatedAt time.Time
    UpdatedAt time.Time
}

type Session struct {
    Administrator Administrator
    RawToken      string
    ExpiresAt     time.Time
}

type Passwords interface {
    Hash(password string) (string, error)
    Verify(encodedHash, password string) error
}

type Clock interface {
    Now() time.Time
}

type Service struct {
    pool        *database.Pool
    passwords   Passwords
    tokenSecret []byte
    setupToken  string
    sessionTTL  time.Duration
    clock       Clock
}
func NewService(pool *database.Pool, passwords Passwords, tokenSecret []byte, setupToken string, sessionTTL time.Duration, clock Clock) *Service

type Cleaner struct {
    pool  *database.Pool
    clock Clock
}
func NewCleaner(pool *database.Pool, clock Clock) *Cleaner
func (c *Cleaner) Run(context.Context) error

func (s *Service) Setup(ctx context.Context, setupToken, email, password, subject string) (Session, error)
func (s *Service) Login(ctx context.Context, email, password, subject string) (Session, error)
func (s *Service) Authenticate(ctx context.Context, rawToken string) (Administrator, error)
func (s *Service) Logout(ctx context.Context, rawToken string) error
```

- [ ] **Step 1: Write password hash round-trip and malformed-hash tests**

Use Argon2id parameters `time=3`, `memory=64*1024 KiB`, `threads=4`, 16-byte random salt, and 32-byte key. Store the standard self-describing string `$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>`. Verification must return a typed invalid-credentials error for malformed input and use constant-time comparison.

Administrator email input is trimmed, lowercased, limited to 254 ASCII bytes, and accepted only when `net/mail.ParseAddress` returns the same bare address. Passwords are 12 to 1,024 UTF-8 bytes with no composition rule; reject invalid UTF-8 before hashing.

- [ ] **Step 2: Run crypto tests and verify RED**

Run: `go test ./internal/adminauth -run 'TestPassword|TestToken' -v`

Expected: FAIL because password and token helpers do not exist.

- [ ] **Step 3: Implement password and 256-bit session token helpers**

Generate session and setup comparison material with `crypto/rand`. Persist only HMAC-SHA-256 of `"session\\x00" + rawSessionToken` using `Options.TokenSecret`; rotating that secret therefore invalidates existing sessions. Derive CSRF tokens as base64url HMAC-SHA-256 of `"csrf\\x00" + rawSessionToken` with the same key and a distinct domain prefix.

- [ ] **Step 4: Write administrator store integration tests**

Cover setup creation, normalized-email uniqueness, session authentication, expiration, logout, and shared rate limiting from two `Store` instances. Normalize administrator emails by trimming surrounding Unicode whitespace and lowercasing before validation.

- [ ] **Step 5: Implement store and service transactions**

`Setup` must acquire advisory transaction key `0x7067626173650003`, re-check that no administrator exists, compare the configured setup token in constant time, insert the administrator and session, and return the raw token only after commit. `Login` increments `admin_rate_limits` on failure and clears it on success. Key limits by HMAC of normalized email plus trusted client address; default to five failures per 15 minutes and a 15-minute block.

`Cleaner` runs hourly, acquires advisory key `0x7067626173650004`, and deletes at most 1,000 rows per transaction whose `updated_at` is older than 24 hours using a bounded CTE over `ctid`. Repeat until a batch deletes fewer than 1,000 rows, then wait for the next interval.

- [ ] **Step 6: Run package and race tests**

Run: `go test -race ./internal/adminauth -v`

Expected: PASS, including two concurrent setup calls where exactly one succeeds.

- [ ] **Step 7: Commit**

```bash
git add internal/adminauth
git commit -m "feat: add administrator authentication core"
```

### Task 6: Setup, Login, Session, CSRF, and Administrator HTTP APIs

**Files:**
- Create: `internal/api/admin_handlers.go`
- Create: `internal/api/admin_handlers_test.go`
- Create: `internal/api/admin_middleware.go`
- Create: `internal/api/admin_middleware_test.go`
- Create: `internal/api/client_address.go`
- Create: `internal/api/client_address_test.go`
- Create: `internal/api/admin_test_helpers_test.go`
- Modify: `internal/api/router.go`
- Modify: `pgbase/app.go`

**Interfaces:**
- Consumes: `adminauth.Service`, `pgbase.Options.PublicURL`, `pgbase.Options.TrustedProxies`.
- Produces:

```go
type AdminAuth interface {
    Setup(context.Context, string, string, string, string) (adminauth.Session, error)
    Login(context.Context, string, string, string) (adminauth.Session, error)
    Authenticate(context.Context, string) (adminauth.Administrator, error)
    Logout(context.Context, string) error
}

type AdminPrincipal struct {
    Administrator adminauth.Administrator
    RawSession     string
}
```

The test-helper file defines `fakeAdminAuth`, `validSession`, `mustURL`, and `testDependencies` with fixed UUIDs and UTC timestamps so handler tests contain no hidden database dependency.

- [ ] **Step 1: Write setup and login handler tests**

```go
func TestSetupSetsSecureAdminCookie(t *testing.T) {
    auth := &fakeAdminAuth{setupSession: validSession()}
    handler := NewRouter(testDependencies(auth, mustURL("https://pgbase.example")))
    request := httptest.NewRequest(http.MethodPost, "/api/admin/setup",
        strings.NewReader("{\"setupToken\":\"secret\",\"email\":\"admin@example.com\",\"password\":\"correct horse battery staple\"}"))
    request.Header.Set("Content-Type", "application/json")
    request.Header.Set("Origin", "https://pgbase.example")
    response := httptest.NewRecorder()
    handler.ServeHTTP(response, request)
    if response.Code != http.StatusCreated {
        t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
    }
    cookie := response.Result().Cookies()[0]
    if cookie.Name != "pgbase_admin" || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
        t.Fatalf("cookie=%+v", cookie)
    }
}
```

- [ ] **Step 2: Run handler tests and verify RED**

Run: `go test ./internal/api -run 'TestSetup|TestLogin|TestSession|TestLogout' -v`

Expected: FAIL because the administrator routes are not registered.

- [ ] **Step 3: Implement strict JSON handlers**

Reject unknown JSON fields and trailing JSON values. Setup returns 201, login 200, current session 200, and logout 204. Return the CSRF token in the setup, login, and current-session JSON body; never make the session cookie readable by JavaScript.

- [ ] **Step 4: Write origin, CSRF, and trusted-proxy tests**

Cover these cases:

- mutation with an absent or mismatched `Origin` returns `403 origin_rejected`;
- authenticated mutation without `X-PgBase-CSRF` returns `403 csrf_rejected`;
- an untrusted peer cannot influence the rate-limit subject using `X-Forwarded-For`;
- the nearest untrusted address is selected from a forwarded chain only when the direct peer is in `TrustedProxies`;
- `X-Forwarded-Proto` affects origin and secure-cookie decisions only for a trusted direct proxy and only when its single value is `http` or `https`.

- [ ] **Step 5: Implement administrator middleware**

Use `PublicURL` as the canonical origin in production. When it is absent, accept only the request host with the request scheme determined from the direct connection. Apply CSRF to `POST`, `PUT`, `PATCH`, and `DELETE` administrator-session requests. Setup has no session, so require origin plus the setup token instead.

- [ ] **Step 6: Wire the service and rate-limit cleaner into `App` and run tests**

Start `Cleaner.Run` with the application background group and stop it through the root context. Run: `go test -race ./internal/adminauth ./internal/api ./pgbase`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/api pgbase/app.go
git commit -m "feat: expose secure administrator sessions"
```

### Task 7: Collection Names, Types, and Deterministic Schema Planning

**Files:**
- Create: `internal/schema/types.go`
- Create: `internal/schema/names.go`
- Create: `internal/schema/names_test.go`
- Create: `internal/schema/defaults.go`
- Create: `internal/schema/defaults_test.go`
- Create: `internal/schema/planner.go`
- Create: `internal/schema/planner_test.go`

**Interfaces:**
- Produces:

```go
type FieldType string

const (
    TypeBoolean     FieldType = "boolean"
    TypeInteger     FieldType = "integer"
    TypeBigInt      FieldType = "bigint"
    TypeNumeric     FieldType = "numeric"
    TypeText        FieldType = "text"
    TypeTimestampTZ FieldType = "timestamptz"
    TypeUUID        FieldType = "uuid"
    TypeJSONB       FieldType = "jsonb"
)

type FieldInput struct {
    Name     string          `json:"name"`
    Type     FieldType       `json:"type"`
    Required bool            `json:"required"`
    Unique   bool            `json:"unique"`
    Indexed  bool            `json:"indexed"`
    Default  json.RawMessage `json:"default,omitempty"`
}

type CreateCollectionInput struct {
    Name   string       `json:"name"`
    Fields []FieldInput `json:"fields"`
}

type Field struct {
    ID         uuid.UUID
    Name       string
    ColumnName string
    Type       FieldType
    Required   bool
    Unique     bool
    Indexed    bool
    Default    json.RawMessage
    Ordinal    int
}

type Collection struct {
    ID        uuid.UUID
    Name      string
    TableName string
    Fields    []Field
    CreatedAt time.Time
}

type PlannedField struct {
    Name     string
    Type     FieldType
    Required bool
    Unique   bool
    Indexed  bool
    Default  json.RawMessage
    Ordinal  int
}

type PlannedCollection struct {
    Name      string
    TableName string
    Fields    []PlannedField
}

type Plan struct {
    Operation   string
    BaseVersion int64
    Checksum    string
    Definition  PlannedCollection
    Statements  []string
}

type Planner struct{}
func (Planner) PlanCreate(baseVersion int64, input CreateCollectionInput) (Plan, error)

type PlanBuilder interface {
    PlanCreate(baseVersion int64, input CreateCollectionInput) (Plan, error)
}
```

- [ ] **Step 1: Write name and reserved-word tests**

Test valid `posts` and `post_comments2`; reject uppercase, whitespace, quoting characters, names longer than 48 bytes, system field names, and the `pgbase_` prefix.

Run: `go test ./internal/schema -run TestName -v`

Expected: FAIL because validation is absent.

- [ ] **Step 2: Implement name validation**

Use the anchored regular expression from Global Constraints and a separate exact reserved-name map. Return field-addressable validation details such as `fields.2.name -> reserved_name`; do not return raw SQL errors.

- [ ] **Step 3: Write typed-default tests**

Test boolean, integer, decimal-string numeric, text with quotes, UTC timestamp, UUID, JSONB, and `{"kind":"now"}` for timestamptz. Reject NaN, infinity, numeric JSON numbers, invalid timestamps, invalid UUIDs, and every object that attempts to carry SQL.

- [ ] **Step 4: Implement canonical default rendering**

Normalize input into typed Go values, then render literals with PostgreSQL-safe canonical forms. Text and JSON strings must be quoted through a dedicated SQL-literal helper that doubles single quotes; `now` renders exactly `CURRENT_TIMESTAMP`. No caller-provided string may be copied as SQL syntax.

- [ ] **Step 5: Write planner determinism and injection tests**

```go
func TestPlanCreateIsDeterministic(t *testing.T) {
    input := CreateCollectionInput{
        Name: "posts",
        Fields: []FieldInput{
            {Name: "title", Type: TypeText, Required: true, Indexed: true},
            {Name: "views", Type: TypeBigInt, Default: json.RawMessage("\"0\"")},
        },
    }
    planner := Planner{}
    first, err := planner.PlanCreate(7, input)
    if err != nil {
        t.Fatal(err)
    }
    second, err := planner.PlanCreate(7, input)
    if err != nil {
        t.Fatal(err)
    }
    if first.Checksum != second.Checksum || !reflect.DeepEqual(first.Statements, second.Statements) {
        t.Fatalf("plans differ: %#v %#v", first, second)
    }
}
```

- [ ] **Step 6: Implement `CREATE TABLE` planning**

Map field types exactly to the spec. Always add `id uuid primary key`, `created_at timestamptz not null`, and `updated_at timestamptz not null`. Use application-generated IDs and timestamps, not database UUID extensions. Keep random metadata IDs out of `Plan`; generate deterministic index names from a truncated SHA-256 of normalized collection and field names. A unique field gets one unique constraint/index; if `Unique` and `Indexed` are both true, do not create a redundant ordinary index. Sort canonical metadata before the plan SHA-256 and include `BaseVersion` in the checksum.

- [ ] **Step 7: Run tests and commit**

Run: `go test ./internal/schema -v`

Expected: PASS.

```bash
git add internal/schema
git commit -m "feat: add deterministic collection planner"
```

### Task 8: Schema Catalog, Atomic Application, and Multi-Instance Synchronization

**Files:**
- Create: `internal/schema/catalog.go`
- Create: `internal/schema/cache.go`
- Create: `internal/schema/store.go`
- Create: `internal/schema/service.go`
- Create: `internal/schema/listener.go`
- Create: `internal/schema/service_test.go`
- Create: `internal/schema/listener_test.go`
- Create: `internal/api/schema_handlers.go`
- Create: `internal/api/schema_handlers_test.go`
- Modify: `internal/api/router.go`
- Modify: `pgbase/app.go`

**Interfaces:**
- Consumes: `schema.Planner.PlanCreate`, `database.Pool`, administrator middleware.
- Produces:

```go
type Catalog struct {
    Version     int64
    Collections map[string]Collection
}

type Cache struct {
    current atomic.Pointer[Catalog]
}
func (c *Cache) Load() Catalog
func (c *Cache) Replace(Catalog)

type Service struct {
    pool    *database.Pool
    cache   *Cache
    planner PlanBuilder
}
func NewService(pool *database.Pool, cache *Cache, planner PlanBuilder) *Service
func (s *Service) PlanCreate(ctx context.Context, input CreateCollectionInput) (Plan, error)
func (s *Service) ApplyCreate(ctx context.Context, input CreateCollectionInput, checksum string, administratorID uuid.UUID) (Collection, error)
func (s *Service) EnsureCurrent(ctx context.Context, tx database.DBTX) (Catalog, error)
func (s *Service) Catalog(ctx context.Context) (Catalog, error)

type Listener struct {
    pool  *database.Pool
    cache *Cache
    ready atomic.Bool
}
func (l *Listener) Run(context.Context) error
func (l *Listener) Ready(context.Context) error
```

- [ ] **Step 1: Write atomic-apply integration tests**

Cover successful creation, duplicate collection conflict, stale base version conflict, wrong checksum conflict, and a deliberately invalid DDL statement that leaves `collections`, `fields`, `schema_migrations`, and `schema_state` unchanged.

- [ ] **Step 2: Run the service tests and verify RED**

Run: `go test ./internal/schema -run 'TestApply|TestCatalog' -v`

Expected: FAIL because persistence and application are absent.

- [ ] **Step 3: Implement metadata persistence and atomic apply**

Within one transaction:

1. set the lock timeout;
2. acquire advisory key `0x7067626173650002`;
3. re-read `schema_state`;
4. regenerate the plan from stored state and compare checksum;
5. execute all DDL statements;
6. insert collection, fields, indexes, and schema-migration audit rows;
7. increment the singleton version;
8. call `SELECT pg_notify('pgbase_schema_changed', $1)` with the decimal version.

After commit, reload the local cache before returning.

Generate UUIDv7 metadata IDs only after the regenerated plan checksum matches. The IDs are persistence details and do not alter SQL or the plan checksum.

- [ ] **Step 4: Write listener reconnect and two-cache tests**

Start two services against one database. Apply a collection through the first, wait for the second cache to observe the new version, forcibly close the second listener connection, apply another collection, reconnect, and assert it reloads the missed version before reporting ready.

- [ ] **Step 5: Implement LISTEN/NOTIFY synchronization**

Acquire a dedicated pgx connection, issue `LISTEN pgbase_schema_changed`, and loop on `WaitForNotification`. On connection loss, mark listener readiness false, retry with bounded exponential backoff from 100 ms to 5 seconds, and reload `schema_state` plus the full catalog before marking ready. Payloads are hints only; always read the database version.

- [ ] **Step 6: Implement plan/apply/catalog administrator routes**

`POST /api/admin/collections/plan` returns the base version, checksum, normalized definition, and SQL. `POST /api/admin/collections/apply` requires the original input plus checksum and returns 201. `GET /api/admin/collections` returns the current version and definitions. All three require administrator authentication; both POST routes use the same Origin and CSRF mutation boundary.

- [ ] **Step 7: Run race and integration tests**

Run: `go test -race ./internal/schema ./internal/api ./pgbase -v`

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/schema internal/api pgbase/app.go
git commit -m "feat: apply and synchronize collection schemas"
```

### Task 9: Record Codecs, Signed Cursors, and CRUD SQL Compiler

**Files:**
- Create: `internal/query/types.go`
- Create: `internal/query/codec.go`
- Create: `internal/query/codec_test.go`
- Create: `internal/query/cursor.go`
- Create: `internal/query/cursor_test.go`
- Create: `internal/query/compiler.go`
- Create: `internal/query/compiler_test.go`

**Interfaces:**
- Consumes: immutable `schema.Collection` values from a pinned catalog version.
- Produces:

```go
type Statement struct {
    SQL  string
    Args []any
}

type PageCursor struct {
    Version   int
    CreatedAt time.Time
    ID        uuid.UUID
}

type CursorCodec struct {
    secret []byte
}
func NewCursorCodec(secret []byte) *CursorCodec
func (c *CursorCodec) Encode(PageCursor) (string, error)
func (c *CursorCodec) Decode(string) (PageCursor, error)

func CompileInsert(collection schema.Collection, input map[string]json.RawMessage, id uuid.UUID, now time.Time) (Statement, error)
func CompileSelectByID(collection schema.Collection, id uuid.UUID) Statement
func CompileUpdate(collection schema.Collection, id uuid.UUID, input map[string]json.RawMessage, now time.Time) (Statement, error)
func CompileDelete(collection schema.Collection, id uuid.UUID) Statement
func CompileList(collection schema.Collection, after *PageCursor, limit int) Statement

type RowScanner interface {
    Scan(...any) error
}

func EncodeRecord(collection schema.Collection, row RowScanner) (map[string]any, error)
```

- [ ] **Step 1: Write codec contract tests**

Use table-driven tests for every field type. Assert bigint and numeric inputs require JSON strings and outputs remain strings; timestamps normalize to UTC; UUIDs canonicalize; JSONB round-trips object, array, and scalar; unknown fields produce field-addressed errors. Every encoded record includes `id`, `created_at`, and `updated_at` with canonical UUID and UTC timestamp strings; tests compare decoded JSON objects rather than relying on object-key order.

- [ ] **Step 2: Implement the typed codec**

Parse with `math/big.Int`, `math/big.Rat` or pgx numeric codecs without converting through `float64`. Reject non-finite values and timestamps without offsets. Preserve JSONB using `json.RawMessage` after a full decoder validation.

- [ ] **Step 3: Write cursor tamper and version tests**

Encode canonical JSON containing `v`, `createdAt`, and `id`; append HMAC-SHA-256; base64url encode without padding. Test bit flips, truncation, unsupported version, invalid UUID, and non-UTC time.

- [ ] **Step 4: Implement `CursorCodec`**

Verify HMAC before JSON decoding and cap decoded payload at 512 bytes. Return a typed invalid-cursor error that the API maps to 400.

- [ ] **Step 5: Write compiler golden tests**

Assert exact SQL and arguments for insert, lookup, patch, delete, first page, and next page. Include malicious map keys and verify they are rejected before SQL construction. Empty PATCH must return validation failure.

- [ ] **Step 6: Implement the compiler**

Sort writable fields by catalog ordinal for deterministic SQL. Quote catalog identifiers through `pgx.Identifier.Sanitize`; use positional parameters for every value. Omit absent fields that have database defaults, reject absent required fields without defaults before execution, and bind explicit JSONB null only when the field type permits it. List ordering is always `created_at ASC, id ASC` and next-page predicate is `(created_at, id) > ($1, $2)`. Query `limit + 1` rows so the service can decide whether to emit a next cursor.

- [ ] **Step 7: Run tests and commit**

Run: `go test ./internal/query -v`

Expected: PASS.

```bash
git add internal/query
git commit -m "feat: compile safe record queries"
```

### Task 10: Administrator-Only Record Service and REST API

**Files:**
- Create: `internal/query/service.go`
- Create: `internal/query/service_test.go`
- Create: `internal/api/record_handlers.go`
- Create: `internal/api/record_handlers_test.go`
- Modify: `internal/api/router.go`
- Modify: `pgbase/app.go`

**Interfaces:**
- Consumes: `schema.Service.EnsureCurrent`, query compiler functions, `database.Pool`, administrator middleware.
- Produces:

```go
type RecordService interface {
    List(context.Context, string, string, int) (RecordPage, error)
    Get(context.Context, string, uuid.UUID) (map[string]any, error)
    Create(context.Context, string, map[string]json.RawMessage) (map[string]any, error)
    Update(context.Context, string, uuid.UUID, map[string]json.RawMessage) (map[string]any, error)
    Delete(context.Context, string, uuid.UUID) error
}

type RecordPage struct {
    Items      []map[string]any `json:"items"`
    NextCursor string           `json:"nextCursor,omitempty"`
}
```

- [ ] **Step 1: Write end-to-end record integration tests at the service boundary**

Create a collection through `schema.Service`, then cover create, get, partial update, stable two-page listing, delete, not found, required violation, unique conflict, unknown field, bigint precision, numeric precision, and JSONB scalar round-trip.

- [ ] **Step 2: Run the service test and verify RED**

Run: `go test ./internal/query -run TestRecordService -v`

Expected: FAIL because the service does not exist.

- [ ] **Step 3: Implement request transactions and catalog pinning**

For every operation, begin a transaction, call `EnsureCurrent` inside it, compile against the returned immutable collection, execute with request context, and commit only after successful decoding. If the database version differs, reload and compile once. Never retry an ambiguous transport or commit failure after SQL execution begins. The service may retry only PostgreSQL errors that guarantee the transaction was aborted (`0A000`, `40001`, `40P01`), with a finite capped-jitter budget.

- [ ] **Step 4: Write REST handler contract tests**

Cover:

- the five exact `/api/v1/collections/{collection}/records` routes;
- 401 without an administrator session;
- 403 without CSRF on mutations;
- 400 malformed JSON or cursor;
- 404 missing collection or record;
- 409 unique or schema conflict;
- 422 field and constraint validation;
- 503 dependency timeout;
- response `requestId` and no SQL details.

- [ ] **Step 5: Implement handlers and route registration**

List defaults to 50 and caps at 200. Parse record IDs with `uuid.Parse`. Require `application/json` for POST and PATCH. Return 201 for create, 200 for list/get/update, and 204 for delete.

- [ ] **Step 6: Run affected suites and race detection**

Run: `go test -race ./internal/query ./internal/api ./pgbase -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/query internal/api pgbase/app.go
git commit -m "feat: expose administrator record CRUD"
```

### Task 11: Administrator and Diagnostic CLI Commands

**Files:**
- Create: `internal/doctor/report.go`
- Create: `internal/doctor/checks.go`
- Create: `internal/doctor/checks_test.go`
- Create: `internal/cli/password.go`
- Create: `internal/cli/admin_test.go`
- Create: `internal/cli/doctor_test.go`
- Create: `internal/api/system_handler.go`
- Create: `internal/api/system_handler_test.go`
- Modify: `internal/adminauth/service.go`
- Modify: `internal/cli/run.go`
- Modify: `internal/api/router.go`
- Modify: `pgbase/app.go`

**Interfaces:**
- Consumes: administrator store, migration status, schema catalog store, PostgreSQL `information_schema`.
- Produces:

```go
type Finding struct {
    Severity string `json:"severity"`
    Code     string `json:"code"`
    Message  string `json:"message"`
}

type Report struct {
    OK       bool      `json:"ok"`
    Findings []Finding `json:"findings"`
}

func Run(ctx context.Context, pool *database.Pool, migrations *migrate.Runner) (Report, error)
func (s *adminauth.Service) CreateAdministrator(ctx context.Context, email, password string) (adminauth.Administrator, error)

type SystemStatus struct {
    Version           string `json:"version"`
    DatabaseReachable bool   `json:"databaseReachable"`
    MigrationCurrent int64  `json:"migrationCurrent"`
    MigrationLatest  int64  `json:"migrationLatest"`
    CatalogVersion   int64  `json:"catalogVersion"`
    ListenerReady    bool   `json:"listenerReady"`
}
```

- [ ] **Step 1: Add terminal password dependency and CLI tests**

Run: `go get golang.org/x/term@v0.45.0`.

Test `pgbase admin create --email admin@example.com` with an injected password reader. Require two equal non-empty password entries when stdin is a terminal. Also support `--password-stdin`, reading one newline-terminated value and never echoing it.

- [ ] **Step 2: Implement direct administrator creation**

Database access is the authorization boundary for the CLI. Normalize and validate the email, hash the password, insert a new active administrator, and map duplicate email to a clear conflict error. Do not create a browser session.

- [ ] **Step 3: Write doctor integration tests**

Create a healthy migrated database and assert `OK=true`. Then independently test checksum mismatch, missing physical collection table, unexpected physical column type, stale `schema_state`, and unreachable database.

- [ ] **Step 4: Implement read-only doctor checks**

Read only metadata, migration history, `information_schema.columns`, constraints, and indexes. Never repair state. Human output prints one finding per line; `--json` emits `Report`. Exit 0 for healthy, 1 for findings or connection failure, and 2 for invalid flags.

- [ ] **Step 5: Expose read-only system status**

Add authenticated `GET /api/admin/system`. Build the response from injected migration, catalog, listener, and database readiness providers plus `pgbase.Version`; do not execute diagnostics or mutate state from the HTTP endpoint.

- [ ] **Step 6: Run CLI, doctor, and API tests**

Run: `go test ./internal/doctor ./internal/cli ./internal/adminauth ./internal/api ./pgbase -v`

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add go.mod go.sum internal/doctor internal/cli internal/adminauth/service.go internal/api pgbase/app.go
git commit -m "feat: add administrator and diagnostic commands"
```

### Task 12: React Administrator Shell, Setup, and Login

**Files:**
- Create: `ui/admin/package.json`
- Create: `ui/admin/package-lock.json`
- Create: `ui/admin/tsconfig.json`
- Create: `ui/admin/tsconfig.app.json`
- Create: `ui/admin/vite.config.ts`
- Create: `ui/admin/index.html`
- Create: `ui/admin/src/main.tsx`
- Create: `ui/admin/src/app.tsx`
- Create: `ui/admin/src/api/client.ts`
- Create: `ui/admin/src/api/types.ts`
- Create: `ui/admin/src/auth/session.tsx`
- Create: `ui/admin/src/pages/setup-page.tsx`
- Create: `ui/admin/src/pages/login-page.tsx`
- Create: `ui/admin/src/styles.css`
- Create: `ui/admin/src/test/setup.ts`
- Create: `ui/admin/src/pages/setup-page.test.tsx`
- Create: `ui/admin/src/pages/login-page.test.tsx`

**Interfaces:**
- Consumes: `/api/admin/setup`, `/api/admin/auth/login`, `/api/admin/auth/logout`, `/api/admin/session`.
- Produces:

```ts
export interface Administrator {
  id: string;
  email: string;
  createdAt: string;
}

export interface SessionResponse {
  administrator: Administrator;
  csrfToken: string;
  expiresAt: string;
}

export interface SetupInput {
  setupToken: string;
  email: string;
  password: string;
}

export interface LoginInput {
  email: string;
  password: string;
}

export type SessionState =
  | { kind: "loading" }
  | { kind: "setup-required" }
  | { kind: "anonymous" }
  | { kind: "authenticated"; administrator: Administrator; csrfToken: string };

export class APIClient {
  getSession(signal?: AbortSignal): Promise<SessionResponse>;
  setup(input: SetupInput): Promise<SessionResponse>;
  login(input: LoginInput): Promise<SessionResponse>;
  logout(csrfToken: string): Promise<void>;
}
```

- [ ] **Step 1: Initialize exact frontend dependencies**

In `ui/admin`, install:

```bash
npm install react@19.2.8 react-dom@19.2.8
npm install --save-dev vite@8.2.2 @vitejs/plugin-react@6.1.0 typescript@7.0.2 vitest@4.1.11 @testing-library/react@16.3.2 @testing-library/user-event@14.6.6 @testing-library/jest-dom@7.0.1 @types/react@19.2.18 @types/react-dom@19.2.5 jsdom@30.0.1
```

Set `engines.node` to `>=24 <25`. Scripts are `dev`, `typecheck` (`tsc -b`), `test` (`vitest run`), and `build` (`tsc -b && vite build`). Configure Vite base `/admin/` and proxy `/api` to `http://localhost:8090` in development. Vite transpiles TypeScript but does not replace the explicit typecheck.

- [ ] **Step 2: Write API client tests with mocked fetch**

Verify `credentials: "same-origin"`, JSON content type, CSRF header on mutation, error-envelope decoding, abort propagation, and no retry of POST requests.

- [ ] **Step 3: Implement the API client**

Use one `request<T>` helper with strict status handling. Treat 404 from `/api/admin/session` with `setup_required` as setup state and 401 as anonymous. Preserve field-level `details` for forms.

- [ ] **Step 4: Write setup and login page tests**

Use accessible labels and user-event. Cover required fields, password confirmation, server validation details, disabled submit while pending, successful session transition, and password fields cleared after failure.

- [ ] **Step 5: Implement the administrator shell**

On first render, fetch the session exactly once. Render setup, login, or authenticated shell from `SessionState`. Keep the CSRF token in React memory only. Use browser history paths beneath `/admin/` without adding a router dependency.

- [ ] **Step 6: Verify frontend**

Run:

```bash
cd ui/admin
npm run typecheck
npm test
npm run build
```

Expected: all commands pass and `dist/index.html` exists.

- [ ] **Step 7: Commit**

```bash
git add ui/admin
git commit -m "feat: add administrator setup and login UI"
```

### Task 13: Collection, Record, and System Administrator UI with Binary Embedding

**Files:**
- Create: `ui/admin/src/api/collections.ts`
- Create: `ui/admin/src/api/records.ts`
- Create: `ui/admin/src/pages/collections-page.tsx`
- Create: `ui/admin/src/pages/collection-create-page.tsx`
- Create: `ui/admin/src/pages/records-page.tsx`
- Create: `ui/admin/src/pages/record-edit-page.tsx`
- Create: `ui/admin/src/pages/system-page.tsx`
- Create: `ui/admin/src/pages/collection-create-page.test.tsx`
- Create: `ui/admin/src/pages/records-page.test.tsx`
- Create: `ui/admin/embed.go`
- Create: `ui/admin/handler.go`
- Create: `ui/admin/handler_test.go`
- Modify: `ui/admin/src/app.tsx`
- Modify: `internal/api/router.go`
- Modify: `pgbase/app.go`

**Interfaces:**
- Consumes: administrator collection, record, and system APIs.
- Produces:

```go
//go:embed all:dist
var Dist embed.FS

func Handler() (http.Handler, error)
```

- [ ] **Step 1: Write collection preview/apply UI tests**

Build a collection with `title text required indexed` and `views bigint default "0"`. Assert preview displays normalized fields and SQL, apply sends the exact checksum and original input, a changed input invalidates the preview, and a 409 requires a fresh preview.

- [ ] **Step 2: Implement collection list and create screens**

Use a two-step form: validate/preview, then explicit apply. Destructive-edit controls do not exist. Display SQL as read-only text and never execute user-entered SQL.

- [ ] **Step 3: Write record list/edit tests**

Cover first and next cursor pages, create, partial update, delete confirmation, bigint/numeric string preservation, JSONB editor validation, 422 field errors, and 404 after another administrator deletes the record.

- [ ] **Step 4: Implement record and system screens**

The record form derives controls from field metadata and always preserves decimal strings. The system page shows binary version, database reachability, migration current/latest, catalog version, and listener readiness. Do not invent analytics cards or operational controls.

- [ ] **Step 5: Write static-handler tests**

Assert `/admin` redirects to `/admin/`; known assets have immutable cache headers; `/admin/collections` serves the SPA index; non-GET requests and every `/api/` path never fall back to the SPA; path traversal returns 404.

- [ ] **Step 6: Implement and register the embedded UI**

Vite base is `/admin/`. Build output remains tracked so a plain `go build` produces a working single binary. Use `fs.Sub(Dist, "dist")`; set HTML to no-cache and hashed assets to one-year immutable caching. Use `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self' data:; connect-src 'self'; font-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'`, plus `X-Content-Type-Options: nosniff` and `Referrer-Policy: no-referrer`.

- [ ] **Step 7: Run frontend and Go tests**

Run:

```bash
cd ui/admin
npm run typecheck
npm test
npm run build
cd ../..
go test ./ui/admin ./internal/api ./pgbase
```

Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add ui/admin internal/api/router.go pgbase/app.go
git commit -m "feat: embed collection and record admin UI"
```

### Task 14: Container Image, Docker Compose, and Black-Box E2E

**Files:**
- Create: `deployments/Dockerfile`
- Create: `deployments/docker-compose.yml`
- Create: `deployments/.env.example`
- Create: `tests/e2e/e2e_test.go`
- Create: `tests/e2e/client_test.go`
- Create: `tests/e2e/shutdown_test.go`
- Modify: `.gitignore`
- Modify: `internal/cli/run.go`
- Modify: `internal/cli/run_test.go`

**Interfaces:**
- Consumes: final binary, administrator and record HTTP contracts.
- Produces: a non-root single-binary container and reproducible local stack.

Every file under `tests/e2e` starts with `//go:build e2e`, so ordinary `go test ./...` does not start containers.

- [ ] **Step 1: Write a failing E2E smoke test**

The test uses only HTTP and Docker Compose. It must:

1. wait for readiness;
2. create the first administrator;
3. plan and apply a `posts` collection;
4. create two records;
5. page through them;
6. restart pgBase without restarting PostgreSQL;
7. log in again and verify both records remain.

Run: `go test -tags=e2e ./tests/e2e -run TestBootstrapCRUDAndRestart -v`

Expected: FAIL because the container stack does not exist.

- [ ] **Step 2: Create the multi-stage Dockerfile**

Use `node:24.18.0-bookworm-slim` to run `npm ci && npm run build`, `golang:1.26.7-bookworm` to run tests and build with `CGO_ENABLED=0`, and a non-root distroless runtime. Build with `-trimpath` and `-ldflags "-s -w -X github.com/yakisoba0728/PgBase/pgbase.Version=$VERSION"`, where `VERSION` is a non-secret build argument defaulting to `dev`. Copy only the binary and CA certificates. Set the OCI source label to the Git remote and expose 8090. Do not bake secrets into layers.

- [ ] **Step 3: Create the development Compose stack**

Use `postgres:16.13-bookworm` with a named volume, a PostgreSQL health check, pgBase dependency on healthy PostgreSQL, `PGBASE_AUTO_MIGRATE=true`, a documented generated setup token and token secret, and pgBase health check against `/health/ready`. Bind only 8090 and the optional local PostgreSQL port. Run pgBase with a read-only root filesystem, a small `/tmp` tmpfs, dropped Linux capabilities, and `no-new-privileges`.

- [ ] **Step 4: Add the hidden in-image healthcheck command**

Implement `pgbase healthcheck --url http://127.0.0.1:8090/health/ready` with a two-second default timeout and no redirects. Treat only HTTP 200 as healthy. Keep it out of ordinary user-facing command help, but test success, timeout, redirect, non-200, and invalid URL. The container invokes the same pgBase binary and requires neither curl nor a shell.

- [ ] **Step 5: Add persistence, multi-instance, and graceful-shutdown E2E tests**

Run a second pgBase container against the same database on another host port. Apply a collection through instance A and observe it through B. Send SIGTERM to A, assert readiness fails before exit, new requests stop, and a deliberately delayed in-flight request finishes within the configured grace period.

- [ ] **Step 6: Run the complete container test**

Run:

```bash
docker compose -f deployments/docker-compose.yml up -d --build
go test -tags=e2e ./tests/e2e -v
docker compose -f deployments/docker-compose.yml down
```

Expected: PASS. Named database volumes remain unless the caller explicitly adds `--volumes`; tests use a uniquely named Compose project and remove only that project's disposable volumes in registered cleanup.

- [ ] **Step 7: Commit**

```bash
git add deployments tests/e2e .gitignore internal/cli
git commit -m "feat: package pgbase for Docker"
```

### Task 15: Security Regression Suite, Performance Baseline, CI, and Operator Docs

**Files:**
- Create: `internal/api/security_test.go`
- Create: `internal/query/benchmark_test.go`
- Create: `.github/workflows/backend.yml`
- Create: `.github/workflows/frontend.yml`
- Create: `.github/workflows/e2e.yml`
- Create: `README.md`
- Create: `docs/quickstart.md`
- Create: `docs/deployment.md`
- Create: `docs/backup-restore.md`
- Create: `docs/upgrades.md`
- Create: `SECURITY.md`

**Interfaces:**
- Consumes: every Production Kernel command, API, package, UI, and container.
- Produces: release gates and the documented 10-minute operator path.

- [ ] **Step 1: Add explicit security regression tests**

Use generated cases for SQL values, identifiers, JSONB, cursor payloads, forwarded addresses, origins, CSRF, cookies, oversized bodies, malformed password hashes, setup reuse, and log redaction. Every case asserts both the status/code and absence of the injected secret or SQL fragment from response and captured logs.

- [ ] **Step 2: Add deterministic compiler and CRUD benchmarks**

```go
func BenchmarkCompileInsert20Fields(b *testing.B) {
    collection, input := benchmarkCollectionAndInput(20)
    id := uuid.MustParse("018f1f09-7b44-7cc1-98e8-6f9a46a17d21")
    now := time.Date(2026, 8, 28, 0, 0, 0, 0, time.UTC)
    b.ReportAllocs()
    for range b.N {
        if _, err := CompileInsert(collection, input, id, now); err != nil {
            b.Fatal(err)
        }
    }
}
```

Add an integration benchmark for create/get/list using a fixed 20-field collection and 10,000 seeded rows. Record results as CI artifacts; do not fail on an invented throughput target. Add a later regression threshold only after three stable main-branch baselines.

- [ ] **Step 3: Create backend CI**

On Linux, use Go 1.26.7 and PostgreSQL service matrix 16.13, 17, and 18. Run:

```bash
test -z "$(gofmt -l .)"
go vet ./...
go test -race ./...
go test ./internal/query -run '^$' -bench . -benchmem
```

Upload benchmark text and test logs on failure.

- [ ] **Step 4: Create frontend CI**

Use Node 24, `npm ci`, `npm run typecheck`, `npm test`, and `npm run build`. Verify `git diff --exit-code -- ui/admin/dist` so the embedded output matches source.

- [ ] **Step 5: Create E2E CI**

Build the image, start the uniquely named Compose project, run black-box tests, capture pgBase and PostgreSQL logs on failure, and always remove the CI project's containers and volumes. Verify the image runs as a non-root UID and has no shell requirement.

- [ ] **Step 6: Write operator documentation**

`README.md` links to the approved design and plan, states the project is pre-v0.1, and gives the shortest quickstart. `docs/quickstart.md` covers secret generation, Compose startup, browser setup, collection creation, and first REST call. Deployment documents explicit migrations, TLS proxying, trusted proxies, external PostgreSQL, readiness, SIGTERM, and secret rotation. Backup/restore uses PostgreSQL PITR or `pg_dump`/`pg_restore`; upgrades require backup, `migrate status`, `migrate up`, then server rollout. `SECURITY.md` documents private reporting and supported versions without inventing a contact address; direct reporters to GitHub private vulnerability reporting for this repository.

- [ ] **Step 7: Run the release-candidate verification**

Run:

```bash
go vet ./...
go test -race ./...
cd ui/admin
npm ci
npm run typecheck
npm test
npm run build
cd ../..
docker compose -f deployments/docker-compose.yml up -d --build
go test -tags=e2e ./tests/e2e -v
docker compose -f deployments/docker-compose.yml down
git diff --check
git status --short
```

Expected: all verification commands pass. The only working-tree changes before the final commit are the planned documentation, workflow, test, and rebuilt `ui/admin/dist` files.

- [ ] **Step 8: Commit**

```bash
git add internal/api/security_test.go internal/query/benchmark_test.go .github README.md docs SECURITY.md ui/admin/dist
git commit -m "ci: gate the production kernel release"
```

## Final Acceptance Run

After all task commits:

- [ ] Run `go test -race ./...`.
- [ ] Run `go vet ./...`.
- [ ] Run `npm ci && npm run typecheck && npm test && npm run build` in `ui/admin`.
- [ ] Run `go test -tags=e2e ./tests/e2e -v` against PostgreSQL 16.13.
- [ ] Run the two-instance schema synchronization test.
- [ ] Run `pgbase doctor --json` against the E2E database and require `"ok": true`.
- [ ] Run `git diff --check` and require a clean `git status --short`.

## Primary Technical References

- [Go release history](https://go.dev/doc/devel/release)
- [pgxpool package documentation](https://pkg.go.dev/github.com/jackc/pgx/v5/pgxpool)
- [Argon2 package documentation](https://pkg.go.dev/golang.org/x/crypto/argon2)
- [PostgreSQL 16 advisory lock functions](https://www.postgresql.org/docs/16/functions-admin.html)
- [PostgreSQL 16 LISTEN](https://www.postgresql.org/docs/16/sql-listen.html)
- [Vite getting started](https://vite.dev/guide/)
- [Vite TypeScript behavior](https://vite.dev/guide/features)
- [React versions](https://react.dev/versions)
