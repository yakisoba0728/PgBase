package pgbase

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

// JavaScriptOptions enables trusted local JavaScript extensions. Scripts run
// in process and are not an operating-system sandbox for hostile tenants.
type JavaScriptOptions struct {
	Directory    string
	Capabilities map[string]bool
	// JobServiceSubjects binds each manifest job to an operator-provisioned
	// service token identity. The script cannot select or change this subject.
	JobServiceSubjects map[string]uuid.UUID
	Concurrency        int
	Queue              int
	AdmissionTimeout   time.Duration
	DefaultTimeout     time.Duration
	MaximumTimeout     time.Duration
	MaximumHostCalls   int
	MaximumInputBytes  int
	MaximumOutputBytes int
	// OnInit installs trusted application bindings in every fresh VM. It runs
	// during validation and each invocation and must not retain the VM.
	OnInit func(*goja.Runtime) error
	// PocketBaseCompatibility configures the bounded host adapters used by the
	// PocketBase-compatible globals. Effects still require the matching
	// manifest capability (http, os, or filesystem).
	PocketBaseCompatibility PocketBaseCompatibilityOptions
}

type PocketBaseCompatibilityOptions struct {
	AuthMailCollections []uuid.UUID
	FilesystemRoot      string
	HTTPAllowedOrigins  []string
	Commands            map[string]string
	Environment         map[string]string
	Arguments           []string
	// ProcessExit is an operator-provided process lifecycle request. Leaving it
	// nil makes $os.exit fail closed. Tests and supervisors can inject it.
	ProcessExit func(int) error
	// ProcessRestart delegates a restart request to the operator's managed host.
	// A nil callback makes $app.restart fail closed.
	ProcessRestart func(context.Context) error
	// ProcessAppControl queues lifecycle actions with the operator-managed host
	// and returns its durable receipt. JavaScript never tears down live database
	// and HTTP resources from inside an invocation.
	ProcessAppControl     func(context.Context, string) (string, error)
	DataDirectory         string
	EncryptionEnvironment string
	Development           bool
	AllowHookBypass       bool
}

type JavaScriptHandler struct {
	Module string
	Export string
}

type JavaScriptRoute struct {
	Method         string
	Pattern        string
	Access         string
	Handler        JavaScriptHandler
	Capabilities   []string
	StreamResponse bool
}

type JavaScriptHook struct {
	Event    string
	Priority int
	Before   *JavaScriptHandler
	After    *JavaScriptHandler
}

type JavaScriptJob struct {
	Name             string
	Schedule         string
	Timezone         string
	Handler          JavaScriptHandler
	Capabilities     []string
	ServiceSubjectID uuid.UUID
}

type JavaScriptMigration struct {
	ID                 string
	Handler            JavaScriptHandler
	Down               string
	IrreversibleReason string
}

type JavaScriptManifest struct {
	Digest     string
	Bootstrap  string
	Routes     []JavaScriptRoute
	Hooks      []JavaScriptHook
	Jobs       []JavaScriptJob
	Migrations []JavaScriptMigration
}

func (options JavaScriptOptions) normalized() (JavaScriptOptions, error) {
	if options.Directory == "" {
		return JavaScriptOptions{}, errors.New("JavaScript.Directory is required")
	}
	capabilities := make(map[string]bool, len(options.Capabilities))
	for name, enabled := range options.Capabilities {
		switch name {
		case "records", "mail", "files", "log", "template", "database", "objects", "http", "os", "filesystem", "app-control", "hooks-bypass":
		default:
			return JavaScriptOptions{}, fmt.Errorf("JavaScript.Capabilities: unknown capability %q", name)
		}
		capabilities[name] = enabled
	}
	options.Capabilities = capabilities
	if capabilities["hooks-bypass"] && !options.PocketBaseCompatibility.AllowHookBypass {
		return JavaScriptOptions{}, errors.New("JavaScript hooks-bypass capability requires PocketBaseCompatibility.AllowHookBypass")
	}
	if capabilities["hooks-bypass"] && !capabilities["app-control"] {
		return JavaScriptOptions{}, errors.New("JavaScript.Capabilities: hooks-bypass requires app-control")
	}
	compatibility, err := options.PocketBaseCompatibility.normalized()
	if err != nil {
		return JavaScriptOptions{}, fmt.Errorf("JavaScript.PocketBaseCompatibility: %w", err)
	}
	options.PocketBaseCompatibility = compatibility
	subjects := make(map[string]uuid.UUID, len(options.JobServiceSubjects))
	for name, id := range options.JobServiceSubjects {
		if name == "" || id == uuid.Nil {
			return JavaScriptOptions{}, errors.New("JavaScript.JobServiceSubjects requires job names and non-zero UUIDs")
		}
		subjects[name] = id
	}
	options.JobServiceSubjects = subjects
	limits, err := options.limits().NormalizedForPublic()
	if err != nil {
		return JavaScriptOptions{}, fmt.Errorf("JavaScript: %w", err)
	}
	options.Concurrency = limits.Concurrency
	options.Queue = limits.Queue
	options.AdmissionTimeout = limits.AdmissionTimeout
	options.DefaultTimeout = limits.DefaultTimeout
	options.MaximumTimeout = limits.MaximumTimeout
	options.MaximumHostCalls = limits.MaximumHostCalls
	options.MaximumInputBytes = limits.MaximumInputBytes
	options.MaximumOutputBytes = limits.MaximumOutputBytes
	return options, nil
}

func (options PocketBaseCompatibilityOptions) normalized() (PocketBaseCompatibilityOptions, error) {
	if options.DataDirectory != "" {
		root, err := filepath.Abs(options.DataDirectory)
		if err != nil {
			return options, err
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return options, errors.New("DataDirectory must be an existing directory")
		}
		options.DataDirectory = root
	}
	if value := options.EncryptionEnvironment; len(value) > 128 || strings.Trim(value, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_") != "" {
		return options, errors.New("EncryptionEnvironment must be an environment variable name")
	}
	if options.FilesystemRoot != "" {
		root, err := filepath.Abs(options.FilesystemRoot)
		if err != nil {
			return options, err
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return options, errors.New("FilesystemRoot must be an existing directory")
		}
		root, err = filepath.EvalSymlinks(root)
		if err != nil {
			return options, err
		}
		options.FilesystemRoot = root
	}
	options.HTTPAllowedOrigins = slices.Clone(options.HTTPAllowedOrigins)
	for i, origin := range options.HTTPAllowedOrigins {
		u, err := url.Parse(origin)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" {
			return options, fmt.Errorf("invalid HTTPAllowedOrigins entry %q", origin)
		}
		options.HTTPAllowedOrigins[i] = u.Scheme + "://" + u.Host
	}
	options.Commands = maps.Clone(options.Commands)
	options.Environment = maps.Clone(options.Environment)
	options.Arguments = slices.Clone(options.Arguments)
	options.AuthMailCollections = slices.Clone(options.AuthMailCollections)
	for _, id := range options.AuthMailCollections {
		if id == uuid.Nil {
			return options, errors.New("AuthMailCollections contains an empty collection id")
		}
	}
	for name, command := range options.Commands {
		if name == "" || !filepath.IsAbs(command) {
			return options, errors.New("Commands requires names mapped to absolute paths")
		}
	}
	return options, nil
}

func (options JavaScriptOptions) limits() jsvm.Limits {
	return jsvm.Limits{
		Concurrency: options.Concurrency, Queue: options.Queue, AdmissionTimeout: options.AdmissionTimeout,
		DefaultTimeout: options.DefaultTimeout, MaximumTimeout: options.MaximumTimeout,
		MaximumHostCalls: options.MaximumHostCalls, MaximumInputBytes: options.MaximumInputBytes,
		MaximumOutputBytes: options.MaximumOutputBytes,
	}
}

// ValidateJavaScript loads an immutable source snapshot, validates every
// handler export without database or transport effects, and returns its public
// registration manifest.
func ValidateJavaScript(ctx context.Context, options JavaScriptOptions) (manifest JavaScriptManifest, err error) {
	normalized, err := options.normalized()
	if err != nil {
		return JavaScriptManifest{}, err
	}
	bundle, err := jsvm.LoadBundle(normalized.Directory)
	if err != nil {
		return JavaScriptManifest{}, err
	}
	runtime, err := jsvm.NewRuntimeWithInitializer(bundle, normalized.limits(), normalized.OnInit)
	if err != nil {
		return JavaScriptManifest{}, err
	}
	defer func() {
		err = errors.Join(err, runtime.Close(ctx))
	}()
	if err := validateJavaScriptRegistration(bundle.Manifest(), normalized.Capabilities); err != nil {
		return JavaScriptManifest{}, err
	}
	result := publicJavaScriptManifest(bundle)
	for index := range result.Jobs {
		result.Jobs[index].ServiceSubjectID = normalized.JobServiceSubjects[result.Jobs[index].Name]
	}
	return result, nil
}

func validateJavaScriptRegistration(manifest jsvm.Manifest, enabled map[string]bool) error {
	routes := make([]CustomRoute, 0, len(manifest.Routes))
	for _, route := range manifest.Routes {
		access := CustomRouteAccess(route.Access)
		routes = append(routes, CustomRoute{Method: route.Method, Pattern: route.Pattern, Access: access,
			Handler: http.NotFoundHandler(), MaximumRequestBytes: route.MaximumRequestBytes,
			MaximumResponseBytes: route.MaximumResponseBytes, StreamResponse: route.StreamResponse})
		if err := validateRequestedCapabilities(route.Capabilities, enabled, "route "+route.Pattern); err != nil {
			return err
		}
	}
	if err := api.ValidateCustomRoutes(routes); err != nil {
		return err
	}
	jobs := make([]ScheduledJob, 0, len(manifest.Jobs))
	for _, job := range manifest.Jobs {
		if err := validateRequestedCapabilities(job.Capabilities, enabled, "job "+job.Name); err != nil {
			return err
		}
		jobs = append(jobs, ScheduledJob{Name: job.Name, Schedule: job.Schedule, Timezone: job.Timezone, Handler: func(context.Context, JobRun) error { return nil }})
	}
	if err := validateScheduledJobs(jobs); err != nil {
		return err
	}
	seenHooks := make(map[string]bool)
	for _, hook := range manifest.Hooks {
		if !knownJavaScriptHook(hook.Event) {
			return fmt.Errorf("JavaScript hook event %q is not supported", hook.Event)
		}
		if err := validateRequestedCapabilities(hook.Capabilities, enabled, "hook "+hook.Event); err != nil {
			return err
		}
		key := fmt.Sprintf("%s:%d", hook.Event, hook.Priority)
		if seenHooks[key] {
			return fmt.Errorf("duplicate JavaScript hook event and priority %q", key)
		}
		seenHooks[key] = true
	}
	return nil
}

func validateRequestedCapabilities(requested []string, enabled map[string]bool, owner string) error {
	seen := make(map[string]bool, len(requested))
	for _, capability := range requested {
		if seen[capability] {
			return fmt.Errorf("JavaScript %s repeats capability %q", owner, capability)
		}
		seen[capability] = true
		if !enabled[capability] {
			return fmt.Errorf("JavaScript %s requests disabled capability %q", owner, capability)
		}
	}
	if seen["hooks-bypass"] && !seen["app-control"] {
		return fmt.Errorf("JavaScript %s requests hooks-bypass without app-control", owner)
	}
	return nil
}

func knownJavaScriptHook(kind string) bool {
	if kind == HookSettingsList || kind == HookSettingsUpdate {
		return true
	}
	if strings.HasPrefix(kind, "model.") || strings.HasPrefix(kind, "record.model.") || strings.HasPrefix(kind, "collection.model.") {
		parts := strings.Split(kind, ".")
		phase := strings.Join(parts[1:], ".")
		if parts[0] == "record" || parts[0] == "collection" {
			phase = strings.Join(parts[2:], ".")
		}
		return slices.Contains([]string{"validate", "create", "create.execute", "create.success", "create.error", "update", "update.execute", "update.success", "update.error", "delete", "delete.execute", "delete.success", "delete.error"}, phase)
	}
	return slices.Contains([]string{
		HookRequest, HookRecordBefore, HookRecordAfter, HookMailSend, HookMailOTP, HookMailVerification,
		HookMailPasswordReset, HookMailEmailChange, HookMailSMTPTest, HookFileUpload, HookFileDelete,
		HookFileToken, HookFileDownload, HookFileTransform, HookRecordListRequest, HookRecordViewRequest,
		HookRecordCreateRequest, HookRecordUpdateRequest, HookRecordDeleteRequest, HookRecordBatchRequest,
		HookSchemaImportPreview, HookSchemaImportApply, HookCollectionCreate, HookCollectionUpdate,
		HookCollectionDelete, HookAppBoot, HookAppServe, HookAppTerminate, HookAuthPassword, HookAuthRefresh,
		HookAuthOAuthBegin, HookAuthOAuthComplete, HookAuthOTPRequest, HookAuthOTPComplete,
		HookAuthVerificationRequest, HookAuthVerificationConfirm, HookAuthPasswordResetRequest,
		HookAuthPasswordResetConfirm, HookAuthEmailChangeRequest, HookAuthEmailChangeConfirm,
		HookAuthImpersonate, HookRealtimeConnect, HookRealtimeSubscribe, HookRealtimeMessage,
		HookStorageSettingsList, HookStorageSettingsUpdate, HookStorageSettingsTest,
		HookBackupCreate, HookBackupRestore, HookBackupDelete,
		HookObjectWrite, HookObjectDelete,
		HookAuthAlert, HookSettingsReload, HookModelValidate, HookModelExecute, HookModelSuccess, HookModelError,
	}, kind)
}

func publicJavaScriptManifest(bundle *jsvm.Bundle) JavaScriptManifest {
	manifest := bundle.Manifest()
	result := JavaScriptManifest{Digest: bundle.Digest(), Bootstrap: manifest.Bootstrap}
	for _, route := range manifest.Routes {
		result.Routes = append(result.Routes, JavaScriptRoute{Method: route.Method, Pattern: route.Pattern, Access: route.Access,
			Handler: publicHandler(route.Handler), Capabilities: append([]string(nil), route.Capabilities...), StreamResponse: route.StreamResponse})
	}
	for _, hook := range manifest.Hooks {
		converted := JavaScriptHook{Event: hook.Event, Priority: hook.Priority}
		if hook.Before != nil {
			value := publicHandler(*hook.Before)
			converted.Before = &value
		}
		if hook.After != nil {
			value := publicHandler(*hook.After)
			converted.After = &value
		}
		result.Hooks = append(result.Hooks, converted)
	}
	for _, job := range manifest.Jobs {
		result.Jobs = append(result.Jobs, JavaScriptJob{Name: job.Name, Schedule: job.Schedule, Timezone: job.Timezone,
			Handler: publicHandler(job.Handler), Capabilities: append([]string(nil), job.Capabilities...)})
	}
	for _, migration := range manifest.Migrations {
		result.Migrations = append(result.Migrations, JavaScriptMigration{ID: migration.ID,
			Handler: JavaScriptHandler{Module: migration.Module, Export: migration.Up}, Down: migration.Down,
			IrreversibleReason: migration.IrreversibleReason})
	}
	return result
}

func publicHandler(ref jsvm.HandlerRef) JavaScriptHandler {
	return JavaScriptHandler{Module: ref.Module, Export: ref.Export}
}
