// Package mailer sends bounded transactional email without retaining secrets.
package mailer

import "context"

type Message struct {
	To      string `json:"-"`
	Subject string `json:"subject"`
	Text    string `json:"text"`
	HTML    string `json:"html"`
}

type Sender interface {
	Send(context.Context, Message) error
}
