//go:build integration

package pgbase

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/jsvm"
	"github.com/yakisoba0728/PgBase/internal/schema"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestNativeRecordsEmptyPageAlwaysHasItems(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "sdk_empty_page_probe")
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v2","routes":[{"method":"GET","pattern":"/probe","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["records"]}]}`, `const pg=require("pgbase");exports.run=()=>{const page=pg.records.list({collection:"sdk_empty_page_probe"});let lengthThrows=false;try{page.items.length}catch(_){lengthThrows=true}return {page,itemsType:typeof page.items,lengthThrows}};`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "sdk-empty-page-probe", valid: true, authorityContext: t.Context()}
	output, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"records"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("actual PostgreSQL -> recordops -> extension bridge -> native JS result: %s", output)
	var result struct {
		Page         map[string]json.RawMessage `json:"page"`
		ItemsType    string                     `json:"itemsType"`
		LengthThrows bool                       `json:"lengthThrows"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err)
	}
	if result.ItemsType != "object" || result.LengthThrows {
		t.Fatalf("empty page contract violated: %s", output)
	}
	if items, exists := result.Page["items"]; !exists || string(items) != "[]" {
		t.Fatalf("unexpected items: %s", output)
	}
}

func TestExtensionObjectUncertainCommitCleanupPreservesReferencedBytes(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024}
	})
	objects, ctx := fixture.app.extensions.objects, t.Context()
	if err := objects.Write(ctx, "module.js", "state", []byte("committed")); err != nil {
		t.Fatal(err)
	}
	physical, err := objects.physicalKey(ctx, "module.js", "state")
	if err != nil {
		t.Fatal(err)
	}
	logical, _ := extensionObjectKey("module.js", "state")
	// Exercise the error cleanup after a server-side commit whose acknowledgement was lost.
	if err := objects.cleanupUnpublished(ctx, logical, physical); err != nil {
		t.Fatal(err)
	}
	data, err := objects.Read(ctx, "module.js", "state")
	if err != nil || string(data) != "committed" {
		t.Fatalf("committed bytes=%q err=%v", data, err)
	}
	orphan := logical + "/" + uuid.NewString()
	if err := objects.store.Put(ctx, orphan, bytes.NewBufferString("orphan")); err != nil {
		t.Fatal(err)
	}
	if err := objects.cleanupUnpublished(ctx, logical, orphan); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.store.Open(ctx, orphan); !errors.Is(err, filestore.ErrNotFound) {
		t.Fatalf("orphan remains err=%v", err)
	}
}

func TestExtensionObjectExternalTransactionRetainsStorageLeaseUntilFinish(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024}
	})
	objects, ctx := fixture.app.extensions.objects, t.Context()
	controller := objects.store.(*filestore.StorageController)
	settings, err := controller.Settings(ctx)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := fixture.app.pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(context.Background())
	state := newExtensionObjectTransactionState(tx, objects)
	defer state.finish(ctx, false)
	callCtx, cancel := context.WithCancel(context.WithValue(ctx, extensionObjectTransactionKey{}, state))
	if err := objects.Write(callCtx, "module.js", "state", []byte("first")); err != nil {
		t.Fatal(err)
	}
	cancel() // Host operation contexts end before the app transaction.
	nextCtx := context.WithValue(ctx, extensionObjectTransactionKey{}, state)
	if err := objects.Write(nextCtx, "module.js", "state", []byte("second")); err != nil {
		t.Fatal(err)
	}
	if _, err := objects.Copy(nextCtx, "module.js", "state", "copy"); err != nil {
		t.Fatal(err)
	}
	blocked, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err = controller.Update(blocked, filestore.StorageUpdate{Revision: settings.Revision, Driver: "local"})
	stop()
	if err == nil {
		t.Fatal("storage transition succeeded before transaction completion")
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err := state.finish(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Update(ctx, filestore.StorageUpdate{Revision: settings.Revision, Driver: "local"}); err != nil {
		t.Fatalf("storage transition after commit: %v", err)
	}
	data, err := objects.Read(ctx, "module.js", "copy")
	if err != nil || string(data) != "second" {
		t.Fatalf("copied=%q err=%v", data, err)
	}
}

func TestExtensionObjectLostCommitAcknowledgementPreservesLiveBytes(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024}
	})
	target, err := url.Parse(fixture.app.options.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	upstreamAddress := target.Host
	if target.Port() == "" {
		upstreamAddress = net.JoinHostPort(target.Hostname(), "5432")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var dropCommit atomic.Bool
	var dropped atomic.Bool
	go func() {
		for {
			client, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer client.Close()
				upstream, err := net.Dial("tcp", upstreamAddress)
				if err != nil {
					return
				}
				defer upstream.Close()
				go func() { _, _ = io.Copy(upstream, client); _ = upstream.Close() }()
				for {
					var header [5]byte
					if _, err := io.ReadFull(upstream, header[:]); err != nil {
						return
					}
					length := binary.BigEndian.Uint32(header[1:])
					if length < 4 || length > 16<<20 {
						return
					}
					payload := make([]byte, length-4)
					if _, err := io.ReadFull(upstream, payload); err != nil {
						return
					}
					if header[0] == 'C' && string(payload) == "COMMIT\x00" && dropCommit.CompareAndSwap(true, false) {
						dropped.Store(true) // CommandComplete is sent only after the server committed.
						return
					}
					if _, err := client.Write(header[:]); err != nil {
						return
					}
					if _, err := client.Write(payload); err != nil {
						return
					}
				}
			}()
		}
	}()
	target.Host = listener.Addr().String()
	query := target.Query()
	query.Set("sslmode", "disable")
	target.RawQuery = query.Encode()
	pool, err := database.Open(t.Context(), target.String(), time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	objects := *fixture.app.extensions.objects
	objects.pool = pool
	dropCommit.Store(true)
	if err := objects.Write(t.Context(), "module.js", "uncertain", []byte("committed despite disconnect")); err == nil {
		t.Fatal("lost COMMIT acknowledgement did not surface an error")
	}
	if !dropped.Load() {
		t.Fatal("COMMIT acknowledgement was not dropped")
	}
	data, err := fixture.app.extensions.objects.Read(t.Context(), "module.js", "uncertain")
	if err != nil || string(data) != "committed despite disconnect" {
		t.Fatalf("committed bytes=%q err=%v", data, err)
	}
}

func TestLegacyCollectionReadSaveAndFindersPreserveExactJSON(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "legacy-exact", valid: true, authorityContext: t.Context()}
	if _, err := host.Call(t.Context(), "app-control.collectionSave", json.RawMessage(`{"collection":{"name":"legacy_exact","fields":[{"name":"title","type":"text"},{"name":"payload","type":"jsonb"}],"rules":{"list":{"op":"owner"},"view":{"op":"owner"},"create":{"op":"owner"},"update":{"op":"owner"},"delete":{"op":"owner"}}}}`)); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"app.record.findByIds", "app.record.findAll", "app.record.expandMany"} {
		page, err := host.callAppRecordQuery(t.Context(), method, extensionAppQueryRequest{Collection: "legacy_exact"})
		if err != nil || string(page) != `{"items":[]}` {
			t.Fatalf("empty %s=%s err=%v", method, page, err)
		}
	}
	raw, err := host.Call(t.Context(), "records.create", json.RawMessage(`{"collection":"legacy_exact","input":{"title":"one","payload":{"n":9007199254740993}}}`))
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"app.record.findByIds", "app.record.expandMany", "app.record.findAll", "app.record.findFirstByData"} {
		raw, err := host.callAppRecordQuery(t.Context(), method, extensionAppQueryRequest{Collection: "legacy_exact", IDs: []string{created.Data.ID}, Field: "title", Value: json.RawMessage(`"one"`)})
		if err != nil || !strings.Contains(string(raw), "9007199254740993") {
			t.Fatalf("%s raw=%s err=%v", method, raw, err)
		}
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/save","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control"]}]}`, `exports.run=()=>{const collection=$app.findCollectionByNameOrId("legacy_exact");$app.save(collection);return {name:collection.name}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	if raw, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control"}}); err != nil {
		t.Fatalf("collection read/save=%s err=%v", raw, err)
	}
}

func TestLegacyAppMigrationsMergeRegisteredGoAndJavaScript(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","migrations":[{"id":"202611130002_js","module":"route.js","up":"up","irreversibleReason":"test fixture"}]}`, `exports.up=()=>{require("pgbase").migration.exec({sql:"insert into app.closure_go_migration values (2)"})}`)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.JavaScript = &JavaScriptOptions{Directory: directory} })
	registeredUserMigrations.Lock()
	previous := registeredUserMigrations.items
	registeredUserMigrations.items = []UserMigration{{ID: "202611130001_go", Source: []byte("closure Go migration"), IrreversibleReason: "test fixture", Down: func(context.Context, pgx.Tx) error { return nil }, Up: func(ctx context.Context, tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `create table app.closure_go_migration(n integer); insert into app.closure_go_migration values(1)`)
		return err
	}}}
	registeredUserMigrations.Unlock()
	defer func() {
		registeredUserMigrations.Lock()
		registeredUserMigrations.items = previous
		registeredUserMigrations.Unlock()
	}()
	control := &extensionAppControl{app: fixture.app}
	if _, err := control.runAppMigrations(t.Context()); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := fixture.app.pool.QueryRow(t.Context(), `select count(*) from app.closure_go_migration`).Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("merged migrations rows=%d err=%v", rows, err)
	}
}

func TestLegacyDryRunViewReleasesMetadataConnectionBeforeSample(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	// Use a separate pool so background workers cannot occupy the last connection.
	poolURL, err := url.Parse(fixture.app.options.DatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	poolQuery := poolURL.Query()
	poolQuery.Set("pool_max_conns", "4")
	poolURL.RawQuery = poolQuery.Encode()
	pool, err := database.Open(t.Context(), poolURL.String(), time.Second, true)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	held := make([]interface{ Release() }, 0)
	defer func() {
		for _, conn := range held {
			conn.Release()
		}
	}()
	for index := int32(1); index < pool.Stats().MaxConns; index++ {
		conn, err := pool.Acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, conn)
	}
	control := &extensionAppControl{app: &App{pool: pool}}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	raw, err := control.callDatabaseMetadata(ctx, "app-control.databaseDryRunView", json.RawMessage(`{"name":"probe","query":"select '00000000-0000-0000-0000-000000000001'::uuid id", "sampleSize":1}`))
	var result struct {
		Items []map[string]any `json:"items"`
	}
	if err != nil || json.Unmarshal(raw, &result) != nil || len(result.Items) != 1 {
		t.Fatalf("dryRun=%s err=%v", raw, err)
	}
}

func TestDirectAppBackupFilesystemUsesConstructedController(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.Backups.Directory = t.TempDir() })
	if fixture.app.backupManager == nil || fixture.app.options.Backups.controller == nil {
		t.Fatal("constructed backup facade dependencies missing")
	}
	control := &extensionAppControl{app: fixture.app}
	if raw, err := control.callBackupFilesystem(t.Context(), "app-control.backupList", json.RawMessage(`{}`)); err != nil || !json.Valid(raw) {
		t.Fatalf("backup list=%s err=%v", raw, err)
	}
}

func TestLegacyCollectionDeleteRemovesOnlyRequestedEmptyCollection(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, nil)
	cookie, csrf := setupPublicUsersAdministrator(t, fixture)
	createPublicUsersCollection(t, fixture, cookie, csrf, "delete_me")
	createPublicUsersCollection(t, fixture, cookie, csrf, "keep_me")
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "delete-empty", valid: true, authorityContext: t.Context()}
	if _, err := host.Call(t.Context(), "app-control.collectionDelete", json.RawMessage(`{"collection":{"name":"delete_me"}}`)); err != nil {
		t.Fatal(err)
	}
	catalog := fixture.app.schemaCache.Load()
	if _, exists := catalog.Collections["delete_me"]; exists {
		t.Fatal("deleted collection remains")
	}
	if _, exists := catalog.Collections["keep_me"]; !exists {
		t.Fatal("unrelated collection deleted")
	}
}

func TestLegacyDatabaseStatementResultIsStableInsideAndOutsideTransaction(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) { options.EnableTrustedDatabaseAccess = true })
	setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"GET","pattern":"/db-shape","access":"admin","handler":{"module":"route.js","export":"run"},"capabilities":["app-control","database"]}]}`, `exports.run=()=>{const db=$app.db();db.exec("create table app.sdk_statement_contract(n integer)");const inserted=db.exec("insert into app.sdk_statement_contract values(1)");const outside=db.query("select n from app.sdk_statement_contract");let inside;$app.runInTransaction(tx=>{inside=tx.db().query("select n from app.sdk_statement_contract")});const explicit=db.transaction([{sql:"select n from app.sdk_statement_contract",query:true}]);return {inserted:String(inserted.rowsAffected),outside:outside.items.length,inside:inside.items.length,explicit:explicit.results[0].items.length}}`)
	bundle, err := jsvm.LoadBundle(directory)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := jsvm.NewRuntime(bundle, jsvm.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close(context.Background())
	host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "statement-contract", valid: true, authorityContext: t.Context()}
	raw, err := runtime.Invoke(t.Context(), jsvm.Invocation{Handler: bundle.Manifest().Routes[0].Handler, Host: host, Capabilities: []string{"app-control", "database"}})
	if err != nil || string(raw) != `{"explicit":1,"inserted":"1","inside":1,"outside":1}` {
		t.Fatalf("statement results=%s err=%v", raw, err)
	}
}

func TestLegacyCollectionDeleteSoleAndPairedAuthContracts(t *testing.T) {
	for _, kind := range []string{"sole base", "empty paired auth", "paired auth with users", "default paired auth"} {
		t.Run(kind, func(t *testing.T) {
			fixture := newPublicUsersAppOptions(t, false, nil)
			cookie, csrf := setupPublicUsersAdministrator(t, fixture)
			var actor uuid.UUID
			if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
				t.Fatal(err)
			}
			host := &extensionHost{bridge: fixture.app.extensions, principal: identity.Subject{Kind: identity.Administrator, ID: actor}, requestID: "delete-contract", valid: true, authorityContext: t.Context()}
			name := "sole_collection"
			if kind == "sole base" {
				createPublicUsersCollection(t, fixture, cookie, csrf, name)
				if count := len(fixture.app.schemaCache.Load().Collections); count != 1 {
					t.Fatalf("sole collection precondition count=%d", count)
				}
			} else {
				name = "paired_collection"
				if kind == "default paired auth" {
					name = "users"
				}
				var scaffolds struct {
					Auth struct {
						Collection     schema.SchemaDocumentCollection     `json:"collection"`
						AuthCollection schema.SchemaDocumentAuthCollection `json:"authCollection"`
					} `json:"auth"`
				}
				response := publicUsersRequest(t, fixture, http.MethodGet, "/api/admin/collections/scaffolds", nil, "", cookie, nil)
				assertPublicUsersStatus(t, response, http.StatusOK)
				decodePublicUsersJSON(t, response, &scaffolds)
				service := fixture.app.schemaOperations.(*schema.Service)
				document, err := service.ExportSchema(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				scaffolds.Auth.Collection.Name = name
				document.Collections = append(document.Collections, scaffolds.Auth.Collection)
				if kind == "default paired auth" {
					// Preserve the default realm document; only add its paired catalog collection.
				} else {
					scaffolds.Auth.AuthCollection.Name = name
					scaffolds.Auth.AuthCollection.RegistrationEnabled = true
					document.AuthCollections = append(document.AuthCollections, scaffolds.Auth.AuthCollection)
				}
				plan, err := service.PreviewSchemaImport(t.Context(), document)
				if err != nil {
					t.Fatal(err)
				}
				input, _ := json.Marshal(extensionSchemaApplyRequest{Document: document, BaseVersion: plan.BaseVersion, Checksum: plan.Checksum})
				if _, err := host.Call(t.Context(), "app-control.schemaApply", input); err != nil {
					t.Fatal(err)
				}
				if kind == "paired auth with users" {
					registered := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/auth/"+name+"/register", map[string]any{"email": "paired@example.test", "password": publicUserOnePass}, "", nil, nil)
					assertPublicUsersStatus(t, registered, http.StatusCreated)
				}
			}
			var realmBefore string
			_ = fixture.app.pool.QueryRow(t.Context(), `select id::text from pgbase_meta.auth_collections where name=$1`, name).Scan(&realmBefore)
			input, _ := json.Marshal(map[string]any{"collection": map[string]string{"name": name}})
			result, err := host.Call(t.Context(), "app-control.collectionDelete", input)
			protected := kind == "paired auth with users" || kind == "default paired auth"
			if (err != nil) != protected {
				t.Fatalf("delete error=%v protected=%t", err, protected)
			}
			if !protected {
				var receipt schema.SchemaImportResult
				if err := json.Unmarshal(result, &receipt); err != nil || receipt.Status != "committed" || receipt.Applied != 1 || receipt.Version < 1 || receipt.OperationID == uuid.Nil {
					t.Fatalf("delete result=%s err=%v", result, err)
				}
			}
			_, catalogExists := fixture.app.schemaCache.Load().Collections[name]
			if catalogExists != protected {
				t.Fatalf("catalog exists=%t protected=%t", catalogExists, protected)
			}
			var realmAfter string
			_ = fixture.app.pool.QueryRow(t.Context(), `select id::text from pgbase_meta.auth_collections where name=$1`, name).Scan(&realmAfter)
			if protected && realmAfter != realmBefore {
				t.Fatalf("protected realm changed before=%s after=%s", realmBefore, realmAfter)
			}
			if !protected && realmAfter != "" {
				t.Fatalf("deleted paired realm remains/recreated: before=%s after=%s", realmBefore, realmAfter)
			}
			if kind == "empty paired auth" {
				var policies int
				if err := fixture.app.pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.auth_collection_policies where auth_collection_id=$1::uuid`, realmBefore).Scan(&policies); err != nil || policies != 0 {
					t.Fatalf("paired realm policy cascade count=%d err=%v", policies, err)
				}
			}
			var defaultExists bool
			if err := fixture.app.pool.QueryRow(t.Context(), `select exists(select 1 from pgbase_meta.auth_collections where id='00000000-0000-0000-0000-000000000001')`).Scan(&defaultExists); err != nil || !defaultExists {
				t.Fatalf("default realm lost err=%v", err)
			}
		})
	}
}
