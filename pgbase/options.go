package pgbase

import (
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"net/url"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/webisnoteasy/PgBase/internal/api"
)

const (
	maximumAdminSessionTTL = 12 * time.Hour
	// The setup endpoint has a 16 KiB body ceiling. Capping the configured
	// token at 1 KiB leaves room for a maximum-size password, email, and JSON
	// escaping, so every configuration accepted here remains usable over HTTP.
	maximumSetupTokenBytes = 1 << 10
)

// maximumStatementTimeout is PostgreSQL's own statement_timeout ceiling: the
// GUC is a signed 32-bit millisecond count. internal/database.postgresDuration
// rounds a partial millisecond up, so anything above this renders a value
// set_config rejects and every pooled connection fails to initialize instead of
// the configuration being refused here with a name the operator can act on.
const maximumStatementTimeout = 2147483647 * time.Millisecond

// The schema apply window an operator may choose. The floor keeps a value that
// could not finish a trivial DDL from being mistaken for a working
// configuration; the ceiling keeps a stuck apply from holding a schema lock
// for an unbounded time.
const (
	minimumSchemaApplyTimeout = 30 * time.Second
	maximumSchemaApplyTimeout = time.Hour
)

type Options struct {
	// CustomRoutes installs up to 64 application handlers with explicit access.
	CustomRoutes []CustomRoute
	// JavaScript enables trusted local scripts after validating an immutable
	// source manifest. A nil value keeps the runtime entirely disabled.
	JavaScript *JavaScriptOptions
	// SQLConsole enables the administrator SQL console with dedicated,
	// least-privileged database roles. A nil value keeps its routes disabled.
	SQLConsole *SQLConsoleOptions
	Backups    BackupOptions
	// Hooks is shared with the application for runtime registration.
	Hooks          *HookRegistry
	Operations     OperationsOptions
	Files          FileOptions
	OAuthProviders []OAuthProviderOptions
	OAuthRedirects []string
	MutationHooks  []MutationHook
	SMTP           SMTPOptions
	DatabaseURL    string
	// UserRegistrationEnabled permits public self-registration; false by default.
	UserRegistrationEnabled bool
	// Jobs enables registered durable scheduled jobs. Jobs are disabled by
	// default so a process that has not opted in never claims shared work.
	JobsEnabled bool
	// Jobs contains named handlers registered before App startup.
	Jobs []ScheduledJob
	// AllowInsecureDatabase permits an unverified or plaintext PostgreSQL
	// connection. It is only for an explicitly trusted local development
	// network such as the bundled Compose stack.
	AllowInsecureDatabase bool
	HTTPAddress           string
	// PublicDir is an absolute directory of user-owned static files. An empty
	// value leaves user-site hosting disabled.
	PublicDir string
	// PublicSPAFallback serves PublicDir/index.html for navigation-shaped
	// missing paths. Missing asset-shaped paths remain 404.
	PublicSPAFallback bool
	PublicURL         *url.URL
	SetupToken        string
	TokenSecret       []byte
	// MetricsToken enables the protected Prometheus-compatible GET /metrics
	// endpoint when non-empty. It is copied during normalization so callers
	// cannot change the live credential after constructing an App.
	MetricsToken []byte
	AutoMigrate  bool
	// UserMigrationAutoRecordDirectory opts administrator schema mutations into
	// durable Go migration source recording in this directory.
	UserMigrationAutoRecordDirectory string
	TrustedProxies                   []netip.Prefix
	CORSAllowedOrigins               []string
	AdminSessionTTL                  time.Duration
	StatementTimeout                 time.Duration
	ShutdownTimeout                  time.Duration
	// SchemaApplyTimeout bounds one schema apply, including its DDL. It is a
	// separate axis from StatementTimeout because index and constraint builds
	// legitimately outlive an ordinary statement, and from the server write
	// timeout because the response still has to be delivered afterwards.
	SchemaApplyTimeout time.Duration
	Logger             *slog.Logger
}

func (o Options) Normalized() (Options, error) {
	files, err := o.Files.normalized()
	if err != nil {
		return Options{}, err
	}
	o.Files = files
	if o.HTTPAddress == "" {
		o.HTTPAddress = "127.0.0.1:8090"
	}
	if o.PublicDir != "" && !filepath.IsAbs(o.PublicDir) {
		return Options{}, fmt.Errorf("PublicDir: must be an absolute path")
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
	if o.SchemaApplyTimeout == 0 {
		o.SchemaApplyTimeout = 10 * time.Minute
	}
	if o.Logger == nil {
		o.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	if o.JavaScript != nil {
		javascript, err := o.JavaScript.normalized()
		if err != nil {
			return Options{}, err
		}
		o.JavaScript = &javascript
	}
	if o.SQLConsole != nil {
		sqlConsole, err := o.SQLConsole.normalized(o.AllowInsecureDatabase)
		if err != nil {
			return Options{}, err
		}
		o.SQLConsole = &sqlConsole
	}
	if err := validateOptions(o); err != nil {
		return Options{}, err
	}
	for _, hook := range o.MutationHooks {
		if hook == nil {
			return Options{}, fmt.Errorf("MutationHooks: nil hook")
		}
	}
	o.MutationHooks = append([]MutationHook(nil), o.MutationHooks...)
	o.CustomRoutes = append([]CustomRoute(nil), o.CustomRoutes...)
	o.OAuthProviders = append([]OAuthProviderOptions(nil), o.OAuthProviders...)
	for i := range o.OAuthProviders {
		o.OAuthProviders[i].Scopes = append([]string(nil), o.OAuthProviders[i].Scopes...)
	}
	o.OAuthRedirects = append([]string(nil), o.OAuthRedirects...)
	o.Jobs = append([]ScheduledJob(nil), o.Jobs...)
	o.TokenSecret = append([]byte(nil), o.TokenSecret...)
	o.MetricsToken = append([]byte(nil), o.MetricsToken...)
	return o, nil
}

func validateOptions(o Options) error {
	if err := api.ValidateCustomRoutes(o.CustomRoutes); err != nil {
		return err
	}
	if err := validateScheduledJobs(o.Jobs); err != nil {
		return err
	}
	if err := o.validateOAuth(); err != nil {
		return err
	}
	if err := o.validateEmail(); err != nil {
		return err
	}
	if o.JavaScript != nil {
		if o.JavaScript.Capabilities["files"] && o.Files.Driver == "" {
			return fmt.Errorf("JavaScript.Capabilities: files requires Files.Driver")
		}
		if o.JavaScript.Capabilities["mail"] && o.SMTP.Address == "" {
			return fmt.Errorf("JavaScript.Capabilities: mail requires SMTP configuration")
		}
	}
	if err := validateDatabaseURL(o.DatabaseURL, o.AllowInsecureDatabase); err != nil {
		return fmt.Errorf("DatabaseURL: %w", err)
	}
	if len(o.TokenSecret) < 32 {
		return fmt.Errorf("TokenSecret: must contain at least 32 bytes")
	}
	if len(o.MetricsToken) != 0 {
		if len(o.MetricsToken) < 32 || len(o.MetricsToken) > 256 {
			return fmt.Errorf("MetricsToken: must contain between 32 and 256 bytes")
		}
		for _, character := range o.MetricsToken {
			switch {
			case character >= 'a' && character <= 'z':
			case character >= 'A' && character <= 'Z':
			case character >= '0' && character <= '9':
			case character == '-', character == '_', character == '.', character == '~':
			default:
				return fmt.Errorf("MetricsToken: must use bearer-token-safe characters")
			}
		}
	}
	if o.SetupToken != "" {
		if !utf8.ValidString(o.SetupToken) {
			return fmt.Errorf("SetupToken: must be valid UTF-8")
		}
		if utf8.RuneCountInString(o.SetupToken) < 32 {
			return fmt.Errorf("SetupToken: must contain at least 32 characters")
		}
		if len(o.SetupToken) > maximumSetupTokenBytes {
			return fmt.Errorf("SetupToken: must contain no more than %d bytes", maximumSetupTokenBytes)
		}
	}
	if o.PublicURL != nil {
		if err := validateOrigin(o.PublicURL); err != nil {
			return fmt.Errorf("PublicURL: %w", err)
		}
		if o.PublicURL.Scheme == "http" && !loopbackHostname(o.PublicURL.Hostname()) {
			return fmt.Errorf("PublicURL: plaintext HTTP is allowed only for a loopback host")
		}
	}
	for _, origin := range o.CORSAllowedOrigins {
		parsed, err := url.Parse(origin)
		if err != nil {
			return fmt.Errorf("CORSAllowedOrigins: %w", err)
		}
		if err := validateOrigin(parsed); err != nil {
			return fmt.Errorf("CORSAllowedOrigins: %w", err)
		}
	}
	for _, prefix := range o.TrustedProxies {
		if !prefix.IsValid() || prefix != prefix.Masked() || prefix.Bits() == 0 {
			return fmt.Errorf("TrustedProxies: must contain valid CIDR prefixes")
		}
	}
	if o.AdminSessionTTL <= 0 || o.AdminSessionTTL > maximumAdminSessionTTL {
		return fmt.Errorf("AdminSessionTTL: must be positive and no greater than 12h")
	}
	if o.StatementTimeout <= 0 || o.StatementTimeout > maximumStatementTimeout {
		return fmt.Errorf("StatementTimeout: must be positive and no greater than 2147483647ms")
	}
	if o.ShutdownTimeout <= 0 {
		return fmt.Errorf("ShutdownTimeout: must be positive")
	}
	if o.SchemaApplyTimeout < minimumSchemaApplyTimeout || o.SchemaApplyTimeout > maximumSchemaApplyTimeout {
		return fmt.Errorf("SchemaApplyTimeout: must be between %s and %s",
			minimumSchemaApplyTimeout, maximumSchemaApplyTimeout)
	}
	if len(o.UserMigrationAutoRecordDirectory) > 4096 || strings.IndexByte(o.UserMigrationAutoRecordDirectory, 0) >= 0 {
		return fmt.Errorf("UserMigrationAutoRecordDirectory: invalid path")
	}
	return nil
}

func validateDatabaseURL(value string, allowInsecure bool) error {
	parsed, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("must be a valid PostgreSQL URL")
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return fmt.Errorf("must use postgres or postgresql scheme")
	}
	hosts, ok := databaseURLHosts(parsed)
	if !ok {
		return fmt.Errorf("must include a host")
	}
	if !allowInsecure && parsed.Query().Get("sslmode") != "verify-full" {
		for _, host := range hosts {
			if !strings.HasPrefix(host, "/") && !loopbackHostname(host) {
				return fmt.Errorf("remote PostgreSQL must use sslmode=verify-full")
			}
		}
	}
	return nil
}

func databaseURLHosts(parsed *url.URL) ([]string, bool) {
	queryHosts, specified := parsed.Query()["host"]
	if !specified {
		if parsed.Host == "" || parsed.Hostname() == "" {
			return nil, false
		}
		return []string{parsed.Hostname()}, true
	}
	if len(queryHosts) != 1 {
		return nil, false
	}
	hosts := strings.Split(queryHosts[0], ",")
	for index := range hosts {
		hosts[index] = strings.TrimSpace(hosts[index])
		if hosts[index] == "" {
			return nil, false
		}
	}
	return hosts, true
}

func loopbackHostname(hostname string) bool {
	hostname = strings.TrimSuffix(strings.ToLower(hostname), ".")
	if hostname == "localhost" || strings.HasSuffix(hostname, ".localhost") {
		return true
	}
	address := net.ParseIP(hostname)
	return address != nil && address.IsLoopback()
}

func validateOrigin(origin *url.URL) error {
	if origin == nil {
		return fmt.Errorf("is required")
	}
	if origin.Scheme != "http" && origin.Scheme != "https" {
		return fmt.Errorf("must use http or https")
	}
	if origin.Host == "" || origin.User != nil || origin.Opaque != "" {
		return fmt.Errorf("must be an origin")
	}
	if origin.Path != "" || origin.RawPath != "" || origin.ForceQuery || origin.RawQuery != "" || origin.Fragment != "" {
		return fmt.Errorf("must not include a path, query, or fragment")
	}
	if strings.Contains(origin.Host, "*") {
		return fmt.Errorf("must not contain a wildcard")
	}
	return nil
}
