package config

import (
	"fmt"
	"strings"

	"github.com/yakisoba0728/PgBase/pgbase"
)

func loadTLS(lookup func(string) (string, bool), options *pgbase.Options) error {
	address, enabled := value(lookup, "PGBASE_HTTPS_ADDRESS")
	certificate, certificateSet := value(lookup, "PGBASE_HTTPS_CERT_FILE")
	key, keySet := value(lookup, "PGBASE_HTTPS_KEY_FILE")
	acmeHosts, acmeSet := value(lookup, "PGBASE_ACME_HOSTS")
	cache, cacheSet := value(lookup, "PGBASE_ACME_CACHE_DIR")
	challenge, challengeSet := value(lookup, "PGBASE_ACME_HTTP_ADDRESS")
	if !enabled && !certificateSet && !keySet && !acmeSet && !cacheSet && !challengeSet {
		return nil
	}
	tls := &pgbase.TLSOptions{Address: address, CertificateFile: certificate, KeyFile: key, ACMECacheDirectory: cache, HTTPChallengeAddress: challenge}
	if acmeHosts != "" {
		tls.ACMEHosts = strings.Split(acmeHosts, ",")
	}
	if _, err := tls.NormalizedForConfig(); err != nil {
		return fmt.Errorf("HTTPS: %w", err)
	}
	options.TLS = tls
	return nil
}
