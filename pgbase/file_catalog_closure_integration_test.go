//go:build integration

package pgbase

import (
	"bytes"
	"crypto/sha256"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/accessrule"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func TestFileRulesUseFullRelationCatalog(t *testing.T) {
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, MaximumImagePixels: 1000, ThumbnailDimension: 32, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain"}}
	})
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	headers := map[string]string{"X-PgBase-CSRF": csrf}
	apply := func(change map[string]any) {
		preview := publicUsersRequest(t, fixture, "POST", "/api/admin/collections/plan", change, "", admin, headers)
		assertPublicUsersStatus(t, preview, 200)
		var plan struct {
			BaseVersion int64  `json:"baseVersion"`
			Checksum    string `json:"checksum"`
		}
		decodePublicUsersJSON(t, preview, &plan)
		change["baseVersion"], change["checksum"] = plan.BaseVersion, plan.Checksum
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "POST", "/api/admin/collections/apply", change, "", admin, headers), 201)
	}
	apply(map[string]any{"operation": "create", "name": "file_parents", "fields": []map[string]any{{"name": "title", "type": "text"}}})
	apply(map[string]any{"operation": "create", "name": "file_children", "fields": []map[string]any{{"name": "parent", "type": "relation", "relation": map[string]any{"targetCollection": "file_parents", "cardinality": "single", "onDelete": "restrict"}}, {"name": "attachment", "type": "file"}}})
	for _, name := range []string{"file_parents", "file_children"} {
		assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", "/api/admin/collections/"+name+"/user-access", map[string]any{"enabled": true}, "", admin, headers), 200)
	}
	owner := registerPublicUser(t, fixture, "file-catalog@example.test", publicUserOnePass)
	create := func(name string, input map[string]any) string {
		result := publicUsersRequest(t, fixture, "POST", "/api/v1/user/collections/"+name+"/records", input, owner.Token, nil, nil)
		assertPublicUsersStatus(t, result, 201)
		var record struct {
			ID string `json:"id"`
		}
		decodePublicUsersJSON(t, result, &record)
		return record.ID
	}
	parent := create("file_parents", map[string]any{"title": "allowed"})
	child := create("file_children", map[string]any{"parent": parent})
	rules := accessrule.OwnerRules()
	expression := "parent.title = 'allowed'"
	rules.View = accessrule.Rule{Op: "expression", Expression: &expression}
	rules.Update = rules.View
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "PUT", "/api/admin/collections/file_children/rules", map[string]any{"rules": rules}, "", admin, headers), 200)
	assertPublicUsersStatus(t, publicUsersRequest(t, fixture, "GET", "/api/v1/user/collections/file_children/records/"+child, nil, owner.Token, nil, nil), 200)
	files := fixture.app.fileWorker.(*filestore.Service)
	collection, record, err := files.ResolveRecordID(t.Context(), "file_children", child)
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("catalog permission")
	sum := sha256.Sum256(body)
	principal := identity.Subject{Kind: identity.User, ID: owner.User.ID}
	file, err := files.Upload(t.Context(), principal, filestore.Upload{CollectionID: collection, RecordID: record, Field: "attachment", Filename: "file.txt", ContentType: "text/plain", Size: int64(len(body)), SHA256: sum, Body: bytes.NewReader(body)})
	if err != nil {
		t.Fatal(err)
	}
	if list, err := files.List(t.Context(), principal, collection, record, "attachment"); err != nil || len(list) != 1 {
		t.Fatalf("list=%v err=%v", list, err)
	}
	token, err := files.MintDownloadToken(t.Context(), principal, file.ID, filestore.Original)
	if err != nil {
		t.Fatal(err)
	}
	_, reader, err := files.Open(t.Context(), principal, file.ID, filestore.Original, token)
	if err != nil {
		t.Fatal(err)
	}
	if err = reader.Close(); err != nil {
		t.Fatal(err)
	}
	if err := files.Delete(t.Context(), principal, file.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNativeFileUploadAndListResolveTextAndUUIDLookingIDs(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{"format":"pgbase.scripts/v1","routes":[{"method":"POST","pattern":"/native-text-files","access":"administrator","handler":{"module":"route.js","export":"run"},"capabilities":["records","files"]}]}`, `const pg=require("pgbase");exports.run=()=>{
 const counts=[];
 for(const id of ["ExactFile01","11111111-1111-4111-8111-111111111111"]){
  const record=pg.records.create({collection:"native_text_files",input:{id}}).data;
  const file=pg.files.upload({collection:"native_text_files",recordId:record.id,field:"attachment",filename:"test.txt",contentType:"text/plain",dataBase64:"YQ=="});
  const listed=pg.files.list({collection:"native_text_files",recordId:record.id,field:"attachment"});
  if(listed.items.length!==1||listed.items[0].id!==file.id)throw Error("wrong file association");
  counts.push(listed.items.length);
 }
 return {status:200,json:{counts}};
};`)
	fixture := newPublicUsersAppOptions(t, false, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024, MaximumImagePixels: 1000, ThumbnailDimension: 32, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain"}}
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"records": true, "files": true}}
	})
	admin, csrf := setupPublicUsersAdministrator(t, fixture)
	var actor uuid.UUID
	if err := fixture.app.pool.QueryRow(t.Context(), `select id from pgbase_meta.admins where email=$1`, publicAdminEmail).Scan(&actor); err != nil {
		t.Fatal(err)
	}
	applyN6SchemaChange(t, fixture.app.schemaOperations.(*schema.Service), actor, schema.NewCreateChange(schema.CreateCollectionInput{Name: "native_text_files", RecordID: &schema.RecordIDPolicy{Kind: schema.RecordIDText, MinLength: 1, MaxLength: 40, Pattern: `^[A-Za-z0-9-]+$`}, Fields: []schema.FieldInput{{Name: "attachment", Type: schema.TypeFile}}}))
	response := publicUsersRequest(t, fixture, "POST", "/native-text-files", nil, "", admin, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, response, 200)
	var count int
	if err := fixture.app.pool.QueryRow(t.Context(), `select count(*) from pgbase_meta.record_files f join pgbase_meta.record_identities i on i.internal_id=f.record_id where i.external_id in ('ExactFile01','11111111-1111-4111-8111-111111111111') and f.status='ready'`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("native file identities count=%d err=%v", count, err)
	}
}
