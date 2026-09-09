package api

import (
	"fmt"
	"net/http"
	"testing"
)

func TestAllOptionalRoutesFitTheDeclaredBudget(t *testing.T) {
	dependencies := allOptionalRouteDependencies()
	routes := declareRoutes(dependencies, true)
	if err := validateRoutes(routes); err != nil {
		t.Fatal(err)
	}
	t.Logf("all optional modules: %d routes, budget %d", len(routes), maximumRegisteredRoutes)
	patterns := map[string]bool{}
	for _, route := range routes {
		patterns[route.muxPattern()] = true
	}
	for _, pattern := range []string{"GET /api/admin/schema/export", "POST /api/admin/schema/import/preview", "POST /api/admin/schema/import/apply", "POST /api/v1/auth/otp/request", "POST /api/v1/auth/otp/complete", "POST /api/v1/auth/{authCollection}/otp/request", "POST /api/v1/auth/{authCollection}/otp/complete", "GET /api/admin/backups"} {
		if !patterns[pattern] {
			t.Errorf("optional route omitted: %s", pattern)
		}
	}
	for i := 0; i < MaximumCustomRoutes; i++ {
		dependencies.CustomRoutes = append(dependencies.CustomRoutes, CustomRoute{Method: "GET", Pattern: fmt.Sprintf("/application%d/{id}", i), Access: CustomRoutePublic, Handler: http.NotFoundHandler()})
	}
	if _, err := NewRouter(completeDependencies(dependencies)); err != nil {
		t.Fatalf("built-ins and bounded custom routes cannot coexist: %v", err)
	}
}

func TestSchemaDocumentRequestLimitIsAnExplicitException(t *testing.T) {
	routes := schemaDocumentRoutes(allOptionalRouteDependencies())
	for _, declared := range routes {
		if declared.method == "GET" {
			continue
		}
		if !declared.schemaDocument || declared.maximumRequestBytes != maximumCatalogResponseBytes {
			t.Fatal("schema document cap not explicit")
		}
		if err := validateRoutes([]route{declared}); err != nil {
			t.Fatal(err)
		}
		for _, mutate := range []func(*route){
			func(r *route) { r.pattern = "/api/other" },
			func(r *route) { r.access = accessPublic },
			func(r *route) { r.streamedUpload = true },
			func(r *route) { r.maximumRequestBytes++ },
		} {
			invalid := declared
			mutate(&invalid)
			if err := validateRoutes([]route{invalid}); err == nil {
				t.Fatal("schema document exception widened silently")
			}
		}
	}
}
