package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/query"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

type RecordService interface {
	List(context.Context, string, string, int) (query.RecordPage, error)
	Get(context.Context, string, uuid.UUID) (map[string]any, error)
	Create(context.Context, string, map[string]json.RawMessage) (map[string]any, error)
	Update(context.Context, string, uuid.UUID, map[string]json.RawMessage) (map[string]any, error)
	Delete(context.Context, string, uuid.UUID) error
}

type recordEndpoints struct{ service RecordService }

func (e recordEndpoints) list(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil {
			writeInvalidRecordRequest(w, r)
			return
		}
		limit = parsed
		if limit <= 0 {
			limit = 50
		} else if limit > 200 {
			limit = 200
		}
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	page, err := e.service.List(r.Context(), r.PathValue("collection"), r.URL.Query().Get("cursor"), limit)
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, page)
}

func (e recordEndpoints) get(w http.ResponseWriter, r *http.Request) {
	id, ok := recordID(w, r)
	if !ok {
		return
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	record, err := e.service.Get(r.Context(), r.PathValue("collection"), id)
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, record)
}

func (e recordEndpoints) create(w http.ResponseWriter, r *http.Request) {
	input, ok := recordInput(w, r)
	if !ok {
		return
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	record, err := e.service.Create(r.Context(), r.PathValue("collection"), input)
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusCreated, record)
}

func (e recordEndpoints) update(w http.ResponseWriter, r *http.Request) {
	id, ok := recordID(w, r)
	if !ok {
		return
	}
	input, ok := recordInput(w, r)
	if !ok {
		return
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	record, err := e.service.Update(r.Context(), r.PathValue("collection"), id, input)
	if err != nil {
		writeRecordError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, record)
}

func (e recordEndpoints) delete(w http.ResponseWriter, r *http.Request) {
	id, ok := recordID(w, r)
	if !ok {
		return
	}
	if e.service == nil {
		writeRecordError(w, r, errors.New("record service is unavailable"))
		return
	}
	if err := e.service.Delete(r.Context(), r.PathValue("collection"), id); err != nil {
		writeRecordError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

func recordInput(w http.ResponseWriter, r *http.Request) (map[string]json.RawMessage, bool) {
	var input map[string]json.RawMessage
	if err := decodeStrictJSON(r, &input); err != nil || input == nil {
		writeInvalidRecordRequest(w, r)
		return nil, false
	}
	return input, true
}

func recordID(w http.ResponseWriter, r *http.Request) (uuid.UUID, bool) {
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil || id == uuid.Nil {
		writeInvalidRecordRequest(w, r)
		return uuid.Nil, false
	}
	return id, true
}

func writeInvalidRecordRequest(w http.ResponseWriter, r *http.Request) {
	writeAPIError(w, r, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid request"})
}

func writeRecordError(w http.ResponseWriter, r *http.Request, err error) {
	apiError := APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"}
	var validation *query.ValidationError
	databaseKind := database.Classify(err)
	switch {
	case query.IsInvalidCursor(err):
		apiError = APIError{Status: http.StatusBadRequest, Code: "invalid_cursor", Message: "Cursor is invalid"}
	case errors.Is(err, query.ErrCollectionNotFound), errors.Is(err, query.ErrRecordNotFound):
		apiError = APIError{Status: http.StatusNotFound, Code: "not_found", Message: "Record or collection not found"}
	case errors.Is(err, schema.ErrConflict):
		apiError = APIError{Status: http.StatusConflict, Code: "schema_conflict", Message: "Schema changed during the request"}
	case databaseKind == database.KindConflict:
		apiError = APIError{Status: http.StatusConflict, Code: "record_conflict", Message: "Record conflicts with existing data"}
	case errors.As(err, &validation):
		details := make(map[string][]string, len(validation.Details))
		for _, detail := range validation.Details {
			details[detail.Field] = append(details[detail.Field], detail.Code)
		}
		apiError = APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "Record input is invalid", Details: details}
	case databaseKind == database.KindValidation:
		apiError = APIError{Status: http.StatusUnprocessableEntity, Code: "validation_failed", Message: "Record input is invalid"}
	case databaseKind == database.KindUnavailable, databaseKind == database.KindCanceled:
		apiError = APIError{Status: http.StatusServiceUnavailable, Code: "record_unavailable", Message: "Record service is unavailable"}
	}
	if databaseKind == database.KindUnavailable {
		w.Header().Set("Retry-After", "1")
	}
	writeAPIError(w, r, apiError)
}
