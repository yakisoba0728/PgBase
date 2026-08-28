// Package cli provides command dispatch for the pgbase executable.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/config"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/doctor"
	"github.com/webisnoteasy/PgBase/internal/migrate"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/pgbase"
)

// Command is a pgbase CLI command handler.
type Command func(context.Context, []string, func(string) (string, bool), io.Writer, io.Writer) error

// CommandSet contains the handlers exposed by the pgbase command line.
type CommandSet struct {
	Serve       Command
	Migrate     Command
	Admin       Command
	Doctor      Command
	Healthcheck Command
}

// DefaultCommands returns the command handlers wired into the production binary.
// Commands are added by their owning packages in later implementation tasks.
func DefaultCommands() CommandSet {
	return CommandSet{Serve: runServe, Migrate: runMigrate, Admin: runAdmin, Doctor: runDoctor, Healthcheck: runHealthcheck}
}

func runHealthcheck(ctx context.Context, args []string, _ func(string) (string, bool), _, _ io.Writer) error {
	flags := flag.NewFlagSet("healthcheck", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	endpoint := flags.String("url", "", "")
	timeout := flags.Duration("timeout", 2*time.Second, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || *endpoint == "" {
		return configurationError{err: errors.New("invalid healthcheck usage"), message: "usage: pgbase healthcheck --url URL [--timeout DURATION]"}
	}
	parsed, err := url.Parse(*endpoint)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" || parsed.User != nil || parsed.Opaque != "" || parsed.Fragment != "" {
		return configurationError{err: errors.New("invalid healthcheck URL"), message: "invalid healthcheck URL"}
	}
	if *timeout <= 0 {
		return configurationError{err: errors.New("invalid healthcheck timeout"), message: "invalid healthcheck timeout"}
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return configurationError{err: errors.New("invalid healthcheck URL"), message: "invalid healthcheck URL"}
	}
	client := &http.Client{
		Timeout: *timeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("healthcheck request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("healthcheck returned HTTP %d", response.StatusCode)
	}
	return nil
}

func runAdmin(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return configurationError{err: errors.New("invalid admin usage"), message: "usage: pgbase admin <create|reset-password> --email EMAIL [--password-stdin]"}
	}
	operation := args[0]
	if operation != "create" && operation != "reset-password" {
		return configurationError{err: errors.New("invalid admin usage"), message: "usage: pgbase admin <create|reset-password> --email EMAIL [--password-stdin]"}
	}
	usage := "usage: pgbase admin " + operation + " --email EMAIL [--password-stdin]"
	flags := flag.NewFlagSet("admin "+operation, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	email := flags.String("email", "", "")
	passwordFromStdin := flags.Bool("password-stdin", false, "")
	if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || *email == "" {
		return configurationError{err: errors.New("invalid admin usage"), message: usage}
	}
	if _, err := adminauth.NormalizeEmail(*email); err != nil {
		return configurationError{err: err, message: "invalid administrator email"}
	}
	password, err := readAdministratorPassword(commandStdin, stderr, *passwordFromStdin)
	if err != nil {
		return err
	}
	options, err := config.LoadEnv(lookup)
	if err != nil {
		return configurationError{err: err}
	}
	if _, err := pgxpool.ParseConfig(options.DatabaseURL); err != nil {
		return configurationError{err: fmt.Errorf("parse PostgreSQL pool configuration: %w", err)}
	}
	pool, err := database.Open(ctx, options.DatabaseURL, options.StatementTimeout)
	if err != nil {
		return migrationOperationError(options.Logger, "open PostgreSQL pool", err)
	}
	defer pool.Close()
	service := adminauth.NewService(pool, adminauth.NewArgon2idPasswords(), options.TokenSecret, options.SetupToken, options.AdminSessionTTL, nil)
	var administrator adminauth.Administrator
	if operation == "create" {
		administrator, err = service.CreateAdministrator(ctx, *email, password)
	} else {
		administrator, err = service.ResetAdministratorPassword(ctx, *email, password)
	}
	if errors.Is(err, adminauth.ErrValidation) {
		return configurationError{err: err, message: "invalid administrator credentials"}
	}
	if errors.Is(err, adminauth.ErrConflict) {
		return migrationOperationError(options.Logger, "create administrator", err)
	}
	if err != nil {
		return migrationOperationError(options.Logger, "create administrator", err)
	}
	message := "administrator created"
	if operation == "reset-password" {
		message = "administrator password reset"
	}
	_, err = fmt.Fprintf(stdout, "%s: %s\n", message, administrator.Email)
	return err
}

func runDoctor(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, _ io.Writer) error {
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonOutput := flags.Bool("json", false, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return configurationError{err: errors.New("invalid doctor usage"), message: "usage: pgbase doctor [--json]"}
	}
	options, err := config.LoadEnv(lookup)
	if err != nil {
		configErr := configurationError{err: err}
		return emitDoctorFailure(stdout, *jsonOutput, "configuration_invalid", "pgBase configuration is invalid", configErr)
	}
	if _, err := pgxpool.ParseConfig(options.DatabaseURL); err != nil {
		configErr := configurationError{err: fmt.Errorf("parse PostgreSQL pool configuration: %w", err)}
		return emitDoctorFailure(stdout, *jsonOutput, "configuration_invalid", "pgBase configuration is invalid", configErr)
	}
	pool, err := database.Open(ctx, options.DatabaseURL, options.StatementTimeout)
	if err != nil {
		operationErr := migrationOperationError(options.Logger, "open PostgreSQL pool", err)
		return emitDoctorFailure(stdout, *jsonOutput, "database_unreachable", "PostgreSQL is unreachable", operationErr)
	}
	defer pool.Close()
	report, err := doctor.Run(ctx, pool, migrate.New(pool, options.Logger))
	if err != nil && len(report.Findings) == 0 {
		report.Findings = append(report.Findings, doctor.Finding{Severity: "error", Code: "diagnostic_failed", Message: "Database diagnostics could not be completed"})
		report.OK = false
	}
	if writeErr := writeDoctorReport(stdout, *jsonOutput, report); writeErr != nil {
		return writeErr
	}
	if err != nil {
		return migrationOperationError(options.Logger, "run diagnostics", err)
	}
	if !report.OK {
		return errors.New("diagnostic findings")
	}
	return nil
}

func emitDoctorFailure(stdout io.Writer, jsonOutput bool, code, message string, operationErr error) error {
	report := doctor.Report{OK: false, Findings: []doctor.Finding{{Severity: "error", Code: code, Message: message}}}
	if err := writeDoctorReport(stdout, jsonOutput, report); err != nil {
		return err
	}
	return operationErr
}

func writeDoctorReport(stdout io.Writer, jsonOutput bool, report doctor.Report) error {
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(report)
	}
	for _, finding := range report.Findings {
		if _, err := fmt.Fprintf(stdout, "%s %s: %s\n", finding.Severity, finding.Code, finding.Message); err != nil {
			return err
		}
	}
	return nil
}

// Run dispatches a command and returns its process exit code.
func Run(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, stderr io.Writer, commands CommandSet) int {
	if len(args) == 0 {
		printUsage(stderr)
		return 2
	}

	name := args[0]
	var command Command
	switch name {
	case "serve":
		command = commands.Serve
	case "migrate":
		command = commands.Migrate
	case "admin":
		command = commands.Admin
	case "doctor":
		command = commands.Doctor
	case "healthcheck":
		command = commands.Healthcheck
	default:
		fmt.Fprintf(stderr, "pgbase: unknown command %q\n", name)
		printUsage(stderr)
		return 2
	}

	if command == nil {
		fmt.Fprintf(stderr, "pgbase: command %q is unavailable\n", name)
		return 1
	}
	if err := command(ctx, args[1:], lookup, stdout, stderr); err != nil {
		var configErr configurationError
		if errors.As(err, &configErr) {
			message := configErr.message
			if message == "" {
				message = "invalid configuration"
			}
			fmt.Fprintf(stderr, "pgbase: %s\n", message)
			return 2
		}
		fmt.Fprintln(stderr, "pgbase: command failed")
		return 1
	}
	return 0
}

type configurationError struct {
	err     error
	message string
}

func (e configurationError) Error() string { return e.err.Error() }

func (e configurationError) Unwrap() error { return e.err }

func runServe(ctx context.Context, args []string, lookup func(string) (string, bool), _ io.Writer, stderr io.Writer) error {
	if len(args) != 0 {
		return configurationError{err: errors.New("invalid serve usage"), message: "usage: pgbase serve"}
	}
	options, err := config.LoadEnv(lookup)
	if err != nil {
		return configurationError{err: err}
	}
	options.Logger = observability.NewJSONLogger(stderr)
	app, err := pgbase.New(ctx, options)
	if err != nil {
		options.Logger.Error("construct pgbase application", "error", err)
		return fmt.Errorf("construct pgbase application: %w", err)
	}
	if err := app.Run(ctx); err != nil {
		options.Logger.Error("run pgbase application", "error", err)
		return fmt.Errorf("run pgbase application: %w", err)
	}
	return nil
}

func runMigrate(ctx context.Context, args []string, lookup func(string) (string, bool), stdout, _ io.Writer) error {
	if len(args) != 1 || (args[0] != "status" && args[0] != "up") {
		return configurationError{err: errors.New("invalid migrate usage"), message: "usage: pgbase migrate <status|up>"}
	}

	options, err := config.LoadEnv(lookup)
	if err != nil {
		return configurationError{err: err}
	}
	if _, err := pgxpool.ParseConfig(options.DatabaseURL); err != nil {
		return configurationError{err: fmt.Errorf("parse PostgreSQL pool configuration: %w", err)}
	}
	pool, err := database.Open(ctx, options.DatabaseURL, options.StatementTimeout)
	if err != nil {
		return migrationOperationError(options.Logger, "open PostgreSQL pool", err)
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return migrationOperationError(options.Logger, "ping PostgreSQL pool", err)
	}

	runner := migrate.New(pool, options.Logger)
	if args[0] == "up" {
		if err := runner.Up(ctx); err != nil {
			return migrationOperationError(options.Logger, "apply system migrations", err)
		}
	}
	status, err := runner.Status(ctx)
	if err != nil {
		return migrationOperationError(options.Logger, "read system migration status", err)
	}
	_, err = fmt.Fprintf(stdout, "system migrations: current=%d latest=%d dirty=%t\n", status.Current, status.Latest, status.Dirty)
	if err != nil {
		return migrationOperationError(options.Logger, "write migration status", err)
	}
	return nil
}

func migrationOperationError(logger *slog.Logger, message string, err error) error {
	logger.Error(message, "error", err)
	return fmt.Errorf("%s: %w", message, err)
}

func printUsage(stderr io.Writer) {
	fmt.Fprintln(stderr, "usage: pgbase <serve, migrate, admin, doctor> [arguments]")
}
