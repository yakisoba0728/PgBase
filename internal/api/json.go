package api

import (
	"encoding/json"
	"net/http"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	writeJSONHeader(w, status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeJSONHeader(w http.ResponseWriter, status int) {
	if w.Header().Get("ETag") != "" {
		// Proxy compression can weaken the validator and make every If-Match write fail.
		w.Header().Add("Cache-Control", "no-transform")
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
}
