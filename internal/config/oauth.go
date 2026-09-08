package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/webisnoteasy/PgBase/pgbase"
)

func loadOAuth(lookup func(string) (string, bool), opts *pgbase.Options) error {
	for _, item := range []struct {
		name   string
		target any
	}{{"PGBASE_OAUTH_PROVIDERS", &opts.OAuthProviders}, {"PGBASE_OAUTH_REDIRECTS", &opts.OAuthRedirects}} {
		raw, ok := value(lookup, item.name)
		if !ok || raw == "" {
			continue
		}
		if len(raw) > 64<<10 {
			return fmt.Errorf("%s: configuration too large", item.name)
		}
		decoder := json.NewDecoder(strings.NewReader(raw))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(item.target); err != nil {
			return fmt.Errorf("%s: invalid JSON configuration", item.name)
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			return fmt.Errorf("%s: invalid JSON configuration", item.name)
		}
	}
	return nil
}
