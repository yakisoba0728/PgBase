package pgbase

import (
	"net/http"
	"testing"
)

func TestCustomRoutesOptionsCopyAndValidation(t *testing.T) {
	routes := []CustomRoute{{Method: "GET", Pattern: "/reports/{id}", Access: RouteAdmin, Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})}}
	options := Options{DatabaseURL: "postgres://postgres:postgres@localhost/pgbase", TokenSecret: make([]byte, 32), CustomRoutes: routes}
	normalized, err := options.Normalized()
	if err != nil {
		t.Fatal(err)
	}
	routes[0].Access = RoutePublic
	if normalized.CustomRoutes[0].Access != RouteAdmin {
		t.Fatal("normalized routes retained caller-owned configuration")
	}
	routes[0].Access = ""
	if _, err := options.Normalized(); err == nil {
		t.Fatal("implicit access passed options validation")
	}
}
