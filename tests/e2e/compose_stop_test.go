//go:build e2e

package e2e

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/webisnoteasy/PgBase/tests/testsupport"
)

const (
	composeShutdownTimeout = 8 * time.Second
	composeStopGracePeriod = 13 * time.Second
	// The in-flight request is released with enough of the application timeout
	// left to prove the drain finished on its own, and far enough inside the
	// Compose grace that a SIGKILL would be unambiguous.
	composeInflightHold = 5 * time.Second
)

// A direct SIGTERM proves the application drains. Only `docker compose stop`
// proves the shipped profile gives that drain enough grace before SIGKILL.
func TestComposeStopDrainsAnInflightRequestWithoutSIGKILL(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	waitReady(t, ctx, suite.baseURL)
	client := authenticatedClient(t, ctx, suite.baseURL)
	collectionName := uniqueCollection("compose_stop_items")
	applyCollection(t, ctx, client, map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "value", "type": "text", "required": true, "unique": false, "indexed": false},
		},
	})
	createRecord(t, ctx, client, collectionName, map[string]any{"value": "drain me"})

	containerID := suite.composeServiceContainer(t, ctx, "pgbase")
	restarted := false
	t.Cleanup(func() {
		if !restarted {
			suite.restartComposeService(t, "pgbase")
		}
	})

	lockerName, lockerPID := suite.holdShutdownTableLock(t, ctx, collectionName)
	lockHeld := true
	t.Cleanup(func() {
		if lockHeld {
			suite.releaseShutdownTableLock(t, context.Background(), lockerName, lockerPID)
		}
	})

	inflightDone := make(chan error, 1)
	go func() {
		values := url.Values{"limit": []string{"1"}}
		inflightDone <- client.request(ctx, http.MethodGet, queryPath("/api/v1/collections/"+collectionName+"/records", values), nil, http.StatusOK, nil)
	}()
	suite.waitForShutdownQueryLock(t, ctx, collectionName)

	// `docker compose stop` blocks until the container exits or the grace
	// period expires, so the lock release has to happen while it is running.
	stopped := make(chan composeStopResult, 1)
	signalTime := time.Now()
	go func() {
		output, err := suite.compose(ctx, "stop", "pgbase")
		stopped <- composeStopResult{output: output, err: err}
	}()

	if err := testsupport.Poll(ctx, composeShutdownTimeout, 25*time.Millisecond, 300*time.Millisecond, func(attemptCtx context.Context) (bool, error) {
		return newAPIClient(suite.baseURL).ready(attemptCtx) != nil, nil
	}); err != nil {
		t.Fatalf("readiness remained successful after compose stop: %v", err)
	}
	select {
	case err := <-inflightDone:
		t.Fatalf("in-flight request stopped blocking before the drain: %v", err)
	case result := <-stopped:
		t.Fatalf("compose stop returned before the in-flight request drained: %v\n%s", result.err, result.output)
	case <-time.After(composeInflightHold - time.Since(signalTime)):
	}

	suite.releaseShutdownTableLock(t, ctx, lockerName, lockerPID)
	lockHeld = false
	select {
	case err := <-inflightDone:
		if err != nil {
			t.Fatalf("in-flight request did not finish successfully: %v", err)
		}
	case <-time.After(composeShutdownTimeout):
		t.Fatal("in-flight request did not finish within the application shutdown timeout")
	}

	select {
	case result := <-stopped:
		if result.err != nil {
			t.Fatalf("compose stop: %v\n%s", result.err, result.output)
		}
	case <-time.After(composeStopGracePeriod):
		t.Fatal("compose stop did not return within the container stop grace period")
	}
	if elapsed := time.Since(signalTime); elapsed >= composeStopGracePeriod {
		t.Fatalf("compose stop took %s, which reaches the %s SIGKILL boundary", elapsed, composeStopGracePeriod)
	}

	exitCode := suite.containerExitCode(t, ctx, containerID)
	if exitCode == 137 {
		t.Fatal("compose stop killed pgBase with SIGKILL instead of draining it")
	}
	if exitCode != 0 {
		t.Fatalf("pgBase exit code after compose stop = %d, want 0", exitCode)
	}

	suite.restartComposeService(t, "pgbase")
	restarted = true
}

type composeStopResult struct {
	output string
	err    error
}

func (s e2eSuite) composeServiceContainer(t *testing.T, ctx context.Context, service string) string {
	t.Helper()
	output, err := s.compose(ctx, "ps", "--quiet", service)
	if err != nil {
		t.Fatalf("read %s container id: %v\n%s", service, err, output)
	}
	containerID := strings.TrimSpace(output)
	if containerID == "" {
		t.Fatalf("compose service %s has no container", service)
	}
	return containerID
}

func (s e2eSuite) containerExitCode(t *testing.T, ctx context.Context, containerID string) int {
	t.Helper()
	output, err := s.docker(ctx, "inspect", "--format", "{{.State.ExitCode}}", containerID)
	if err != nil {
		t.Fatalf("inspect exit code: %v\n%s", err, output)
	}
	exitCode, err := strconv.Atoi(strings.TrimSpace(output))
	if err != nil {
		t.Fatalf("parse exit code %q: %v", strings.TrimSpace(output), err)
	}
	return exitCode
}

func (s e2eSuite) restartComposeService(t *testing.T, service string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if output, err := s.compose(ctx, "start", service); err != nil {
		t.Fatalf("restart %s after the Compose stop drill: %v\n%s", service, err, output)
	}
	waitReady(t, ctx, s.baseURL)
}
