// Package mailer sends bounded transactional email without retaining secrets.
package mailer

import "context"

type Message struct {
	// Purpose is set by trusted application services and is never serialized.
	// It selects a purpose-specific hook but does not contain credentials.
	Purpose string `json:"-"`
	To      string `json:"-"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

type Sender interface {
	Send(context.Context, Message) error
}
