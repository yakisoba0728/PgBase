//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
)

func TestExtensionObjectsStreamListCopyAndRejectTraversal(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 32}
	})
	objects := fixture.app.extensions.objects
	ctx := context.Background()
	if _, err := objects.WriteStream(ctx, "module.js", "docs/a", bytes.NewBufferString("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := objects.Write(ctx, "module.js", "docs/b", []byte("bravo")); err != nil {
		t.Fatal(err)
	}
	page, err := objects.List(ctx, "module.js", "docs/", "", 1)
	if err != nil || len(page.Objects) != 1 || page.Objects[0].Key != "docs/a" || page.NextCursor != "docs/a" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	page, err = objects.List(ctx, "module.js", "docs/", page.NextCursor, 1)
	if err != nil || len(page.Objects) != 1 || page.Objects[0].Key != "docs/b" || page.NextCursor != "" {
		t.Fatalf("next page=%#v err=%v", page, err)
	}
	if size, err := objects.Copy(ctx, "module.js", "docs/a", "docs/c"); err != nil || size != 5 {
		t.Fatalf("copy size=%d err=%v", size, err)
	}
	stream, err := objects.Open(ctx, "module.js", "docs/c")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(stream)
	_ = stream.Close()
	if err != nil || string(data) != "alpha" {
		t.Fatalf("stream=%q err=%v", data, err)
	}
	if _, err := objects.WriteStream(ctx, "module.js", "../escape", bytes.NewBufferString("x")); err == nil {
		t.Fatal("traversal accepted")
	}
	if _, err := objects.WriteStream(ctx, "module.js", "too-large", bytes.NewReader(bytes.Repeat([]byte("x"), 33))); !errors.Is(err, filestore.ErrTooLarge) {
		t.Fatalf("large stream err=%v", err)
	}
	if _, err := objects.Read(ctx, "module.js", "too-large"); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("failed stream left object: %v", err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := objects.WriteStream(cancelled, "module.js", "cancelled", bytes.NewBufferString("x")); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled stream err=%v", err)
	}
	if _, err := objects.Read(ctx, "module.js", "cancelled"); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("cancelled stream left object: %v", err)
	}
}

func TestExtensionObjectReplacementPublicationFailurePreservesCommittedBytes(t *testing.T) {
	files := t.TempDir()
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: files, MaximumBytes: 1024}
	})
	objects, ctx := fixture.app.extensions.objects, t.Context()
	if err := objects.Write(ctx, "module.js", "state", []byte("committed")); err != nil {
		t.Fatal(err)
	}
	var committedKey string
	if err := fixture.app.pool.QueryRow(ctx, `select object_key from pgbase_meta.extension_objects where logical_namespace='module.js' and logical_key='state'`).Scan(&committedKey); err != nil {
		t.Fatal(err)
	}
	if err := objects.Write(ctx, "module.js", "state", []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	var replacementKey string
	if err := fixture.app.pool.QueryRow(ctx, `select object_key from pgbase_meta.extension_objects where logical_namespace='module.js' and logical_key='state'`).Scan(&replacementKey); err != nil || replacementKey == committedKey {
		t.Fatalf("replacement key=%q committed=%q err=%v", replacementKey, committedKey, err)
	}
	var queued bool
	if err := fixture.app.pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1)`, committedKey).Scan(&queued); err != nil || !queued {
		t.Fatalf("old physical key queued=%t err=%v", queued, err)
	}
	if _, err := fixture.app.pool.Exec(ctx, `create function pgbase_meta.reject_extension_object_publish() returns trigger language plpgsql as $$ begin raise exception 'injected metadata failure'; end $$; create trigger reject_extension_object_publish before update on pgbase_meta.extension_objects for each row execute function pgbase_meta.reject_extension_object_publish()`); err != nil {
		t.Fatal(err)
	}
	if err := objects.Write(ctx, "module.js", "state", []byte("rejected")); err == nil {
		t.Fatal("injected metadata failure did not reject replacement")
	}
	got, err := objects.Read(ctx, "module.js", "state")
	if err != nil || string(got) != "replacement" {
		t.Fatalf("committed object after failed replacement=%q err=%v", got, err)
	}
	var publishedKey string
	if err := fixture.app.pool.QueryRow(ctx, `select object_key from pgbase_meta.extension_objects where logical_namespace='module.js' and logical_key='state'`).Scan(&publishedKey); err != nil || publishedKey != replacementKey {
		t.Fatalf("published key=%q want=%q err=%v", publishedKey, replacementKey, err)
	}
	entries, err := filepath.Glob(filepath.Join(files, "extensions", "*", "*", "*"))
	if err != nil || len(entries) != 2 {
		t.Fatalf("physical versions=%v err=%v", entries, err)
	}
}

func TestExtensionObjectExternalTransactionCommitAndRollbackFinalizePhysicalVersions(t *testing.T) {
	files := t.TempDir()
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: files, MaximumBytes: 1024}
	})
	objects, ctx := fixture.app.extensions.objects, t.Context()
	if err := objects.Write(ctx, "module.js", "state", []byte("committed")); err != nil {
		t.Fatal(err)
	}
	var originalKey string
	if err := fixture.app.pool.QueryRow(ctx, `select object_key from pgbase_meta.extension_objects where logical_key='state'`).Scan(&originalKey); err != nil {
		t.Fatal(err)
	}

	tx, err := fixture.app.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	rollbackState := newExtensionObjectTransactionState(tx, objects)
	txCtx := context.WithValue(ctx, extensionObjectTransactionKey{}, rollbackState)
	if err := objects.Write(txCtx, "module.js", "state", []byte("rolled back")); err != nil {
		t.Fatal(err)
	}
	got, err := objects.Read(txCtx, "module.js", "state")
	if err != nil || string(got) != "rolled back" {
		t.Fatalf("transactional read=%q err=%v", got, err)
	}
	if info, err := objects.Attributes(txCtx, "module.js", "state"); err != nil || info.Size != int64(len("rolled back")) {
		t.Fatalf("transactional attributes=%#v err=%v", info, err)
	}
	if page, err := objects.List(txCtx, "module.js", "sta", "", 10); err != nil || len(page.Objects) != 1 || page.Objects[0].Key != "state" {
		t.Fatalf("transactional list=%#v err=%v", page, err)
	}
	if _, err := objects.Copy(txCtx, "module.js", "state", "copy"); err != nil {
		t.Fatalf("transactional copy: %v", err)
	}
	if copied, err := objects.Read(txCtx, "module.js", "copy"); err != nil || string(copied) != "rolled back" {
		t.Fatalf("transactional copied read=%q err=%v", copied, err)
	}
	if err := objects.Delete(txCtx, "module.js", "state"); err != nil {
		t.Fatalf("transactional delete: %v", err)
	}
	if _, err := objects.Attributes(txCtx, "module.js", "state"); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("transactional delete remained visible: %v", err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if err := rollbackState.finish(ctx, true); err != nil {
		t.Fatal(err)
	}
	got, err = objects.Read(ctx, "module.js", "state")
	if err != nil || string(got) != "committed" {
		t.Fatalf("after rollback=%q err=%v", got, err)
	}
	entries, err := filepath.Glob(filepath.Join(files, "extensions", "*", "*", "*"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("rollback physical versions=%v err=%v", entries, err)
	}

	tx, err = fixture.app.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	commitState := newExtensionObjectTransactionState(tx, objects)
	txCtx = context.WithValue(ctx, extensionObjectTransactionKey{}, commitState)
	if err := objects.Write(txCtx, "module.js", "state", []byte("committed replacement")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := commitState.finish(ctx, false); err != nil {
		t.Fatal(err)
	}
	got, err = objects.Read(ctx, "module.js", "state")
	if err != nil || string(got) != "committed replacement" {
		t.Fatalf("after commit=%q err=%v", got, err)
	}
	var queued bool
	if err := fixture.app.pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.file_object_deletions where object_key=$1)`, originalKey).Scan(&queued); err != nil || !queued {
		t.Fatalf("committed old key queued=%t err=%v", queued, err)
	}
}

func TestExtensionObjectTransactionFinalizationWaitsForOperationsAndRejectsLateRegistration(t *testing.T) {
	state := newExtensionObjectTransactionState(nil, nil)
	if !state.acquire() {
		t.Fatal("active transaction state rejected operation")
	}
	done := make(chan error, 1)
	go func() { done <- state.finish(t.Context(), false) }()
	for {
		state.mu.Lock()
		closing := state.closing
		state.mu.Unlock()
		if closing {
			break
		}
		runtime.Gosched()
	}
	select {
	case <-done:
		t.Fatal("transaction finalized while an operation remained active")
	default:
	}
	if state.register("late") {
		t.Fatal("closing transaction accepted a late upload")
	}
	state.release()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestExtensionHostCloseDoesNotHoldFileMutexWhileFinalizingObjectTransaction(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	tx, err := fixture.app.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	state := newExtensionObjectTransactionState(tx, fixture.app.extensions.objects)
	if !state.acquire() {
		t.Fatal("transaction state rejected operation")
	}
	host := &extensionHost{bridge: fixture.app.extensions, authorityContext: t.Context(), databaseTx: tx, databaseToken: uuid.New(), objectTx: state}
	closeDone := make(chan error, 1)
	go func() { closeDone <- host.Close() }()
	for {
		state.mu.Lock()
		closing := state.closing
		state.mu.Unlock()
		if closing {
			break
		}
		runtime.Gosched()
	}
	fileLock := make(chan struct{})
	go func() {
		host.fileMu.Lock()
		host.fileMu.Unlock()
		close(fileLock)
		state.release()
	}()
	select {
	case <-fileLock:
	case <-time.After(time.Second):
		state.release()
		t.Fatal("host Close held fileMu while waiting for transaction operations")
	}
	if err := <-closeDone; err != nil {
		t.Fatal(err)
	}
}

func TestExtensionObjectHostTransactionReadsWritesListsAndDeletesItsOwnChanges(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024}
	})
	_, _ = setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	if err := fixture.app.extensions.objects.Write(t.Context(), "route.js", "state", []byte("original")); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/tx","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control","objects"]}]}`, `const pg=require("pgbase");exports.run=()=>{let rollbackRead="",rollbackListed=false,deleted=false;try{$app.runInTransaction(()=>{pg.objects.write({key:"state",data:"cm9sbGVk"});rollbackRead=pg.objects.read({key:"state"}).data;rollbackListed=pg.objects.list({prefix:"sta"}).Objects.some(v=>v.Key==="state");pg.objects.delete({key:"state"});try{pg.objects.read({key:"state"})}catch(_){deleted=true}throw new Error("rollback")})}catch(_){}let commitRead="";try{$app.runInTransaction(()=>{pg.objects.write({key:"state",data:"Y29tbWl0dGVk"});commitRead=pg.objects.read({key:"state"}).data})}catch(e){return {fatal:String(e),rollbackRead,rollbackListed,deleted}}return {rollbackRead,rollbackListed,deleted,commitRead}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "object-tx", valid: true, authorityContext: t.Context(), namespace: "route.js"}
	out, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control", "objects"}})
	if err != nil {
		t.Fatalf("%v: %v", err, errors.Unwrap(err))
	}
	if string(out) != `{"commitRead":"Y29tbWl0dGVk","deleted":true,"rollbackListed":true,"rollbackRead":"cm9sbGVk"}` {
		t.Fatalf("transaction object behavior=%s", out)
	}
	got, err := fixture.app.extensions.objects.Read(t.Context(), "route.js", "state")
	if err != nil || string(got) != "committed" {
		t.Fatalf("committed object=%q err=%v", got, err)
	}
}

func TestExtensionObjectWriteEntryPointsRunOneVetoHook(t *testing.T) {
	files := t.TempDir()
	var calls atomic.Int32
	hooks := &HookRegistry{}
	veto := errors.New("write veto")
	if _, err := hooks.On(HookObjectWrite, Hook{Before: func(_ context.Context, eventData *HookEvent) error {
		calls.Add(1)
		if event, ok := eventData.Data.(*ExtensionObjectEvent); !ok || event.Size != 1 {
			return errors.New("invalid write hook size")
		}
		return veto
	}}); err != nil {
		t.Fatal(err)
	}
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: files, MaximumBytes: 1024}
		options.Hooks = hooks
	})
	objects, ctx := fixture.app.extensions.objects, t.Context()
	assertVeto := func(name string, operation func() error) {
		t.Helper()
		before := calls.Load()
		if err := operation(); !errors.Is(err, veto) {
			t.Fatalf("%s error=%v", name, err)
		}
		if calls.Load() != before+1 {
			t.Fatalf("%s hook calls=%d want=%d", name, calls.Load(), before+1)
		}
	}
	assertVeto("inline", func() error { return objects.Write(ctx, "module.js", "inline", []byte("x")) })
	assertVeto("stream", func() error {
		_, err := objects.WriteStream(ctx, "module.js", "stream", bytes.NewBufferString("x"))
		return err
	})

	// Seed copy's source without the veto registry, then restore it for publication.
	objects.hooks = nil
	if err := objects.Write(ctx, "module.js", "source", []byte("x")); err != nil {
		t.Fatal(err)
	}
	objects.hooks = hooks
	assertVeto("copy", func() error { _, err := objects.Copy(ctx, "module.js", "source", "copy"); return err })

	host := &extensionHost{bridge: &extensionBridge{objects: objects}, namespace: "module.js"}
	begin, err := host.beginObjectWrite(json.RawMessage(`{"key":"writer"}`))
	if err != nil {
		t.Fatal(err)
	}
	var writer struct{ Handle string }
	if err := json.Unmarshal(begin, &writer); err != nil {
		t.Fatal(err)
	}
	if _, err := host.writeObjectChunk(json.RawMessage(`{"Handle":"` + writer.Handle + `","Data":"eA=="}`)); err != nil {
		t.Fatal(err)
	}
	assertVeto("writer", func() error {
		_, err := host.commitObjectWrite(ctx, json.RawMessage(`{"Handle":"`+writer.Handle+`"}`))
		return err
	})

	temporary, err := os.CreateTemp("", "pgbase-test-multipart-*")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := temporary.WriteString("x"); err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	host.multipartFiles = map[uuid.UUID]extensionObjectWriter{id: {path: temporary.Name(), file: temporary, size: 1}}
	assertVeto("multipart", func() error {
		_, err := host.commitMultipart(ctx, json.RawMessage(`{"Handle":"`+id.String()+`","Key":"multipart"}`))
		return err
	})

	var tracked int
	if err := fixture.app.pool.QueryRow(ctx, `select count(*) from pgbase_meta.extension_objects where logical_key in ('inline','stream','copy','writer','multipart')`).Scan(&tracked); err != nil || tracked != 0 {
		t.Fatalf("vetoed metadata rows=%d err=%v", tracked, err)
	}
}

func TestExtensionObjectPrefixIsLiteralAcrossPaginationAndDeletePrefix(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024}
	})
	objects, ctx := fixture.app.extensions.objects, t.Context()
	for _, key := range []string{"a%/1", "a%/2", "a_/1", "a_/2", "ax/1", "az/1"} {
		if err := objects.Write(ctx, "module.js", key, []byte(key)); err != nil {
			t.Fatal(err)
		}
	}
	page, err := objects.List(ctx, "module.js", "a%/", "", 1)
	if err != nil || len(page.Objects) != 1 || page.Objects[0].Key != "a%/1" || page.NextCursor != "a%/1" {
		t.Fatalf("percent page=%#v err=%v", page, err)
	}
	page, err = objects.List(ctx, "module.js", "a%/", page.NextCursor, 1)
	if err != nil || len(page.Objects) != 1 || page.Objects[0].Key != "a%/2" || page.NextCursor != "" {
		t.Fatalf("percent next page=%#v err=%v", page, err)
	}
	host := &extensionHost{bridge: &extensionBridge{objects: objects}, namespace: "module.js"}
	if _, err := host.callObjects(ctx, "objects.deletePrefix", json.RawMessage(`{"prefix":"a_/"}`)); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"ax/1", "az/1", "a%/1", "a%/2"} {
		if _, err := objects.Read(ctx, "module.js", key); err != nil {
			t.Fatalf("unrelated key %q deleted: %v", key, err)
		}
	}
	for _, key := range []string{"a_/1", "a_/2"} {
		if _, err := objects.Read(ctx, "module.js", key); !errors.Is(err, filestore.ErrNotFound) {
			t.Fatalf("literal-prefix key %q remained: %v", key, err)
		}
	}
}

func TestExtensionObjectsStreamListCopyOnMinIO(t *testing.T) {
	endpoint := testS3Endpoint(t)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "s3", S3Bucket: "pgbase-test", S3Region: "us-east-1", S3Endpoint: endpoint, S3AccessKey: "pgbase-test-access", S3SecretKey: "pgbase-test-secret-0123456789", S3UsePathStyle: true, S3AllowInsecureLoopback: true, MaximumBytes: 1024}
	})
	objects := fixture.app.extensions.objects
	if _, err := objects.WriteStream(t.Context(), "s3.js", "prefix/one", bytes.NewBufferString("one")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = objects.Delete(context.Background(), "s3.js", "prefix/one")
		_ = objects.Delete(context.Background(), "s3.js", "prefix/two")
	})
	if _, err := objects.Copy(t.Context(), "s3.js", "prefix/one", "prefix/two"); err != nil {
		t.Fatal(err)
	}
	page, err := objects.List(t.Context(), "s3.js", "prefix/", "", 10)
	if err != nil || len(page.Objects) != 2 {
		t.Fatalf("s3 page=%#v err=%v", page, err)
	}
}

func testS3Endpoint(t *testing.T) string {
	t.Helper()
	endpoint := os.Getenv("PGBASE_TEST_S3_ENDPOINT")
	if endpoint == "" {
		t.Skip("PGBASE_TEST_S3_ENDPOINT is not set")
	}
	return endpoint
}
