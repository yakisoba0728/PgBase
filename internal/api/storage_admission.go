package api

import (
	"net/http"
	"net/netip"

	"github.com/yakisoba0728/PgBase/internal/filestore"
)

// storageAdmissionMiddleware names the caller that the storage operations a
// request starts (uploads, staged batch files, variant renders) are charged
// to, so one caller cannot hold every shared storage slot. It runs inside the
// route's access middleware, where the principal is already admitted.
// Anonymous callers are keyed by client network (an IPv4 address or an IPv6
// /64), as anonymous realtime admission is; without a trusted proxy every
// anonymous caller behind it shares one key.
func storageAdmissionMiddleware(trusted func() []netip.Prefix, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		subject := apiRateLimitActor(r.Context())
		if subject == "" {
			address, ok := clientAddress(r, currentTrustedProxies(trusted))
			if !ok {
				address = "unknown"
			}
			subject = "anonymous:" + address
		}
		next.ServeHTTP(w, r.WithContext(filestore.WithStorageAdmissionSubject(r.Context(), subject)))
	})
}
