package api

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	if w.Header().Get("ETag") != "" {
		// Proxy compression can weaken the validator and make every If-Match write fail.
		w.Header().Add("Cache-Control", "no-transform")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
