package api

import "net/url"

// duplicateSingleton reports whether a recognized parameter was supplied more
// than once. Query parameters at these HTTP seams have single-value semantics;
// silently selecting the first value lets proxies and clients disagree about
// the request that was authorized and executed.
func duplicateSingleton(values url.Values, names ...string) bool {
	for _, name := range names {
		if len(values[name]) > 1 {
			return true
		}
	}
	return false
}
