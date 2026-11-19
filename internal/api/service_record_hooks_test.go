package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/lifecycle"
	"github.com/yakisoba0728/PgBase/internal/runtimesettings"
	"github.com/yakisoba0728/PgBase/internal/serviceauth"
)

func TestServiceAndPublicRecordsHonorHooksAndBatchSettings(t *testing.T) {
	for _, prefix := range []string{"service", "public"} {
		t.Run(prefix, func(t *testing.T) {
			registry := &lifecycle.Registry{}
			hookCalls := 0
			_, err := registry.On("record.create", lifecycle.Hook{Before: func(context.Context, *lifecycle.Event) error {
				hookCalls++
				return errors.New("veto")
			}})
			if err != nil {
				t.Fatal(err)
			}
			service := &hookBatchService{}
			dependencies := Dependencies{Records: service, ServiceTokens: &struct{ ServiceTokens }{}, LifecycleHooks: registry}
			serve := func(dependencies Dependencies, pattern, body string) *httptest.ResponseRecorder {
				for _, route := range serviceRecordRoutes(dependencies) {
					if route.method != http.MethodPost || route.pattern != pattern {
						continue
					}
					request := httptest.NewRequest(http.MethodPost, "/records", strings.NewReader(body))
					request.Header.Set("Content-Type", "application/json")
					request.SetPathValue("collection", "posts")
					request = request.WithContext(context.WithValue(request.Context(), publicRecordPrincipalKey{}, identity.Subject{Kind: identity.Anonymous}))
					response := httptest.NewRecorder()
					route.handler.ServeHTTP(response, request)
					return response
				}
				t.Fatal("route missing")
				return nil
			}
			response := serve(dependencies, "/api/v1/"+prefix+"/collections/{collection}/records", `{"title":"blocked"}`)
			if hookCalls != 1 || service.createCalls != 0 || response.Code < 400 {
				t.Fatalf("hook=%d create=%d status=%d", hookCalls, service.createCalls, response.Code)
			}
			dependencies.RuntimeSettings = &runtimesettings.Controller{}
			response = serve(dependencies, "/api/v1/"+prefix+"/collections/batch", `{"operations":[{"operation":"delete","collection":"posts","id":"first"}]}`)
			if response.Code != http.StatusServiceUnavailable || service.calls != 0 {
				t.Fatalf("batch calls=%d status=%d body=%s", service.calls, response.Code, response.Body.String())
			}
		})
	}
}

type failingServiceAuthentication struct {
	ServiceTokens
	err error
}

func (f failingServiceAuthentication) Authenticate(context.Context, string) (serviceauth.Principal, error) {
	return serviceauth.Principal{}, f.err
}

func TestServiceTokenAdmissionDistinguishesUnavailableAuthentication(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		want int
	}{
		{"invalid", serviceauth.ErrInvalidToken, http.StatusUnauthorized},
		{"unavailable", errors.New("backend unavailable"), http.StatusServiceUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/records", nil)
			request.Header.Set("Authorization", "Bearer "+strings.Repeat("a", 32))
			response := httptest.NewRecorder()
			handler := serviceSessionMiddleware(failingServiceAuthentication{err: test.err}, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("authentication failure admitted the request")
			}))
			handler.ServeHTTP(response, request)
			if response.Code != test.want {
				t.Fatalf("status = %d, want %d", response.Code, test.want)
			}
		})
	}
}
