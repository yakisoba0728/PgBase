package pgbase

import (
	"context"
	"errors"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type retryCaptureSender struct{ sends int }

func (s *retryCaptureSender) Send(context.Context, mailer.Message) error {
	s.sends++
	if s.sends == 1 {
		return errors.New("temporary")
	}
	return nil
}

func TestBackupFailureAlertRetriesOnce(t *testing.T) {
	sender := &retryCaptureSender{}
	alerter := &backupFailureAlerter{sender: sender}
	if err := alerter.sendWithRetry(context.Background(), mailer.Message{}); err != nil {
		t.Fatal(err)
	}
	if sender.sends != 2 {
		t.Fatalf("SMTP attempts=%d", sender.sends)
	}
}
