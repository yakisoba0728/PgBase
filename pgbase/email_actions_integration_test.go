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

func mailActionToken(t *testing.T, messages <-chan string, action string, contains ...string) string {
	t.Helper()
	select {
	case message := <-messages:
		for _, fragment := range contains {
			if !strings.Contains(message, fragment) {
				t.Fatalf("mail for %s missing %q: %s", action, fragment, message)
			}
		}
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
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
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
	template := publicUsersRequest(t, app, http.MethodPut,
		"/api/admin/auth-collections/00000000-0000-0000-0000-000000000001/mail-templates/email_change",
		map[string]any{"subject": "Custom email change", "body": "Open custom link: {{action_url}}"}, "", adminCookie,
		map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, template, http.StatusOK)
	post("request-email-change", map[string]any{"currentPassword": newPassword, "newEmail": "changed@example.com"}, session.Token, 204)
	change := mailActionToken(t, messages, "email-change", "Subject: Custom email change", "Open custom link:")
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

func TestAuthAdministrationSMTPRedactsPasswordsAndSendsOnlyToLocalSink(t *testing.T) {
	address, messages := localMailSink(t)
	app := newPublicUsersAppOptions(t, true, nil)
	adminCookie, csrf := setupPublicUsersAdministrator(t, app)
	path := "/api/admin/auth-collections/00000000-0000-0000-0000-000000000001/smtp"
	config := map[string]any{
		"address": address, "from": "mailer@example.test", "username": "mailer",
		"password": "test-only-smtp-password", "serverName": "localhost",
		"implicitTls": false, "allowInsecureLoopback": true,
	}
	saved := publicUsersRequest(t, app, http.MethodPut, path, config, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, saved, http.StatusOK)
	if strings.Contains(saved.Body.String(), "test-only-smtp-password") || !strings.Contains(saved.Body.String(), `"passwordConfigured":true`) {
		t.Fatalf("SMTP save response leaked or omitted password state: %s", saved.Body.String())
	}

	config["password"] = ""
	config["from"] = "changed@example.test"
	updated := publicUsersRequest(t, app, http.MethodPut, path, config, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, updated, http.StatusOK)
	if strings.Contains(updated.Body.String(), "test-only-smtp-password") || !strings.Contains(updated.Body.String(), `"passwordConfigured":true`) {
		t.Fatalf("SMTP update response leaked or lost password state: %s", updated.Body.String())
	}

	settings := publicUsersRequest(t, app, http.MethodGet, "/api/admin/auth-collections/00000000-0000-0000-0000-000000000001/administration", nil, "", adminCookie, nil)
	assertPublicUsersStatus(t, settings, http.StatusOK)
	if strings.Contains(settings.Body.String(), "test-only-smtp-password") || !strings.Contains(settings.Body.String(), `"passwordConfigured":true`) {
		t.Fatalf("SMTP settings response leaked or omitted password state: %s", settings.Body.String())
	}

	test := publicUsersRequest(t, app, http.MethodPost, path+"/test", map[string]any{"recipient": "sink@example.test"}, "", adminCookie, map[string]string{"X-PgBase-CSRF": csrf})
	assertPublicUsersStatus(t, test, http.StatusNoContent)
	select {
	case message := <-messages:
		if !strings.Contains(message, "To: sink@example.test") || !strings.Contains(message, "Subject: PgBase SMTP test") {
			t.Fatalf("unexpected local SMTP test message: %s", message)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("local SMTP sink did not receive SMTP test message")
	}
}
