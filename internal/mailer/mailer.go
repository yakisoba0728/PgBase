// Package mailer sends bounded transactional email without retaining secrets.
package mailer

import "context"

type Message struct {
	To      string
	Subject string
	Text    string
}

type Sender interface {
	Send(context.Context, Message) error
}
