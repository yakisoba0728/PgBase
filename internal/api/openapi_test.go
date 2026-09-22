package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/jobs"
	"github.com/yakisoba0728/PgBase/internal/observability"
	"github.com/yakisoba0728/PgBase/internal/realtime"
	"github.com/yakisoba0728/PgBase/internal/recordaccess"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"github.com/yakisoba0728/PgBase/internal/serviceauth"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

// TestOpenAPIRouteContract derives path/admission metadata from the same route
// registry used by the server. Bodies remain extensible; this is a route
// contract, not a claim to describe every dynamically configured record field.
func allOptionalRouteDependencies() Dependencies {
	sqlConsole, err := NewSQLConsoleHandlers(&fakeSQLConsoleService{}, testTokenSecret, SQLConsoleHandlerOptions{})
	if err != nil {
		panic(err)
	}
	return completeDependencies(Dependencies{
		AdminAuth:                   (*adminauth.Service)(nil),
		BackupStorageAdministration: &struct{ BackupStorageAdministration }{}, StorageAdministration: &struct{ StorageAdministration }{}, StorageMigrations: &struct{ StorageMigrationAdministration }{}, Schema: (*schema.Service)(nil), Backups: &struct{ ManagedBackups }{}, AdminUI: http.NotFoundHandler(),
		AuthAdministration: (*userauth.Service)(nil), Jobs: (*jobs.Service)(nil), RequestLog: &observability.RequestLog{},
		RuntimeSettings: &runtimesettings.Controller{},
		UserAuth:        (*userauth.Service)(nil), EmailActions: (*userauth.Service)(nil), AuthFactors: (*userauth.Service)(nil), AuthCollections: (*userauth.Service)(nil),
		RecordAccess: &struct{ RecordAccess }{}, RecordRules: (*recordaccess.RuleService)(nil), ServiceTokens: (*serviceauth.Service)(nil), Files: (*filestore.Service)(nil), Realtime: (*realtime.Service)(nil),
		MaximumFileUploadBytes: 100 << 20, MetricsExposition: http.NotFoundHandler(),
		SQLConsole: sqlConsole,
	})
}

func TestOpenAPIRouteContract(t *testing.T) {
	dependencies := allOptionalRouteDependencies()
	paths := make(map[string]any)
	parametersPattern := regexp.MustCompile(`\{([a-zA-Z][a-zA-Z0-9]*)\}`)
	for _, route := range declareRoutes(dependencies, true) {
		if route.method == "" {
			continue
		}
		path, ok := paths[route.pattern].(map[string]any)
		if !ok {
			path = make(map[string]any)
			paths[route.pattern] = path
		}
		security := []map[string][]string{}
		switch route.access {
		case accessAdminSession, accessAdminSameOriginRead:
			security = append(security, map[string][]string{"adminCookie": {}})
		case accessUserSession:
			security = append(security, map[string][]string{"userBearer": {}})
		case accessServiceSession:
			security = append(security, map[string][]string{"serviceBearer": {}})
		}
		operation := map[string]any{"responses": map[string]any{"default": map[string]any{"description": "Operation response; JSON errors contain an error code. See feature documentation and typed SDK for payload contracts."}}, "security": security}
		if route.maximumRequestBytes > 0 {
			operation["x-pgbase-maximum-request-bytes"] = route.maximumRequestBytes
		}
		if route.access == accessAdminSession {
			operation["x-pgbase-csrf-required"] = true
		}
		var parameters []any
		for _, match := range parametersPattern.FindAllStringSubmatch(route.pattern, -1) {
			parameters = append(parameters, map[string]any{"name": match[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
		}
		if len(parameters) > 0 {
			operation["parameters"] = parameters
		}
		path[strings.ToLower(route.method)] = operation
	}
	document := map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "PgBase route contract", "version": "0.2.0-dev", "description": "All optional modules enabled. Routes are only mounted when their module is configured. Dynamic record schemas and full request/response DTOs are documented separately."}, "paths": paths, "components": map[string]any{"securitySchemes": map[string]any{"adminCookie": map[string]any{"type": "apiKey", "in": "cookie", "name": adminCookieName}, "userBearer": map[string]any{"type": "http", "scheme": "bearer"}, "serviceBearer": map[string]any{"type": "http", "scheme": "bearer"}}}}
	encoded, err := json.MarshalIndent(document, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join("..", "..", "docs", "openapi.routes.json")
	if os.Getenv("PGBASE_UPDATE_OPENAPI") == "1" {
		if err := os.WriteFile(path, encoded, 0644); err != nil {
			t.Fatal(err)
		}
	}
	actual, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, encoded) {
		t.Fatal("OpenAPI route contract stale: PGBASE_UPDATE_OPENAPI=1 go test ./internal/api -run TestOpenAPIRouteContract")
	}
}
