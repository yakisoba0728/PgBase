package pgbase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type extensionMailRequest struct {
	To          string                    `json:"to"`
	Subject     string                    `json:"subject"`
	Text        string                    `json:"text"`
	HTML        string                    `json:"html,omitempty"`
	Attachments []extensionMailAttachment `json:"attachments,omitempty"`
}

// extensionMailPatch keeps omission distinct from a request to clear a field.
// Mail hooks mutate an existing delivery message and retain its other delivery
// fields, purpose, metadata, and attachments.
type extensionMailPatch struct {
	To      *string `json:"to"`
	Subject *string `json:"subject"`
	Text    *string `json:"text"`
	HTML    *string `json:"html"`
}

func (patch extensionMailPatch) apply(message *mailer.Message) {
	if patch.To != nil {
		message.To = *patch.To
	}
	if patch.Subject != nil {
		message.Subject = *patch.Subject
	}
	if patch.Text != nil {
		message.Text = *patch.Text
	}
	if patch.HTML != nil {
		message.HTML = *patch.HTML
	}
}

type extensionMailAttachment struct {
	Filename    string `json:"filename"`
	ContentType string `json:"contentType,omitempty"`
	DataBase64  string `json:"dataBase64"`
}

func (host *extensionHost) callMail(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	if host.bridge.mailer == nil {
		return nil, extensionError("mail_unavailable", errors.New("mail transport is unavailable"))
	}
	var request extensionMailRequest
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, extensionError("invalid_mail_request", err)
	}
	attachments := make([]mailer.Attachment, len(request.Attachments))
	for index, attachment := range request.Attachments {
		data, err := base64.StdEncoding.DecodeString(attachment.DataBase64)
		if err != nil {
			return nil, extensionError("invalid_mail_request", err)
		}
		attachments[index] = mailer.Attachment{Filename: attachment.Filename, ContentType: attachment.ContentType, Data: data}
	}
	if err := host.bridge.mailer.Send(ctx, mailer.Message{To: request.To, Subject: request.Subject, Text: request.Text, HTML: request.HTML, Attachments: attachments}); err != nil {
		return nil, mapExtensionError(err)
	}
	return json.RawMessage(`{"sent":true}`), nil
}
