package api

import "net/http"

type APIError struct {
	Status  int
	Code    string
	Message string
	Details map[string][]string
}

func writeAPIError(w http.ResponseWriter, r *http.Request, apiError APIError) {
	if apiError.Details == nil {
		apiError.Details = map[string][]string{}
	}
	writeJSON(w, apiError.Status, struct {
		Error struct {
			Code      string              `json:"code"`
			Message   string              `json:"message"`
			Details   map[string][]string `json:"details"`
			RequestID string              `json:"requestId"`
		} `json:"error"`
	}{Error: struct {
		Code      string              `json:"code"`
		Message   string              `json:"message"`
		Details   map[string][]string `json:"details"`
		RequestID string              `json:"requestId"`
	}{
		Code:      apiError.Code,
		Message:   apiError.Message,
		Details:   apiError.Details,
		RequestID: requestID(r.Context()),
	}})
}
