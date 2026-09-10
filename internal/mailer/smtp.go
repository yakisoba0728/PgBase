package mailer

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"mime/multipart"
	"net"
	"net/smtp"
	"net/textproto"
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
	if s == nil || message.To == "" || strings.ContainsAny(message.To+message.Subject, "\r\n") || len(message.Text)+len(message.HTML) > 64<<10 || message.Text == "" {
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
	body, err := encodeMessage(s.config.From, message)
	if err != nil {
		_ = w.Close()
		return err
	}
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

func encodeMessage(from string, message Message) (string, error) {
	headers := "From: " + from + "\r\nTo: " + message.To + "\r\nSubject: " + message.Subject + "\r\nMIME-Version: 1.0\r\n"
	if message.HTML == "" && len(message.Attachments) == 0 {
		return headers + "Content-Type: text/plain; charset=UTF-8\r\nContent-Transfer-Encoding: 8bit\r\n\r\n" + message.Text, nil
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	subtype := "multipart/mixed"
	if len(message.Attachments) == 0 {
		subtype = "multipart/alternative"
	}
	if err := writeMailPart(writer, "text/plain; charset=UTF-8", "", []byte(message.Text), false); err != nil {
		return "", err
	}
	if message.HTML != "" {
		if err := writeMailPart(writer, "text/html; charset=UTF-8", "", []byte(message.HTML), false); err != nil {
			return "", err
		}
	}
	total := 0
	for _, attachment := range message.Attachments {
		if attachment.Filename == "" || strings.ContainsAny(attachment.Filename, "\r\n/\\") || strings.ContainsAny(attachment.ContentType, "\r\n") {
			return "", errors.New("invalid email attachment")
		}
		total += len(attachment.Data)
		if total > 8<<20 {
			return "", errors.New("email attachments exceed 8 MiB")
		}
		contentType := attachment.ContentType
		if contentType == "" {
			contentType = "application/octet-stream"
		}
		if err := writeMailPart(writer, contentType, attachment.Filename, attachment.Data, true); err != nil {
			return "", err
		}
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return headers + "Content-Type: " + subtype + "; boundary=\"" + writer.Boundary() + "\"\r\n\r\n" + body.String(), nil
}

func writeMailPart(writer *multipart.Writer, contentType, filename string, data []byte, encoded bool) error {
	header := textproto.MIMEHeader{}
	header.Set("Content-Type", contentType)
	if !encoded {
		header.Set("Content-Transfer-Encoding", "8bit")
	}
	if encoded {
		header.Set("Content-Transfer-Encoding", "base64")
		header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": filename}))
	}
	part, err := writer.CreatePart(header)
	if err != nil {
		return err
	}
	if !encoded {
		_, err = part.Write(data)
		return err
	}
	encoder := base64.NewEncoder(base64.StdEncoding, part)
	if _, err = encoder.Write(data); err != nil {
		return err
	}
	return encoder.Close()
}
