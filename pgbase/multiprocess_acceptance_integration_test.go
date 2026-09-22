//go:build integration && multiprocess

package pgbase

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/testutil"
)

// TestMultiProcessBoundedFailureAcceptance is a local correctness acceptance
// workload. It deliberately makes no production capacity or availability claim.
func TestMultiProcessBoundedFailureAcceptance(t *testing.T) {
	databaseURL := testutil.NewDatabase(t)
	binary := buildMultiProcessBinary(t)
	firstAddress := reserveMultiProcessAddress(t)
	secondAddress := reserveMultiProcessAddress(t)
	first := startMultiProcessServer(t, binary, databaseURL, firstAddress, true)
	defer func() { first.stop(t) }()
	second := startMultiProcessServer(t, binary, databaseURL, secondAddress, false)
	defer second.stop(t)

	firstURL, secondURL := "http://"+firstAddress, "http://"+secondAddress
	admin, csrf := multiProcessSetup(t, firstURL)
	multiProcessCreateCollection(t, firstURL, admin, csrf)
	user := multiProcessRegister(t, secondURL)

	const uniqueWrites = 32
	statuses := make(map[int]int)
	var statusMu sync.Mutex
	var wg sync.WaitGroup
	for index := 0; index < uniqueWrites; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			base := firstURL
			if index%2 == 1 {
				base = secondURL
			}
			response := multiProcessRequest(t, base, http.MethodPost, "/api/v1/user/collections/mp_notes/records",
				map[string]any{"title": fmt.Sprintf("unique-%02d", index)}, user.Token, nil,
				map[string]string{"Idempotency-Key": fmt.Sprintf("mp-unique-record-%02d", index)})
			statusMu.Lock()
			statuses[response.StatusCode]++
			statusMu.Unlock()
			response.Body.Close()
		}()
	}
	wg.Wait()
	if statuses[http.StatusCreated] != uniqueWrites || len(statuses) != 1 {
		t.Fatalf("concurrent unique writes status classes=%v, want map[201:%d]", statuses, uniqueWrites)
	}

	const replayRequests = 12
	replayIDs := make(chan string, replayRequests)
	statuses = make(map[int]int)
	for index := 0; index < replayRequests; index++ {
		index := index
		wg.Add(1)
		go func() {
			defer wg.Done()
			base := firstURL
			if index%2 == 1 {
				base = secondURL
			}
			response := multiProcessRequest(t, base, http.MethodPost, "/api/v1/user/collections/mp_notes/records",
				map[string]any{"title": "one-idempotent-record"}, user.Token, nil,
				map[string]string{"Idempotency-Key": "mp-shared-idempotency-key"})
			defer response.Body.Close()
			var record struct {
				ID string `json:"id"`
			}
			if response.StatusCode == http.StatusCreated {
				_ = json.NewDecoder(response.Body).Decode(&record)
			}
			statusMu.Lock()
			statuses[response.StatusCode]++
			statusMu.Unlock()
			replayIDs <- record.ID
		}()
	}
	wg.Wait()
	close(replayIDs)
	ids := make(map[string]int)
	for id := range replayIDs {
		if id != "" {
			ids[id]++
		}
	}
	if statuses[http.StatusCreated] == 0 || statuses[http.StatusCreated]+statuses[http.StatusConflict] != replayRequests || len(statuses) > 2 || len(ids) != 1 {
		t.Fatalf("concurrent idempotent writes status classes=%v durable IDs=%v", statuses, ids)
	}
	for _, base := range []string{firstURL, secondURL} {
		response := multiProcessRequest(t, base, http.MethodPost, "/api/v1/user/collections/mp_notes/records",
			map[string]any{"title": "one-idempotent-record"}, user.Token, nil,
			map[string]string{"Idempotency-Key": "mp-shared-idempotency-key"})
		var replayed struct {
			ID string `json:"id"`
		}
		multiProcessDecode(t, response, http.StatusCreated, &replayed)
		if ids[replayed.ID] == 0 {
			t.Fatalf("post-contention replay on %s returned new ID %s; initial IDs=%v", base, replayed.ID, ids)
		}
	}

	stream := openMultiProcessStream(t, firstURL, user.Token, "")
	checkpoint := stream.next(t, "cursor")
	createdBeforeKill := multiProcessCreateRecord(t, secondURL, user.Token, "before-kill", "mp-before-kill-record")
	beforeEvent := stream.nextRecordContaining(t, createdBeforeKill)
	checkpoint = beforeEvent

	first.kill(t)
	stream.waitClosed(t)
	createdDuringKill := multiProcessCreateRecord(t, secondURL, user.Token, "during-kill", "mp-during-kill-record")
	first = startMultiProcessServer(t, binary, databaseURL, firstAddress, false)
	resumed := openMultiProcessStream(t, firstURL, user.Token, checkpoint.id)
	_ = resumed.nextRecordContaining(t, createdDuringKill)

	logout := multiProcessRequest(t, firstURL, http.MethodPost, "/api/v1/auth/logout", nil, user.Token, nil, nil)
	if logout.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status=%d", logout.StatusCode)
	}
	logout.Body.Close()
	revoked := multiProcessRequest(t, secondURL, http.MethodGet, "/api/v1/auth/session", nil, user.Token, nil, nil)
	if revoked.StatusCode != http.StatusUnauthorized {
		t.Fatalf("cross-process revoked session status=%d, want 401", revoked.StatusCode)
	}
	revoked.Body.Close()
	resumed.waitClosed(t)

	page := multiProcessRequest(t, secondURL, http.MethodGet, "/api/v1/user/collections/mp_notes/records?limit=100", nil, user.Token, nil, nil)
	if page.StatusCode != http.StatusUnauthorized {
		t.Fatalf("revoked token listing status=%d", page.StatusCode)
	}
	page.Body.Close()
	login := multiProcessLogin(t, secondURL)
	page = multiProcessRequest(t, firstURL, http.MethodGet, "/api/v1/user/collections/mp_notes/records?limit=100", nil, login.Token, nil, nil)
	defer page.Body.Close()
	if page.StatusCode != http.StatusOK {
		t.Fatalf("durability listing status=%d", page.StatusCode)
	}
	var listing struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.NewDecoder(page.Body).Decode(&listing); err != nil {
		t.Fatal(err)
	}
	wantDurable := uniqueWrites + 1 + 2
	if len(listing.Items) != wantDurable {
		t.Fatalf("durable checkpoint records=%d, want %d (unique=%d idempotent=1 failure-window=2)", len(listing.Items), wantDurable, uniqueWrites)
	}
	t.Logf("bounded multi-process acceptance: pids=2 unique_requests=%d idempotent_requests=%d idempotent_status_classes=%v durable_records=%d killed_and_restarted=1 realtime_resumed=1 cross_process_revocation=1", uniqueWrites, replayRequests, statuses, len(listing.Items))
}

type multiProcessServer struct {
	command *exec.Cmd
	logs    *lockedBuffer
	stopped bool
}

func startMultiProcessServer(t *testing.T, binary, databaseURL, address string, migrate bool) *multiProcessServer {
	t.Helper()
	logs := &lockedBuffer{}
	command := exec.Command(binary, "serve")
	command.Env = multiProcessEnvironment(map[string]string{
		"PGBASE_DATABASE_URL": databaseURL, "PGBASE_HTTP_ADDRESS": address,
		"PGBASE_PUBLIC_URL": "https://pgbase.test", "PGBASE_SETUP_TOKEN": publicSetupToken,
		"PGBASE_TOKEN_SECRET": base64.StdEncoding.EncodeToString([]byte("multi-process-token-secret-32-bytes")),
		"PGBASE_AUTO_MIGRATE": fmt.Sprint(migrate), "PGBASE_USER_REGISTRATION": "true",
		"PGBASE_ALLOW_INSECURE_DATABASE": "true",
	})
	command.Stdout, command.Stderr = logs, logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	server := &multiProcessServer{command: command, logs: logs}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		response, err := (&http.Client{Timeout: 300 * time.Millisecond}).Get("http://" + address + "/health/ready")
		if err == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return server
			}
		}
		if command.ProcessState != nil {
			t.Fatalf("server %s exited during startup: %s", address, logs.String())
		}
		time.Sleep(50 * time.Millisecond)
	}
	server.kill(t)
	t.Fatalf("server %s was not ready: %s", address, logs.String())
	return nil
}

func (s *multiProcessServer) kill(t *testing.T) {
	t.Helper()
	if s == nil || s.stopped {
		return
	}
	s.stopped = true
	if err := s.command.Process.Kill(); err != nil {
		t.Fatalf("kill owned server pid %d: %v", s.command.Process.Pid, err)
	}
	if err := s.command.Wait(); err == nil {
		t.Fatalf("killed server pid %d exited without kill status", s.command.Process.Pid)
	}
}

func (s *multiProcessServer) stop(t *testing.T) {
	t.Helper()
	if s == nil || s.stopped {
		return
	}
	s.kill(t)
}

type lockedBuffer struct {
	sync.Mutex
	bytes.Buffer
}

func (b *lockedBuffer) Write(value []byte) (int, error) {
	b.Lock()
	defer b.Unlock()
	return b.Buffer.Write(value)
}
func (b *lockedBuffer) String() string { b.Lock(); defer b.Unlock(); return b.Buffer.String() }

func buildMultiProcessBinary(t *testing.T) string {
	t.Helper()
	extension := ""
	if runtime.GOOS == "windows" {
		extension = ".exe"
	}
	binary := filepath.Join(t.TempDir(), "pgbase-multiprocess"+extension)
	command := exec.Command("go", "build", "-o", binary, "../cmd/pgbase")
	command.Dir = "."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build subprocess binary: %v\n%s", err, output)
	}
	return binary
}

func reserveMultiProcessAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func multiProcessEnvironment(overrides map[string]string) []string {
	values := make(map[string]string)
	for _, entry := range os.Environ() {
		if key, value, ok := strings.Cut(entry, "="); ok {
			values[strings.ToUpper(key)] = value
		}
	}
	for key, value := range overrides {
		values[strings.ToUpper(key)] = value
	}
	result := make([]string, 0, len(values))
	for key, value := range values {
		result = append(result, key+"="+value)
	}
	sort.Strings(result)
	return result
}

type multiProcessResponse struct {
	StatusCode int
	Header     http.Header
	Body       io.ReadCloser
}

func multiProcessRequest(t *testing.T, base, method, path string, body any, bearer string, cookie *http.Cookie, headers map[string]string) *multiProcessResponse {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(encoded)
	}
	request, err := http.NewRequest(method, base+path, reader)
	if err != nil {
		t.Fatal(err)
	}
	request.Host = "pgbase.test"
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if method != http.MethodGet {
		request.Header.Set("Origin", publicUsersOrigin)
	}
	if bearer != "" {
		request.Header.Set("Authorization", "Bearer "+bearer)
	}
	if cookie != nil {
		request.AddCookie(cookie)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return &multiProcessResponse{StatusCode: response.StatusCode, Header: response.Header, Body: response.Body}
}

func multiProcessDecode(t *testing.T, response *multiProcessResponse, want int, destination any) {
	t.Helper()
	defer response.Body.Close()
	if response.StatusCode != want {
		raw, _ := io.ReadAll(response.Body)
		t.Fatalf("status=%d want=%d body=%s", response.StatusCode, want, raw)
	}
	if destination != nil {
		if err := json.NewDecoder(response.Body).Decode(destination); err != nil {
			t.Fatal(err)
		}
	}
}

func multiProcessSetup(t *testing.T, base string) (*http.Cookie, string) {
	response := multiProcessRequest(t, base, http.MethodPost, "/api/admin/setup", map[string]any{"setupToken": publicSetupToken, "email": publicAdminEmail, "password": publicAdminPass}, "", nil, nil)
	var session publicAdminSession
	cookies := response.Header.Values("Set-Cookie")
	multiProcessDecode(t, response, http.StatusCreated, &session)
	parsed := (&http.Response{Header: http.Header{"Set-Cookie": cookies}}).Cookies()
	if len(parsed) != 1 {
		t.Fatalf("setup cookies=%d", len(parsed))
	}
	return parsed[0], session.CSRFToken
}

func multiProcessCreateCollection(t *testing.T, base string, cookie *http.Cookie, csrf string) {
	change := map[string]any{"operation": "create", "name": "mp_notes", "fields": []map[string]any{{"name": "title", "type": "text", "required": true}}}
	var plan struct {
		BaseVersion int64  `json:"baseVersion"`
		Checksum    string `json:"checksum"`
	}
	multiProcessDecode(t, multiProcessRequest(t, base, http.MethodPost, "/api/admin/collections/plan", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK, &plan)
	change["baseVersion"], change["checksum"] = plan.BaseVersion, plan.Checksum
	multiProcessDecode(t, multiProcessRequest(t, base, http.MethodPost, "/api/admin/collections/apply", change, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusCreated, nil)
	multiProcessDecode(t, multiProcessRequest(t, base, http.MethodPut, "/api/admin/collections/mp_notes/user-access", map[string]any{"enabled": true}, "", cookie, map[string]string{"X-PgBase-CSRF": csrf}), http.StatusOK, nil)
}

func multiProcessRegister(t *testing.T, base string) publicUserSession {
	var user publicUserSession
	multiProcessDecode(t, multiProcessRequest(t, base, http.MethodPost, "/api/v1/auth/register", map[string]any{"email": "multiprocess@example.test", "password": publicUserOnePass}, "", nil, nil), http.StatusCreated, &user)
	return user
}

func multiProcessLogin(t *testing.T, base string) publicUserSession {
	var user publicUserSession
	multiProcessDecode(t, multiProcessRequest(t, base, http.MethodPost, "/api/v1/auth/login", map[string]any{"email": "multiprocess@example.test", "password": publicUserOnePass}, "", nil, nil), http.StatusOK, &user)
	return user
}

func multiProcessCreateRecord(t *testing.T, base, token, title, key string) string {
	var record struct {
		ID string `json:"id"`
	}
	multiProcessDecode(t, multiProcessRequest(t, base, http.MethodPost, "/api/v1/user/collections/mp_notes/records", map[string]any{"title": title}, token, nil, map[string]string{"Idempotency-Key": key}), http.StatusCreated, &record)
	return record.ID
}

type multiProcessStream struct {
	response *http.Response
	reader   *bufio.Reader
}
type multiProcessEvent struct{ id, event, data string }

func openMultiProcessStream(t *testing.T, base, token, cursor string) *multiProcessStream {
	t.Helper()
	request, _ := http.NewRequest(http.MethodGet, base+"/api/v1/user/collections/mp_notes/events", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if cursor != "" {
		request.Header.Set("Last-Event-ID", cursor)
	}
	response, err := (&http.Client{Timeout: 0}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		response.Body.Close()
		t.Fatalf("SSE status=%d", response.StatusCode)
	}
	return &multiProcessStream{response: response, reader: bufio.NewReader(response.Body)}
}

func (s *multiProcessStream) next(t *testing.T, kind string) multiProcessEvent {
	t.Helper()
	result := make(chan struct {
		event multiProcessEvent
		err   error
	}, 1)
	go func() {
		for {
			var event multiProcessEvent
			for {
				line, err := s.reader.ReadString('\n')
				if err != nil {
					result <- struct {
						event multiProcessEvent
						err   error
					}{err: err}
					return
				}
				line = strings.TrimSpace(line)
				if line == "" {
					break
				}
				if value, ok := strings.CutPrefix(line, "id: "); ok {
					event.id = value
				}
				if value, ok := strings.CutPrefix(line, "event: "); ok {
					event.event = value
				}
				if value, ok := strings.CutPrefix(line, "data: "); ok {
					event.data = value
				}
			}
			if event.event == kind {
				result <- struct {
					event multiProcessEvent
					err   error
				}{event: event}
				return
			}
		}
	}()
	select {
	case value := <-result:
		if value.err != nil {
			t.Fatalf("read %s SSE: %v", kind, value.err)
		}
		return value.event
	case <-time.After(8 * time.Second):
		t.Fatalf("no %s SSE event", kind)
		return multiProcessEvent{}
	}
}

func (s *multiProcessStream) nextRecordContaining(t *testing.T, recordID string) multiProcessEvent {
	t.Helper()
	for attempts := 0; attempts < 64; attempts++ {
		event := s.next(t, "record")
		if strings.Contains(event.data, recordID) {
			return event
		}
	}
	t.Fatalf("no realtime event for record %s within bounded backlog", recordID)
	return multiProcessEvent{}
}

func (s *multiProcessStream) waitClosed(t *testing.T) {
	t.Helper()
	result := make(chan error, 1)
	go func() { _, err := s.reader.ReadByte(); result <- err }()
	select {
	case <-result:
		_ = s.response.Body.Close()
	case <-time.After(5 * time.Second):
		_ = s.response.Body.Close()
		t.Fatal("SSE stream remained open")
	}
}
