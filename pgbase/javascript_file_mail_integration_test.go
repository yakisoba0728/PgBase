//go:build integration

package pgbase

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestJavaScriptUsesRealFileAndMailServices(t *testing.T) {
	smtpAddress, messages := startN6SMTPFixture(t)
	directory := t.TempDir()
	manifest := `{
 "format":"pgbase.scripts/v1",
 "routes":[
  {"method":"POST","pattern":"/extensions/file-upload","access":"user","handler":{"module":"route.js","export":"upload"},"capabilities":["files"]},
  {"method":"POST","pattern":"/extensions/file-read","access":"user","handler":{"module":"route.js","export":"read"},"capabilities":["files"]},
  {"method":"POST","pattern":"/extensions/file-stream","access":"user","handler":{"module":"route.js","export":"stream"},"capabilities":["files"]},
  {"method":"POST","pattern":"/extensions/file-handle-limit","access":"user","handler":{"module":"route.js","export":"handleLimit"},"capabilities":["files","log"]},
  {"method":"POST","pattern":"/extensions/file-delete","access":"user","handler":{"module":"route.js","export":"remove"},"capabilities":["files"]},
  {"method":"POST","pattern":"/extensions/mail","access":"user","handler":{"module":"route.js","export":"mail"},"capabilities":["mail"]}
 ],
 "hooks":[{"event":"mail.send","before":{"module":"route.js","export":"beforeMail"}}]
}`
	source := `const pg=require("pgbase");
exports.upload=request=>({status:201,json:pg.files.upload(request.json)});
exports.read=request=>({status:200,json:pg.files.read(request.json)});
exports.stream=request=>{try{const opened=pg.files.open(request.json),chunks=[];try{for(;;){const chunk=pg.files.readChunk({handle:opened.handle,maximumBytes:5});if(chunk.dataBase64)chunks.push(chunk.dataBase64);if(chunk.eof)break;}}finally{pg.files.close({handle:opened.handle});}return {status:200,json:{chunks}};}catch(error){return {status:200,json:{error:error.code}};}};
exports.handleLimit=request=>{const handles=[pg.files.open(request.json)];pg.log.info({message:"first-open"});let code="";try{pg.files.open(request.json);}catch(error){code=error.code;}return {status:200,json:{code,handles:handles.length}};};
exports.remove=request=>({status:200,json:pg.files.delete(request.json)});
exports.mail=request=>{pg.mail.send(request.json);return {status:202,json:{sent:true}};};
exports.beforeMail=event=>({patch:{to:event.data.to,subject:"hooked "+event.data.subject,text:event.data.text}});`
	if err := os.WriteFile(filepath.Join(directory, "pgbase.scripts.json"), []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, "route.js"), []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	openLog := &blockingOpenLogHandler{entered: make(chan struct{}), release: make(chan struct{})}
	fixture := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.Files = FileOptions{Driver: "local", LocalDirectory: t.TempDir(), MaximumBytes: 1024,
			MaximumImagePixels: 1_000_000, ThumbnailDimension: 64, DownloadTokenTTL: time.Minute, AllowedTypes: []string{"text/plain"}}
		options.SMTP = SMTPOptions{Address: smtpAddress, From: "pgbase@example.test", ServerName: "localhost", AllowInsecure: true, Timeout: time.Second}
		options.Logger = slog.New(openLog)
		options.JavaScript = &JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{"files": true, "mail": true, "log": true}, Concurrency: 1, Queue: 1}
	})
	adminCookie, csrf := setupPublicUsersAdministrator(t, fixture)
	change := map[string]any{"operation": "create", "name": "documents", "fields": []map[string]any{
		{"name": "title", "type": "text", "required": true}, {"name": "attachment", "type": "file", "options": map[string]any{"maxSelect": 2}},
	}}
	plan := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/plan", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, plan, http.StatusOK)
	var planned struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	decodePublicUsersJSON(t, plan, &planned)
	change["baseVersion"], change["checksum"] = planned.BaseVersion, planned.Checksum
	apply := publicUsersRequest(t, fixture, http.MethodPost, "/api/admin/collections/apply", change, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, apply, http.StatusCreated)
	enable := publicUsersRequest(t, fixture, http.MethodPut, "/api/admin/collections/documents/user-access", map[string]any{"enabled": true}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, enable, http.StatusOK)
	owner := registerPublicUser(t, fixture, "js-file@example.test", publicUserOnePass)
	created := publicUsersRequest(t, fixture, http.MethodPost, "/api/v1/user/collections/documents/records", map[string]any{"title": "file owner"}, owner.Token, nil,
		map[string]string{"Idempotency-Key": "javascript-file-record-0001"})
	assertPublicUsersStatus(t, created, http.StatusCreated)
	var record struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, created, &record)

	content := []byte("byte-exact from JavaScript")
	upload := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/file-upload", map[string]any{
		"collection": "documents", "recordId": record.ID, "field": "attachment", "filename": "n6.txt", "contentType": "text/plain",
		"dataBase64": base64.StdEncoding.EncodeToString(content),
	}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, upload, http.StatusCreated)
	var file struct {
		ID string `json:"id"`
	}
	decodePublicUsersJSON(t, upload, &file)
	read := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/file-read", map[string]any{"fileId": file.ID, "variant": "original"}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, read, http.StatusOK)
	var readResult struct {
		DataBase64 string `json:"dataBase64"`
	}
	decodePublicUsersJSON(t, read, &readResult)
	decoded, err := base64.StdEncoding.DecodeString(readResult.DataBase64)
	if err != nil || !bytes.Equal(decoded, content) {
		t.Fatalf("download data=%q error=%v", decoded, err)
	}
	streamed := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/file-stream", map[string]any{"fileId": file.ID, "variant": "original"}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, streamed, http.StatusOK)
	var streamResult struct {
		Chunks []string `json:"chunks"`
		Error  string   `json:"error"`
	}
	decodePublicUsersJSON(t, streamed, &streamResult)
	if streamResult.Error != "" {
		t.Fatalf("stream error = %s", streamResult.Error)
	}
	var streamBytes []byte
	for _, encoded := range streamResult.Chunks {
		chunk, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		streamBytes = append(streamBytes, chunk...)
	}
	if !bytes.Equal(streamBytes, content) {
		t.Fatalf("streamed data = %q", streamBytes)
	}
	limitResultChannel := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		limitResultChannel <- publicUsersRequest(t, fixture, http.MethodPost, "/extensions/file-handle-limit", map[string]any{"fileId": file.ID}, owner.Token, nil, nil)
	}()
	select {
	case <-openLog.entered:
	case <-time.After(time.Second):
		t.Fatal("first file handle did not open")
	}
	lockConnection, err := fixture.app.pool.OpenCoordinationSession(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer lockConnection.Close(context.Background())
	lockAcquired := make(chan error, 1)
	go func() {
		_, lockErr := lockConnection.Exec(context.Background(), `select pg_advisory_lock($1)`, int64(0x7067626173650034))
		lockAcquired <- lockErr
	}()
	deadline := time.Now().Add(time.Second)
	for {
		var waiting bool
		if err := fixture.app.pool.QueryRow(context.Background(), `select coalesce((select wait_event_type='Lock' and lower(wait_event)='advisory' from pg_stat_activity where pid=$1),false)`, lockConnection.PgConn().PID()).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("exclusive storage transition lock did not queue behind the open reader")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(openLog.release)
	var limit *httptest.ResponseRecorder
	select {
	case limit = <-limitResultChannel:
	case <-time.After(time.Second):
		t.Fatal("second within-cap file open waited behind the exclusive storage lock")
	}
	select {
	case err := <-lockAcquired:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("exclusive storage transition lock did not acquire after invocation cleanup")
	}
	if _, err := lockConnection.Exec(context.Background(), `select pg_advisory_unlock($1)`, int64(0x7067626173650034)); err != nil {
		t.Fatal(err)
	}
	assertPublicUsersStatus(t, limit, http.StatusOK)
	var limitResult struct {
		Code    string `json:"code"`
		Handles int    `json:"handles"`
	}
	decodePublicUsersJSON(t, limit, &limitResult)
	if limitResult.Code != "file_handle_limit" || limitResult.Handles != 1 {
		t.Fatalf("file handle limit = %#v", limitResult)
	}
	mail := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/mail", map[string]any{
		"to": "recipient@example.test", "subject": "subject", "text": "body",
	}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, mail, http.StatusAccepted)
	select {
	case message := <-messages:
		if !strings.Contains(message, "Subject: hooked subject") || !strings.Contains(message, "body") {
			t.Fatalf("SMTP message = %q", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP fixture received no JavaScript mail")
	}
	removed := publicUsersRequest(t, fixture, http.MethodPost, "/extensions/file-delete", map[string]any{"fileId": file.ID}, owner.Token, nil, nil)
	assertPublicUsersStatus(t, removed, http.StatusOK)
	var deleted map[string]any
	decodePublicUsersJSON(t, removed, &deleted)
	if deleted["deleted"] != true {
		t.Fatalf("delete result = %#v", deleted)
	}
}

type blockingOpenLogHandler struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (*blockingOpenLogHandler) Enabled(context.Context, slog.Level) bool { return true }
func (handler *blockingOpenLogHandler) Handle(ctx context.Context, record slog.Record) error {
	if record.Message != "first-open" {
		return nil
	}
	handler.once.Do(func() { close(handler.entered) })
	select {
	case <-handler.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (handler *blockingOpenLogHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }
func (handler *blockingOpenLogHandler) WithGroup(string) slog.Handler      { return handler }

func startN6SMTPFixture(t *testing.T) (string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	messages := make(chan string, 4)
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			go serveN6SMTPFixture(connection, messages)
		}
	}()
	return listener.Addr().String(), messages
}

func serveN6SMTPFixture(connection net.Conn, messages chan<- string) {
	defer connection.Close()
	reader, writer := bufio.NewReader(connection), bufio.NewWriter(connection)
	write := func(value string) { _, _ = writer.WriteString(value); _ = writer.Flush() }
	write("220 localhost fixture\r\n")
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		command := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(command, "EHLO"):
			write("250-localhost\r\n250 8BITMIME\r\n")
		case strings.HasPrefix(command, "HELO"), strings.HasPrefix(command, "MAIL FROM"), strings.HasPrefix(command, "RCPT TO"):
			write("250 ok\r\n")
		case command == "DATA":
			write("354 continue\r\n")
			var body strings.Builder
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					return
				}
				if line == ".\r\n" {
					break
				}
				body.WriteString(line)
			}
			messages <- body.String()
			write("250 queued\r\n")
		case command == "QUIT":
			write("221 bye\r\n")
			return
		default:
			write("502 unsupported\r\n")
		}
	}
}
