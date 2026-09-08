//go:build integration

package pgbase

import (
	"bufio"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"
)

func localMailSink(t *testing.T) (string, <-chan string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	messages := make(chan string, 10)
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
				reader := bufio.NewScanner(conn)
				fmt.Fprint(conn, "220 local mail sink\r\n")
				data := false
				var body strings.Builder
				for reader.Scan() {
					line := reader.Text()
					if data {
						if line == "." {
							messages <- body.String()
							data = false
							fmt.Fprint(conn, "250 accepted\r\n")
						} else {
							body.WriteString(line + "\n")
						}
						continue
					}
					switch {
					case strings.HasPrefix(line, "EHLO"), strings.HasPrefix(line, "HELO"):
						fmt.Fprint(conn, "250 localhost\r\n")
					case line == "DATA":
						data = true
						fmt.Fprint(conn, "354 send data\r\n")
					case line == "QUIT":
						fmt.Fprint(conn, "221 bye\r\n")
						return
					default:
						fmt.Fprint(conn, "250 ok\r\n")
					}
				}
			}()
		}
	}()
	return listener.Addr().String(), messages
}

func mailActionToken(t *testing.T, messages <-chan string, action string) string {
	t.Helper()
	select {
	case message := <-messages:
		for _, field := range strings.Fields(message) {
			if strings.HasPrefix(field, "https://") {
				parsed, err := url.Parse(field)
				if err != nil {
					t.Fatal(err)
				}
				values, err := url.ParseQuery(parsed.Fragment)
				if err != nil {
					t.Fatal(err)
				}
				if values.Get("action") != action || values.Get("token") == "" {
					t.Fatalf("wrong action link: %s", parsed.Path)
				}
				return values.Get("token")
			}
		}
		t.Fatal("email action URL missing")
	case <-time.After(5 * time.Second):
		t.Fatal("email not delivered")
	}
	return ""
}

func TestEmailActionsThroughHTTPAndLocalSMTP(t *testing.T) {
	address, messages := localMailSink(t)
	app := newPublicUsersAppOptions(t, true, func(options *Options) {
		options.SMTP = SMTPOptions{Address: address, From: "local@example.com", AllowInsecure: true}
	})
	session := registerPublicUser(t, app, "mail@example.com", publicUserOnePass)
	post := func(path string, body any, token string, want int) {
		t.Helper()
		assertPublicUsersStatus(t, publicUsersRequest(t, app, http.MethodPost, "/api/v1/auth/"+path, body, token, nil, nil), want)
	}
	post("request-verification", map[string]any{}, session.Token, 204)
	verification := mailActionToken(t, messages, "verify")
	post("confirm-verification", map[string]any{"token": verification}, "", 204)
	post("confirm-verification", map[string]any{"token": verification}, "", 401)
	response := publicUsersRequest(t, app, "GET", "/api/v1/auth/session", nil, session.Token, nil, nil)
	assertPublicUsersStatus(t, response, 200)
	if !strings.Contains(response.Body.String(), `"verified":true`) {
		t.Fatal("verification missing from session")
	}
	post("request-password-reset", map[string]any{"email": "unknown@example.com"}, "", 204)
	post("request-password-reset", map[string]any{"email": "mail@example.com"}, "", 204)
	reset := mailActionToken(t, messages, "reset")
	newPassword := "new-password-for-local-mail-7281"
	post("confirm-password-reset", map[string]any{"token": reset, "newPassword": newPassword}, "", 204)
	assertUserManagementSessionStatus(t, app, session.Token, 401)
	session = userManagementLogin(t, app, "mail@example.com", newPassword)
	post("request-email-change", map[string]any{"currentPassword": newPassword, "newEmail": "changed@example.com"}, session.Token, 204)
	change := mailActionToken(t, messages, "email-change")
	post("confirm-email-change", map[string]any{"token": change}, "", 204)
	assertUserManagementSessionStatus(t, app, session.Token, 401)
	session = userManagementLogin(t, app, "changed@example.com", newPassword)
	assertUserManagementSessionStatus(t, app, session.Token, 200)
	page := publicUsersRequest(t, app, "GET", "/auth/action", nil, "", nil, nil)
	assertPublicUsersStatus(t, page, 200)
	if page.Header().Get("Referrer-Policy") != "no-referrer" || !strings.Contains(page.Body.String(), "location.hash") {
		t.Fatal("confirmation page token boundary missing")
	}
}
