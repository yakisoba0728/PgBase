// Package mailer sends bounded transactional email without retaining secrets.
package mailer

import "context"

type Message struct {
	// Purpose is set by trusted application services and is never serialized.
	// It selects a purpose-specific hook but does not contain credentials.
	Purpose     string       `json:"-"`
	To          string       `json:"-"`
	Subject     string       `json:"subject"`
	Text        string       `json:"text"`
	HTML        string       `json:"html"`
	Attachments []Attachment `json:"-"`
	// Meta is invocation-scoped hook data. It is never serialized by a mail
	// transport; callers must allocate a fresh map for every message.
	Meta map[string]any `json:"-"`
}

type Attachment struct {
	Filename    string
	ContentType string
	Data        []byte
}

type Sender interface {
	Send(context.Context, Message) error
}
