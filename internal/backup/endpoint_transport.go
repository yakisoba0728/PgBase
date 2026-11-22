package backup

import (
	"net/netip"
	"net/url"
	"strings"
)

// PlaintextEndpoint reports whether a backup S3 endpoint sends credentials and
// archives over unencrypted HTTP to a host that is not provably local.
//
// Plain http stays accepted so existing private-network deployments (for
// example the bundled Compose file talking to MinIO) keep working; this only
// decides when to warn. Only loopback, private and link-local IP literals and
// the name "localhost" are treated as local. Every other hostname, including
// single-label service names, is reported, because a name alone does not prove
// where the traffic goes.
func PlaintextEndpoint(endpoint string) bool {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || !strings.EqualFold(parsed.Scheme, "http") {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
	if host == "" {
		return false
	}
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return false
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return true
	}
	address = address.Unmap()
	return !address.IsLoopback() && !address.IsPrivate() && !address.IsLinkLocalUnicast()
}
