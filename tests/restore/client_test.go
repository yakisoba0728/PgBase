//go:build restore

package restore

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"testing"
	"time"
)

// adminClient exercises the same administrator-authenticated wire contract an
// operator uses to validate a restore. Direct SQL is used only for assertions
// the API cannot express, never as the mutation path under test.
type adminClient struct {
	baseURL string
	http    *http.Client
	csrf    string
}

type response struct {
	status int
	header http.Header
	body   []byte
}

func newAdminClient(baseURL string) *adminClient {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}
	return &adminClient{baseURL: baseURL, http: &http.Client{Jar: jar, Timeout: 30 * time.Second}}
}

func (c *adminClient) exchange(ctx context.Context, method, path string, body any, headers http.Header) (response, error) {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return response{}, err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return response{}, err
	}
	if headers != nil {
		request.Header = headers.Clone()
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		request.Header.Set("Origin", c.baseURL)
		if c.csrf != "" {
			request.Header.Set("X-PgBase-CSRF", c.csrf)
		}
	}
	raw, err := c.http.Do(request)
	if err != nil {
		return response{}, err
	}
	defer raw.Body.Close()
	data, err := io.ReadAll(raw.Body)
	if err != nil {
		return response{}, err
	}
	return response{status: raw.StatusCode, header: raw.Header.Clone(), body: data}, nil
}

func (c *adminClient) call(t *testing.T, ctx context.Context, method, path string, body any, headers http.Header, wantStatus int, destination any) http.Header {
	t.Helper()
	result, err := c.exchange(ctx, method, path, body, headers)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	if result.status != wantStatus {
		t.Fatalf("%s %s status = %d, want %d, body = %s", method, path, result.status, wantStatus, result.body)
	}
	if destination != nil && len(result.body) > 0 {
		if err := json.Unmarshal(result.body, destination); err != nil {
			t.Fatalf("decode %s %s: %v (body = %s)", method, path, err, result.body)
		}
	}
	return result.header
}

func (c *adminClient) ready(ctx context.Context) error {
	result, err := c.exchange(ctx, http.MethodGet, "/health/ready", nil, nil)
	if err != nil {
		return err
	}
	if result.status != http.StatusOK {
		return fmt.Errorf("readiness status = %d, body = %s", result.status, result.body)
	}
	return nil
}

func (c *adminClient) login(t *testing.T, ctx context.Context, email, password string) {
	t.Helper()
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	c.call(t, ctx, http.MethodPost, "/api/admin/auth/login",
		map[string]any{"email": email, "password": password}, nil, http.StatusOK, &session)
	if session.CSRFToken == "" {
		t.Fatal("login returned no CSRF token")
	}
	c.csrf = session.CSRFToken
}

func (c *adminClient) reportedVersion(t *testing.T, ctx context.Context) string {
	t.Helper()
	var status struct {
		Version string `json:"version"`
	}
	c.call(t, ctx, http.MethodGet, "/api/admin/system", nil, nil, http.StatusOK, &status)
	return status.Version
}

// applyCollection performs the real plan/apply pair so the drill produces a
// durable schema operation ledger entry and its migration receipt.
func (c *adminClient) applyCollection(t *testing.T, ctx context.Context, definition map[string]any) {
	t.Helper()
	planBody := cloneWith(definition, map[string]any{"operation": "create"})
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	c.call(t, ctx, http.MethodPost, "/api/admin/collections/plan", planBody, nil, http.StatusOK, &plan)
	if plan.Checksum == "" {
		t.Fatal("collection plan returned no checksum")
	}
	applyBody := cloneWith(definition, map[string]any{
		"operation":   "create",
		"baseVersion": plan.BaseVersion,
		"checksum":    plan.Checksum,
	})
	c.call(t, ctx, http.MethodPost, "/api/admin/collections/apply", applyBody, nil, http.StatusCreated, nil)
}

type record struct {
	id       string
	etag     string
	location string
}

func (c *adminClient) createRecord(t *testing.T, ctx context.Context, collection, idempotencyKey string, input map[string]any) record {
	t.Helper()
	headers := make(http.Header)
	headers.Set("Idempotency-Key", idempotencyKey)
	var created map[string]any
	responseHeaders := c.call(t, ctx, http.MethodPost, recordsPath(collection), input, headers, http.StatusCreated, &created)
	identifier, _ := created["id"].(string)
	if identifier == "" {
		t.Fatalf("created record has no id: %#v", created)
	}
	if replayed := responseHeaders.Get("Idempotency-Replayed"); replayed != "false" {
		t.Fatalf("first create Idempotency-Replayed = %q, want false", replayed)
	}
	return record{id: identifier, etag: responseHeaders.Get("ETag"), location: responseHeaders.Get("Location")}
}

func (c *adminClient) patchRecord(t *testing.T, ctx context.Context, collection, identifier, ifMatch string, input map[string]any) string {
	t.Helper()
	headers := make(http.Header)
	headers.Set("If-Match", ifMatch)
	responseHeaders := c.call(t, ctx, http.MethodPatch, recordsPath(collection)+"/"+identifier, input, headers, http.StatusOK, nil)
	return responseHeaders.Get("ETag")
}

func recordsPath(collection string) string {
	return "/api/v1/collections/" + collection + "/records"
}

func cloneWith(base map[string]any, additions map[string]any) map[string]any {
	merged := make(map[string]any, len(base)+len(additions))
	for key, value := range base {
		merged[key] = value
	}
	for key, value := range additions {
		merged[key] = value
	}
	return merged
}
