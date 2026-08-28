//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"testing"
	"time"
)

type apiClient struct {
	baseURL string
	http    *http.Client
	csrf    string
}

func TestBootstrapCRUDAndRestart(t *testing.T) {
	bootstrapCRUDAndRestart(t)
}

func newAPIClient(baseURL string) *apiClient {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err)
	}
	return &apiClient{
		baseURL: baseURL,
		http: &http.Client{
			Jar:     jar,
			Timeout: 10 * time.Second,
		},
	}
}

func (c *apiClient) request(ctx context.Context, method, path string, body any, wantStatus int, destination any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost || method == http.MethodPut || method == http.MethodPatch || method == http.MethodDelete {
		request.Header.Set("Origin", c.baseURL)
		if c.csrf != "" {
			request.Header.Set("X-PgBase-CSRF", c.csrf)
		}
	}
	response, err := c.http.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		return err
	}
	if response.StatusCode != wantStatus {
		return fmt.Errorf("%s %s status = %d, want %d, body = %s", method, path, response.StatusCode, wantStatus, data)
	}
	if destination != nil && len(data) != 0 {
		if err := json.Unmarshal(data, destination); err != nil {
			return fmt.Errorf("decode %s %s response: %w (body = %s)", method, path, err, data)
		}
	}
	return nil
}

func (c *apiClient) ready(ctx context.Context) error {
	return c.request(ctx, http.MethodGet, "/health/ready", nil, http.StatusOK, nil)
}

func (c *apiClient) setup(ctx context.Context, setupToken, email, password string) error {
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/admin/setup", map[string]any{
		"setupToken": setupToken,
		"email":      email,
		"password":   password,
	}, http.StatusCreated, &session); err != nil {
		return err
	}
	c.csrf = session.CSRFToken
	return nil
}

func (c *apiClient) login(ctx context.Context, email, password string) error {
	var session struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := c.request(ctx, http.MethodPost, "/api/admin/auth/login", map[string]any{
		"email": email, "password": password,
	}, http.StatusOK, &session); err != nil {
		return err
	}
	c.csrf = session.CSRFToken
	return nil
}

func queryPath(path string, values url.Values) string {
	if len(values) == 0 {
		return path
	}
	return path + "?" + values.Encode()
}
