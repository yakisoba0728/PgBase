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

	"github.com/yakisoba0728/PgBase/tests/testsupport"
)

func TestGracefulShutdownRejectsNewRequestsAndDrainsInflight(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	baseURL, containerName := suite.startInstance(t, ctx, "shutdown", 8*time.Second)
	t.Cleanup(func() { suite.removeContainer(t, containerName) })
	waitReady(t, ctx, baseURL)
	client := authenticatedClient(t, ctx, baseURL)
	collectionName := uniqueCollection("shutdown_items")
	definition := map[string]any{
		"name": collectionName,
		"fields": []map[string]any{
			{"name": "value", "type": "text", "required": true, "unique": false, "indexed": false},
		},
	}
	applyCollection(t, ctx, client, definition)
	createRecord(t, ctx, client, collectionName, map[string]any{"value": "drain me"})

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
	delay := time.NewTimer(300 * time.Millisecond)
	select {
	case err := <-inflightDone:
		if !delay.Stop() {
			<-delay.C
		}
		t.Fatalf("deliberately delayed request completed before SIGTERM: %v", err)
	case <-delay.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	suite.waitForShutdownQueryLock(t, ctx, collectionName)
	readyCtx, readyCancel := context.WithTimeout(ctx, 2*time.Second)
	readyErr := newAPIClient(baseURL).ready(readyCtx)
	readyCancel()
	if readyErr != nil {
		t.Fatalf("readiness before SIGTERM with table lock held: %v", readyErr)
	}
	select {
	case err := <-inflightDone:
		t.Fatalf("in-flight request stopped blocking before SIGTERM: %v", err)
	default:
	}

	signalTime := time.Now()
	if output, err := suite.docker(ctx, "kill", "--signal", "SIGTERM", containerName); err != nil {
		t.Fatalf("send SIGTERM: %v\n%s", err, output)
	}

	if err := testsupport.Poll(ctx, 3*time.Second, 25*time.Millisecond, 300*time.Millisecond, func(attemptCtx context.Context) (bool, error) {
		return newAPIClient(baseURL).ready(attemptCtx) != nil, nil
	}); err != nil {
		t.Fatalf("readiness remained successful after SIGTERM: %v", err)
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

	suite.releaseShutdownTableLock(t, ctx, lockerName, lockerPID)
	lockHeld = false
	drainCtx, drainCancel := context.WithTimeout(ctx, 5*time.Second)
	defer drainCancel()
	select {
	case err := <-inflightDone:
		if err != nil {
			t.Fatalf("in-flight request did not finish successfully: %v", err)
		}
	case <-drainCtx.Done():
		t.Fatalf("in-flight request did not finish within shutdown grace period: %v", drainCtx.Err())
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

func (s e2eSuite) holdShutdownTableLock(t *testing.T, ctx context.Context, collectionName string) (string, int) {
	t.Helper()
	name := s.project + "-shutdown-lock"
	command := "BEGIN; LOCK TABLE app." + collectionName + " IN ACCESS EXCLUSIVE MODE; SELECT pg_sleep(300);"
	arguments := []string{
		"run", "--detach", "--name", name,
		"--network", s.project + "_default",
		"--env", "PGPASSWORD=" + s.databasePass,
		"--entrypoint", "psql",
		"postgres:16.13-bookworm@sha256:472efd9a66f2b2f1a5aeb18b28de74332e6ef88c2b93a1a5d812fb6db67a5f60",
		"--host", "postgres", "--username", "pgbase", "--dbname", "pgbase",
		"--no-psqlrc", "--set", "ON_ERROR_STOP=1", "--command", command,
	}
	if output, err := s.docker(ctx, arguments...); err != nil {
		t.Fatalf("start shutdown table lock: %v\n%s", err, output)
	}
	query := `select l.pid from pg_locks l
		join pg_class c on c.oid = l.relation
		join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = 'app' and c.relname = '` + collectionName + `'
		and l.mode = 'AccessExclusiveLock' and l.granted`
	var backendPID int
	pollErr := testsupport.Poll(ctx, 10*time.Second, 100*time.Millisecond, 3*time.Second, func(attemptCtx context.Context) (bool, error) {
		output, err := s.compose(attemptCtx, "exec", "-T", "postgres", "psql", "--username", "pgbase", "--dbname", "pgbase", "--no-psqlrc", "--tuples-only", "--no-align", "--command", query)
		if err != nil {
			return false, err
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(output))
		if parseErr != nil {
			return false, parseErr
		}
		backendPID = pid
		return pid > 0, nil
	})
	if pollErr == nil {
		return name, backendPID
	}
	logs, _ := s.docker(ctx, "logs", name)
	s.removeContainer(t, name)
	t.Fatalf("shutdown table lock was not acquired: %v; locker logs:\n%s", pollErr, logs)
	return "", 0
}

func (s e2eSuite) releaseShutdownTableLock(t *testing.T, ctx context.Context, container string, backendPID int) {
	t.Helper()
	releaseCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	query := "select pg_terminate_backend(" + strconv.Itoa(backendPID) + ")"
	output, err := s.compose(releaseCtx, "exec", "-T", "postgres", "psql", "--username", "pgbase", "--dbname", "pgbase", "--no-psqlrc", "--tuples-only", "--no-align", "--command", query)
	if err != nil || strings.TrimSpace(output) != "t" {
		t.Fatalf("terminate shutdown lock backend %d: error=%v output=%q", backendPID, err, output)
	}
	s.removeContainer(t, container)
}

func (s e2eSuite) waitForShutdownQueryLock(t *testing.T, ctx context.Context, collectionName string) {
	t.Helper()
	query := `select count(*) from pg_locks l
		join pg_class c on c.oid = l.relation
		join pg_namespace n on n.oid = c.relnamespace
		where n.nspname = 'app' and c.relname = '` + collectionName + `'
		and l.mode = 'AccessShareLock' and not l.granted`
	if err := testsupport.Poll(ctx, 4*time.Second, 50*time.Millisecond, 2*time.Second, func(attemptCtx context.Context) (bool, error) {
		output, err := s.compose(attemptCtx, "exec", "-T", "postgres", "psql", "--username", "pgbase", "--dbname", "pgbase", "--no-psqlrc", "--tuples-only", "--no-align", "--command", query)
		if err != nil {
			return false, err
		}
		return strings.TrimSpace(output) == "1", nil
	}); err != nil {
		t.Fatalf("record request did not block on app.%s: %v", collectionName, err)
	}
}
