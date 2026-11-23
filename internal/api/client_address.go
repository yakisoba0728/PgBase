package api

import (
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync/atomic"

	"github.com/yakisoba0728/PgBase/internal/apiratelimit"
)

func clientAddress(r *http.Request, trustedProxies []netip.Prefix) (string, bool) {
	address, ok := clientIP(r, trustedProxies)
	if !ok {
		return "", false
	}
	parsed, err := netip.ParseAddr(address)
	if err != nil {
		return address, true
	}
	return apiratelimit.NetworkBucket(parsed), true
}

// clientIP resolves the true peer through explicitly trusted proxies without
// applying the IPv6 /64 aggregation used only by rate limiting.
func clientIP(r *http.Request, trustedProxies []netip.Prefix) (string, bool) {
	peer, ok := remoteAddress(r.RemoteAddr)
	if !ok {
		return "unknown", true
	}
	if !addressTrusted(peer, trustedProxies) {
		return peer.String(), true
	}
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return peer.String(), true
	}
	parts := strings.Split(strings.Join(values, ","), ",")
	forwarded := make([]netip.Addr, 0, len(parts))
	for _, part := range parts {
		address, err := netip.ParseAddr(strings.TrimSpace(part))
		if err != nil || address.Zone() != "" {
			return "", false
		}
		forwarded = append(forwarded, address.Unmap())
	}
	if len(forwarded) == 0 {
		return "", false
	}
	for index := len(forwarded) - 1; index >= 0; index-- {
		if !addressTrusted(forwarded[index], trustedProxies) {
			return forwarded[index].String(), true
		}
	}
	return peer.String(), true
}

func remoteAddress(value string) (netip.Addr, bool) {
	host, _, err := net.SplitHostPort(value)
	if err != nil {
		host = strings.Trim(value, "[]")
	}
	address, err := netip.ParseAddr(host)
	if err != nil {
		return netip.Addr{}, false
	}
	// Zones (fe80::1%en0) are interface-local and not valid inet values.
	return address.WithZone("").Unmap(), true
}

// ignoredForwarding names the misconfiguration a request with proxy
// forwarding headers reveals, if any. Only X-Forwarded-For from a trusted peer
// is read, so a reverse proxy that is not trusted, or that sends only
// Forwarded or X-Real-IP, makes every client behind it look like the proxy:
// they then share one network rate-limit bucket and one anonymous realtime
// bucket.
func ignoredForwarding(r *http.Request, trustedProxies []netip.Prefix) (reason string, peer netip.Addr) {
	forwardedFor := len(r.Header.Values("X-Forwarded-For")) > 0
	if !forwardedFor && r.Header.Get("Forwarded") == "" && r.Header.Get("X-Real-Ip") == "" {
		return "", netip.Addr{}
	}
	peer, ok := remoteAddress(r.RemoteAddr)
	switch {
	case !ok:
		return "", netip.Addr{}
	case !addressTrusted(peer, trustedProxies):
		return "untrusted_peer", peer
	case !forwardedFor:
		return "unread_header", peer
	default:
		return "", netip.Addr{}
	}
}

// forwardingWarningMiddleware warns once per misconfiguration and process
// when a request carries forwarding headers that pgBase ignores. After both
// warnings it costs one atomic load per request. A client that sends these
// headers itself to a server with no proxy in front triggers the same warning,
// which the message says.
func forwardingWarningMiddleware(logger *slog.Logger, trustedProxies func() []netip.Prefix, next http.Handler) http.Handler {
	var untrustedPeer, unreadHeader atomic.Bool
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !untrustedPeer.Load() || !unreadHeader.Load() {
			switch reason, peer := ignoredForwarding(r, currentTrustedProxies(trustedProxies)); reason {
			case "untrusted_peer":
				if untrustedPeer.CompareAndSwap(false, true) {
					logger.WarnContext(r.Context(), "request from an untrusted peer carries proxy forwarding headers; "+
						"pgBase uses the peer address, so all clients behind that proxy share one rate-limit bucket and one anonymous realtime bucket. "+
						"Add the proxy to the trusted proxies (PGBASE_TRUSTED_PROXIES or the proxy runtime setting), "+
						"or ignore this if pgBase has no proxy in front and a client sent the headers itself",
						"code", "forwarding_headers_untrusted_peer", "peer", forwardingPeer(peer))
				}
			case "unread_header":
				if unreadHeader.CompareAndSwap(false, true) {
					logger.WarnContext(r.Context(), "trusted proxy sends Forwarded or X-Real-IP without X-Forwarded-For; "+
						"pgBase reads only X-Forwarded-For, so all clients behind that proxy share one rate-limit bucket and one anonymous realtime bucket. "+
						"Configure the proxy to send X-Forwarded-For",
						"code", "forwarding_header_unread", "peer", forwardingPeer(peer))
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// forwardingPeer is what the warning logs about the peer. A private, loopback
// or link-local peer is a proxy candidate and is shown exactly so its address
// can be trusted; a public peer may be an end user, so only its network is
// shown.
func forwardingPeer(peer netip.Addr) string {
	if peer.IsPrivate() || peer.IsLoopback() || peer.IsLinkLocalUnicast() {
		return peer.String()
	}
	bits := 24
	if peer.Is6() {
		bits = 64
	}
	prefix, err := peer.Prefix(bits)
	if err != nil {
		return "unknown"
	}
	return prefix.String()
}

func addressTrusted(address netip.Addr, trustedProxies []netip.Prefix) bool {
	for _, prefix := range trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
