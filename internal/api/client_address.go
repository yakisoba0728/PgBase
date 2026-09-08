package api

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

func clientAddress(r *http.Request, trustedProxies []netip.Prefix) (string, bool) {
	peer, ok := remoteAddress(r.RemoteAddr)
	if !ok {
		return "unknown", true
	}
	if !addressTrusted(peer, trustedProxies) {
		return rateLimitAddress(peer), true
	}
	values := r.Header.Values("X-Forwarded-For")
	if len(values) == 0 {
		return rateLimitAddress(peer), true
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
			return rateLimitAddress(forwarded[index]), true
		}
	}
	return rateLimitAddress(peer), true
}

func rateLimitAddress(address netip.Addr) string {
	address = address.Unmap()
	if address.Is6() {
		return netip.PrefixFrom(address, 64).Masked().String()
	}
	return address.String()
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
	return address.Unmap(), true
}

func addressTrusted(address netip.Addr, trustedProxies []netip.Prefix) bool {
	for _, prefix := range trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
}
