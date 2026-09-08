package config

import (
	"fmt"
	"strconv"

	"github.com/webisnoteasy/PgBase/pgbase"
)

func loadEmail(lookup func(string) (string, bool), opts *pgbase.Options) error {
	for _, item := range []struct {
		name   string
		target *string
	}{
		{"ADDRESS", &opts.SMTP.Address}, {"FROM", &opts.SMTP.From}, {"USERNAME", &opts.SMTP.Username},
		{"PASSWORD", &opts.SMTP.Password}, {"SERVER_NAME", &opts.SMTP.ServerName},
	} {
		if raw, ok := value(lookup, "PGBASE_SMTP_"+item.name); ok {
			*item.target = raw
		}
	}
	for _, item := range []struct {
		name   string
		target *bool
	}{{"IMPLICIT_TLS", &opts.SMTP.ImplicitTLS}, {"ALLOW_INSECURE", &opts.SMTP.AllowInsecure}} {
		if raw, ok := value(lookup, "PGBASE_SMTP_"+item.name); ok && raw != "" {
			parsed, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("PGBASE_SMTP_%s: invalid boolean", item.name)
			}
			*item.target = parsed
		}
	}
	return parseDuration(lookup, "PGBASE_SMTP_TIMEOUT", &opts.SMTP.Timeout)
}
