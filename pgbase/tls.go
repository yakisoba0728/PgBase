package pgbase

import (
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// TLSOptions configures one opt-in built-in HTTPS listener. A static
// certificate pair and ACME are intentionally exclusive. ACME needs a
// concrete HTTP challenge address and an explicit host allowlist so an
// arbitrary Host header can never cause a certificate request.
type TLSOptions struct {
	Address              string
	CertificateFile      string
	KeyFile              string
	ACMEHosts            []string
	ACMECacheDirectory   string
	HTTPChallengeAddress string
	// ACMEDirectoryURL overrides the public CA directory for a private ACME CA.
	// Empty uses autocert's production directory. It is useful for isolated
	// staging and is intentionally not loaded from ambient environment settings.
	ACMEDirectoryURL string
}

// NormalizedForConfig validates a TLS configuration before app construction.
// It exists for environment adapters; callers receive an isolated normalized
// value from Options.Normalized.
func (o TLSOptions) NormalizedForConfig() (TLSOptions, error) { return o.normalized() }

func (o TLSOptions) normalized() (TLSOptions, error) {
	o.Address = strings.TrimSpace(o.Address)
	if o.Address == "" {
		return TLSOptions{}, fmt.Errorf("TLS.Address: is required")
	}
	if _, _, err := net.SplitHostPort(o.Address); err != nil {
		return TLSOptions{}, fmt.Errorf("TLS.Address: %w", err)
	}
	o.CertificateFile = strings.TrimSpace(o.CertificateFile)
	o.KeyFile = strings.TrimSpace(o.KeyFile)
	static := o.CertificateFile != "" || o.KeyFile != ""
	if static && (o.CertificateFile == "" || o.KeyFile == "") {
		return TLSOptions{}, fmt.Errorf("TLS: CertificateFile and KeyFile must be provided together")
	}
	if static && len(o.ACMEHosts) != 0 {
		return TLSOptions{}, fmt.Errorf("TLS: static certificate and ACME cannot be combined")
	}
	if !static && len(o.ACMEHosts) == 0 {
		return TLSOptions{}, fmt.Errorf("TLS: certificate pair or ACME hosts are required")
	}
	if static {
		for _, name := range []string{o.CertificateFile, o.KeyFile} {
			if !filepath.IsAbs(name) {
				return TLSOptions{}, fmt.Errorf("TLS: certificate paths must be absolute")
			}
			if info, err := os.Stat(name); err != nil || info.IsDir() {
				return TLSOptions{}, fmt.Errorf("TLS: certificate file is unavailable")
			}
		}
		return o, nil
	}
	if !filepath.IsAbs(o.ACMECacheDirectory) || strings.TrimSpace(o.ACMECacheDirectory) == "" {
		return TLSOptions{}, fmt.Errorf("TLS.ACMECacheDirectory: must be an absolute path")
	}
	if strings.TrimSpace(o.HTTPChallengeAddress) == "" {
		return TLSOptions{}, fmt.Errorf("TLS.HTTPChallengeAddress: is required for ACME")
	}
	if _, _, err := net.SplitHostPort(o.HTTPChallengeAddress); err != nil {
		return TLSOptions{}, fmt.Errorf("TLS.HTTPChallengeAddress: %w", err)
	}
	if o.ACMEDirectoryURL != "" {
		directory, err := url.Parse(o.ACMEDirectoryURL)
		if err != nil || directory.Host == "" || directory.User != nil || (directory.Scheme != "https" && directory.Scheme != "http") {
			return TLSOptions{}, fmt.Errorf("TLS.ACMEDirectoryURL: must be an HTTP(S) URL without credentials")
		}
		if directory.Scheme == "http" {
			host := directory.Hostname()
			address, addressErr := netip.ParseAddr(host)
			if addressErr != nil || !address.IsLoopback() {
				return TLSOptions{}, fmt.Errorf("TLS.ACMEDirectoryURL: HTTP is limited to a loopback test CA")
			}
		}
	}
	seen := map[string]bool{}
	hosts := make([]string, 0, len(o.ACMEHosts))
	for _, raw := range o.ACMEHosts {
		host := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(raw), "."))
		if !validACMEHostname(host) || seen[host] {
			return TLSOptions{}, fmt.Errorf("TLS.ACMEHosts: contains an invalid or duplicate host")
		}
		seen[host] = true
		hosts = append(hosts, host)
	}
	o.ACMEHosts = hosts
	return o, nil
}

func validACMEHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 || !strings.Contains(host, ".") {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
				return false
			}
		}
	}
	return true
}
