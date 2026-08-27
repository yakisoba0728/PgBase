//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestGracefulShutdownRejectsNewRequestsAndDrainsInflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	baseURL, containerName := suite.startInstance(t, ctx, "shutdown", 8*time.Second)
	t.Cleanup(func() { suite.removeContainer(containerName) })
	waitReady(t, ctx, baseURL)
	client := authenticatedClient(t, ctx, baseURL)
	definition := map[string]any{
		"name": "shutdown_items",
		"fields": []map[string]any{
			{"name": "value", "type": "text", "required": true, "unique": false, "indexed": false},
		},
	}
	applyCollection(t, ctx, client, definition)
	createRecord(t, ctx, client, "shutdown_items", map[string]any{"value": "drain me"})

	if output, err := suite.compose(ctx, "pause", "postgres"); err != nil {
		t.Fatalf("pause PostgreSQL: %v\n%s", err, output)
	}
	postgresPaused := true
	t.Cleanup(func() {
		if postgresPaused {
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cleanupCancel()
			_, _ = suite.compose(cleanupCtx, "unpause", "postgres")
		}
	})

	inflightDone := make(chan error, 1)
	go func() {
		values := url.Values{"limit": []string{"1"}}
		inflightDone <- client.request(ctx, http.MethodGet, queryPath("/api/v1/collections/shutdown_items/records", values), nil, http.StatusOK, nil)
	}()
	select {
	case err := <-inflightDone:
		t.Fatalf("deliberately delayed request completed before SIGTERM: %v", err)
	case <-time.After(300 * time.Millisecond):
	}

	signalTime := time.Now()
	if output, err := suite.docker(ctx, "kill", "--signal", "SIGTERM", containerName); err != nil {
		t.Fatalf("send SIGTERM: %v\n%s", err, output)
	}

	readinessDeadline := time.Now().Add(3 * time.Second)
	readinessFailed := false
	for time.Now().Before(readinessDeadline) {
		probeCtx, probeCancel := context.WithTimeout(ctx, 300*time.Millisecond)
		err := newAPIClient(baseURL).ready(probeCtx)
		probeCancel()
		if err != nil {
			readinessFailed = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !readinessFailed {
		t.Fatal("readiness remained successful after SIGTERM")
	}
	if output, err := suite.docker(ctx, "inspect", "--format", "{{.State.Running}}", containerName); err != nil || strings.TrimSpace(output) != "true" {
		t.Fatalf("container exited before delayed in-flight request drained: err=%v state=%q", err, output)
	}
	newRequestCtx, newRequestCancel := context.WithTimeout(ctx, 500*time.Millisecond)
	newRequestErr := newAPIClient(baseURL).request(newRequestCtx, http.MethodGet, "/health/live", nil, http.StatusOK, nil)
	newRequestCancel()
	if newRequestErr == nil {
		t.Fatal("new request was accepted after SIGTERM")
	}

	if output, err := suite.compose(ctx, "unpause", "postgres"); err != nil {
		t.Fatalf("unpause PostgreSQL: %v\n%s", err, output)
	}
	postgresPaused = false
	select {
	case err := <-inflightDone:
		if err != nil {
			t.Fatalf("in-flight request did not finish successfully: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("in-flight request did not finish within shutdown grace period")
	}

	waitCtx, waitCancel := context.WithTimeout(ctx, 8*time.Second)
	defer waitCancel()
	output, err := suite.docker(waitCtx, "wait", containerName)
	if err != nil {
		t.Fatalf("wait for graceful container exit: %v\n%s", err, output)
	}
	if strings.TrimSpace(output) != "0" {
		t.Fatalf("container exit code = %q", strings.TrimSpace(output))
	}
	if elapsed := time.Since(signalTime); elapsed > 8*time.Second {
		t.Fatalf("graceful shutdown elapsed = %s", elapsed)
	}
}
