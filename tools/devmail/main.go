// devmail is a loopback-only SMTP sink and inbox for local development.
package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

func main() {
	smtpAddress := flag.String("smtp", "127.0.0.1:1025", "loopback SMTP address")
	httpAddress := flag.String("http", "127.0.0.1:8025", "loopback inbox address")
	directory := flag.String("dir", ".local/pgbase/mail", "private inbox directory")
	flag.Parse()
	for _, address := range []string{*smtpAddress, *httpAddress} {
		host, _, err := net.SplitHostPort(address)
		if err != nil || !net.ParseIP(host).IsLoopback() {
			log.Fatal("devmail addresses must be loopback IP addresses")
		}
	}
	if err := os.MkdirAll(*directory, 0700); err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", *smtpAddress)
	if err != nil {
		log.Fatal(err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	inbox := &mailInbox{directory: *directory}
	server := &http.Server{Addr: *httpAddress, Handler: inbox, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		_ = listener.Close()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Print("devmail inbox stopped")
			stop()
		}
	}()
	slots := make(chan struct{}, 16)
	for {
		connection, err := listener.Accept()
		if err != nil {
			break
		}
		select {
		case slots <- struct{}{}:
			go func() { defer func() { <-slots }(); inbox.receive(connection) }()
		default:
			_ = connection.Close()
		}
	}
}

type mailInbox struct {
	directory string
	mu        sync.Mutex
}

func (inbox *mailInbox) receive(connection net.Conn) {
	defer func() { _ = connection.Close() }()
	_ = connection.SetDeadline(time.Now().Add(30 * time.Second))
	scanner := bufio.NewScanner(connection)
	scanner.Buffer(make([]byte, 4096), 16384)
	_, _ = fmt.Fprint(connection, "220 PgBase local inbox\r\n")
	var data strings.Builder
	receiving := false
	hasSender, hasRecipient := false, false
	for scanner.Scan() {
		line := scanner.Text()
		if receiving {
			if line == "." {
				if err := inbox.save(data.String()); err != nil {
					_, _ = fmt.Fprint(connection, "451 Unable to save message\r\n")
				} else {
					_, _ = fmt.Fprint(connection, "250 Message saved locally\r\n")
				}
				data.Reset()
				receiving = false
				hasSender, hasRecipient = false, false
				continue
			}
			if data.Len()+len(line) > 65536 {
				_, _ = fmt.Fprint(connection, "552 Message too large\r\n")
				return
			}
			if strings.HasPrefix(line, "..") {
				line = line[1:]
			}
			data.WriteString(line + "\n")
			continue
		}
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO "):
			_, _ = fmt.Fprint(connection, "250-localhost\r\n250 SIZE 65536\r\n")
		case strings.HasPrefix(upper, "HELO "):
			_, _ = fmt.Fprint(connection, "250 localhost\r\n")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			hasSender = true
			_, _ = fmt.Fprint(connection, "250 OK\r\n")
		case strings.HasPrefix(upper, "RCPT TO:") && hasSender:
			hasRecipient = true
			_, _ = fmt.Fprint(connection, "250 OK\r\n")
		case upper == "DATA" && hasRecipient:
			receiving = true
			_, _ = fmt.Fprint(connection, "354 End with a dot\r\n")
		case upper == "RSET":
			hasSender, hasRecipient = false, false
			data.Reset()
			_, _ = fmt.Fprint(connection, "250 OK\r\n")
		case upper == "NOOP":
			_, _ = fmt.Fprint(connection, "250 OK\r\n")
		case upper == "QUIT":
			_, _ = fmt.Fprint(connection, "221 Bye\r\n")
			return
		default:
			_, _ = fmt.Fprint(connection, "502 Command unavailable\r\n")
		}
	}
}

func (inbox *mailInbox) save(message string) error {
	inbox.mu.Lock()
	defer inbox.mu.Unlock()
	entropy := make([]byte, 8)
	if _, err := rand.Read(entropy); err != nil {
		return err
	}
	name := fmt.Sprintf("%019d-%s.txt", time.Now().UnixNano(), hex.EncodeToString(entropy))
	if err := os.WriteFile(filepath.Join(inbox.directory, name), []byte(message), 0600); err != nil {
		return err
	}
	entries, err := os.ReadDir(inbox.directory)
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && entry.Type()&os.ModeSymlink == 0 && strings.HasSuffix(entry.Name(), ".txt") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for len(names) > 100 {
		if err := os.Remove(filepath.Join(inbox.directory, names[0])); err != nil {
			return err
		}
		names = names[1:]
	}
	return nil
}

func (inbox *mailInbox) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if r.Method != "GET" {
		http.Error(w, "Method not allowed", 405)
		return
	}
	if r.URL.Path == "/messages" {
		inbox.mu.Lock()
		defer inbox.mu.Unlock()
		entries, err := os.ReadDir(inbox.directory)
		if err != nil {
			http.Error(w, "Inbox unavailable", 503)
			return
		}
		messages := make([]string, 0, len(entries))
		for index := len(entries) - 1; index >= 0; index-- {
			entry := entries[index]
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 || !strings.HasSuffix(entry.Name(), ".txt") {
				continue
			}
			body, err := os.ReadFile(filepath.Join(inbox.directory, entry.Name()))
			if err == nil {
				messages = append(messages, string(body))
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(messages)
		return
	}
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'")
	_, _ = fmt.Fprint(w, `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>PgBase local inbox</title><style>body{font:16px system-ui;max-width:64rem;margin:3rem auto;padding:1rem}article{border:1px solid #ccc;border-radius:.6rem;padding:1rem;margin:1rem 0}pre{white-space:pre-wrap;overflow-wrap:anywhere}button{font:inherit;padding:.5rem}</style><h1>PgBase local inbox</h1><p>Messages stay on this computer. The latest 100 messages are retained.</p><button>Refresh</button><main></main><script>async function refresh(){const response=await fetch('/messages');if(!response.ok)return;const messages=await response.json();const main=document.querySelector('main');main.replaceChildren();for(const message of messages){const article=document.createElement('article');const pre=document.createElement('pre');pre.textContent=message;article.append(pre);for(const match of message.matchAll(/https?:\/\/[^\s]+/g)){const a=document.createElement('a');a.href=match[0];a.textContent='Open confirmation';a.rel='noreferrer';article.append(a)}main.append(article)}}document.querySelector('button').onclick=refresh;refresh();</script></html>`)
}
