package mailer

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

func TestSMTPRequiresTLSUnlessExplicitlyAllowed(t *testing.T) {
	address, messages := startSMTPFixture(t)
	sender, err := NewSMTP(SMTPConfig{Address: address, From: "sender@example.test", ServerName: "localhost", Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), Message{To: "user@example.test", Subject: "subject", Text: "body"}); err == nil || !strings.Contains(err.Error(), "STARTTLS") {
		t.Fatalf("Send without TLS = %v", err)
	}
	select {
	case <-messages:
		t.Fatal("server received message despite TLS refusal")
	default:
	}
}

func TestSMTPRejectsInsecureRemoteServer(t *testing.T) {
	if _, err := NewSMTP(SMTPConfig{Address: "smtp.example.com:25", From: "sender@example.test", AllowInsecure: true}); err == nil {
		t.Fatal("NewSMTP accepted insecure remote server")
	}
}

func TestSMTPDeliversBoundedMessageToLocalInsecureFixture(t *testing.T) {
	address, messages := startSMTPFixture(t)
	sender, err := NewSMTP(SMTPConfig{Address: address, From: "sender@example.test", ServerName: "localhost", AllowInsecure: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), Message{To: "user@example.test", Subject: "Verify", Text: "http://localhost/token"}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		for _, expected := range []string{"From: sender@example.test", "To: user@example.test", "Subject: Verify", "http://localhost/token"} {
			if !strings.Contains(message, expected) {
				t.Fatalf("message missing %q: %q", expected, message)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP fixture received no message")
	}
}

func TestSMTPDeliversMultipartAlternative(t *testing.T) {
	address, messages := startSMTPFixture(t)
	sender, err := NewSMTP(SMTPConfig{Address: address, From: "sender@example.test", ServerName: "localhost", AllowInsecure: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if err := sender.Send(context.Background(), Message{To: "user@example.test", Subject: "Verify", Text: "Open https://example.test", HTML: "<p>Open <a href=\"https://example.test\">this link</a>.</p>"}); err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		for _, expected := range []string{"multipart/alternative", "Content-Type: text/plain", "Content-Type: text/html", "<a href=\"https://example.test\">"} {
			if !strings.Contains(message, expected) {
				t.Fatalf("message missing %q: %q", expected, message)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP fixture received no message")
	}
}

func TestSMTPDeliversAttachment(t *testing.T) {
	address, messages := startSMTPFixture(t)
	sender, err := NewSMTP(SMTPConfig{Address: address, From: "sender@example.test", ServerName: "localhost", AllowInsecure: true, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	message := Message{To: "user@example.test", Subject: "Report", Text: "attached", Attachments: []Attachment{{Filename: "report.txt", ContentType: "text/plain", Data: []byte("hello")}}}
	if err := sender.Send(context.Background(), message); err != nil {
		t.Fatal(err)
	}
	select {
	case delivered := <-messages:
		for _, expected := range []string{"multipart/mixed", `filename=report.txt`, "aGVsbG8="} {
			if !strings.Contains(delivered, expected) {
				t.Fatalf("message missing %q: %q", expected, delivered)
			}
		}
	case <-time.After(time.Second):
		t.Fatal("SMTP fixture received no message")
	}
}

func startSMTPFixture(t *testing.T) (string, <-chan string) {
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
			go serveSMTPFixture(connection, messages)
		}
	}()
	return listener.Addr().String(), messages
}

func serveSMTPFixture(connection net.Conn, messages chan<- string) {
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
