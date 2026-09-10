package pgbase

import (
	"fmt"
	"time"

	"github.com/yakisoba0728/PgBase/internal/mailer"
)

// SMTPOptions enables transactional account email when Address is set.
// AllowInsecure is limited to a loopback SMTP development server.
type SMTPOptions struct {
	Address       string
	From          string
	Username      string
	Password      string
	ServerName    string
	ImplicitTLS   bool
	AllowInsecure bool
	Timeout       time.Duration
}

func (o SMTPOptions) config() mailer.SMTPConfig {
	return mailer.SMTPConfig{Address: o.Address, From: o.From, Username: o.Username, Password: o.Password, ServerName: o.ServerName, ImplicitTLS: o.ImplicitTLS, AllowInsecure: o.AllowInsecure, Timeout: o.Timeout}
}

func (o Options) validateEmail() error {
	if o.SMTP.Address == "" {
		if o.SMTP.From != "" || o.SMTP.Username != "" || o.SMTP.Password != "" || o.SMTP.ServerName != "" || o.SMTP.ImplicitTLS || o.SMTP.AllowInsecure || o.SMTP.Timeout != 0 {
			return fmt.Errorf("SMTP.Address: required when SMTP options are set")
		}
		return nil
	}
	if o.PublicURL == nil {
		return fmt.Errorf("PublicURL: required for account email")
	}
	if o.SMTP.Timeout < 0 || o.SMTP.Timeout > time.Minute {
		return fmt.Errorf("SMTP.Timeout: must be between zero and one minute")
	}
	if _, err := mailer.NewSMTP(o.SMTP.config()); err != nil {
		return fmt.Errorf("SMTP: %w", err)
	}
	return nil
}
