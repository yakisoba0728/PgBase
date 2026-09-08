package mailer

import (
	"bufio"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

type SMTPConfig struct {
	Address       string
	From          string
	Username      string
	Password      string
	ServerName    string
	ImplicitTLS   bool
	AllowInsecure bool
	Timeout       time.Duration
}

type SMTP struct{ config SMTPConfig }

func NewSMTP(config SMTPConfig) (*SMTP, error) {
	if config.Address == "" || config.From == "" || strings.ContainsAny(config.From, "\r\n") {
		return nil, errors.New("invalid SMTP configuration")
	}
	if config.Timeout <= 0 {
		config.Timeout = 10 * time.Second
	}
	host, _, err := net.SplitHostPort(config.Address)
	if err != nil {
		return nil, errors.New("invalid SMTP address")
	}
	if config.AllowInsecure && !localSMTPHost(host) {
		return nil, errors.New("insecure SMTP is allowed only for a loopback server")
	}
	if config.ServerName == "" {
		config.ServerName = host
	}
	return &SMTP{config: config}, nil
}

func localSMTPHost(host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	address := net.ParseIP(host)
	return address != nil && address.IsLoopback()
}

func (s *SMTP) Send(ctx context.Context, message Message) error {
	if s == nil || message.To == "" || strings.ContainsAny(message.To+message.Subject, "\r\n") || len(message.Text) > 64<<10 {
		return errors.New("invalid email message")
	}
	deadline := time.Now().Add(s.config.Timeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	dialer := net.Dialer{Deadline: deadline}
	connection, err := dialer.DialContext(ctx, "tcp", s.config.Address)
	if err != nil {
		return fmt.Errorf("connect SMTP server: %w", err)
	}
	defer func() { _ = connection.Close() }()
	if err := connection.SetDeadline(deadline); err != nil {
		return fmt.Errorf("set SMTP deadline: %w", err)
	}
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: s.config.ServerName}
	if s.config.AllowInsecure {
		tlsConfig.InsecureSkipVerify = true // Explicit local-test/development policy.
	}
	if s.config.ImplicitTLS {
		connection = tls.Client(connection, tlsConfig)
	}
	client, err := smtp.NewClient(connection, s.config.ServerName)
	if err != nil {
		return fmt.Errorf("start SMTP client: %w", err)
	}
	defer func() { _ = client.Close() }()
	if !s.config.ImplicitTLS {
		if ok, _ := client.Extension("STARTTLS"); ok {
			if err := client.StartTLS(tlsConfig); err != nil {
				return fmt.Errorf("start SMTP TLS: %w", err)
			}
		} else if !s.config.AllowInsecure {
			return errors.New("SMTP server does not offer STARTTLS")
		}
	}
	if s.config.Username != "" {
		auth := smtp.PlainAuth("", s.config.Username, s.config.Password, s.config.ServerName)
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("authenticate SMTP client: %w", err)
		}
	}
	if err := client.Mail(s.config.From); err != nil {
		return fmt.Errorf("set SMTP sender: %w", err)
	}
	if err := client.Rcpt(message.To); err != nil {
		return fmt.Errorf("set SMTP recipient: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("start SMTP message: %w", err)
	}
	body := "From: " + s.config.From + "\r\nTo: " + message.To + "\r\nSubject: " + message.Subject + "\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" + message.Text
	writer := bufio.NewWriter(w)
	if _, err := writer.WriteString(body); err != nil {
		_ = w.Close()
		return fmt.Errorf("write SMTP message: %w", err)
	}
	if err := writer.Flush(); err != nil {
		_ = w.Close()
		return fmt.Errorf("flush SMTP message: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("finish SMTP message: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("quit SMTP client: %w", err)
	}
	return nil
}
