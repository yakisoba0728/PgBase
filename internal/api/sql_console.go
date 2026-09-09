package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/sqlconsole"
)

const (
	MaximumSQLConsoleRequestBytes  = 1 << 20
	MaximumSQLConsoleResponseBytes = 5 << 20
	MaximumSQLConsoleHistoryBytes  = 256 << 10
	SQLConsoleQueriesPath          = "/api/admin/sql/queries"
)

type SQLConsoleService interface {
	Submit(context.Context, sqlconsole.Actor, sqlconsole.Request) (sqlconsole.Execution, error)
	Get(context.Context, sqlconsole.Actor, uuid.UUID) (sqlconsole.Execution, error)
	Cancel(context.Context, sqlconsole.Actor, uuid.UUID) (sqlconsole.Execution, error)
	List(context.Context, sqlconsole.Actor, sqlconsole.Cursor) (sqlconsole.Page, error)
}

type SQLConsoleWriteTarget struct {
	Schema  string   `json:"schema"`
	Table   string   `json:"table"`
	Columns []string `json:"columns"`
}

type SQLConsoleHandlers struct {
	service       SQLConsoleService
	sessionSecret []byte
	writeTargets  []SQLConsoleWriteTarget
}

func NewSQLConsoleHandlers(service SQLConsoleService, sessionSecret []byte, targets []SQLConsoleWriteTarget) (*SQLConsoleHandlers, error) {
	if service == nil || len(sessionSecret) < 32 {
		return nil, errors.New("SQL console HTTP handler configuration is incomplete")
	}
	copyTargets := make([]SQLConsoleWriteTarget, len(targets))
	for index, target := range targets {
		copyTargets[index] = target
		copyTargets[index].Columns = append([]string(nil), target.Columns...)
	}
	return &SQLConsoleHandlers{
		service: service, sessionSecret: append([]byte(nil), sessionSecret...), writeTargets: copyTargets,
	}, nil
}

type sqlConsoleSubmitRequest struct {
	Mode       sqlconsole.Mode `json:"mode,omitempty"`
	DryRun     bool            `json:"dryRun,omitempty"`
	TimeoutMS  *int64          `json:"timeoutMs,omitempty"`
	Statements []struct {
		SQL    string                 `json:"sql"`
		Params []sqlconsole.Parameter `json:"params,omitempty"`
	} `json:"statements"`
}

func (handlers *SQLConsoleHandlers) Submit(w http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(request)
	if !ok {
		writeSQLConsoleUnauthorized(w, request)
		return
	}
	var input sqlConsoleSubmitRequest
	if err := decodeStrictJSON(request, &input); err != nil {
		writeAPIError(w, request, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid SQL execution request"})
		return
	}
	executionID, err := sqlConsoleExecutionID(request.Header.Values(operationIDHeader))
	if err != nil {
		writeSQLConsoleError(w, request, err)
		return
	}
	idempotencyKey, err := singletonOptionalHeader(request.Header.Values(idempotencyKeyHeader))
	if err != nil {
		writeSQLConsoleError(w, request, err)
		return
	}
	timeout := time.Duration(0)
	if input.TimeoutMS != nil {
		if *input.TimeoutMS <= 0 || *input.TimeoutMS > sqlconsole.MaximumTimeout.Milliseconds() {
			writeSQLConsoleError(w, request, sqlconsole.ErrInvalidRequest)
			return
		}
		timeout = time.Duration(*input.TimeoutMS) * time.Millisecond
	}
	statements := make([]sqlconsole.Statement, len(input.Statements))
	for index, statement := range input.Statements {
		statements[index] = sqlconsole.Statement{SQL: statement.SQL, Parameters: append([]sqlconsole.Parameter(nil), statement.Params...)}
	}
	execution, err := handlers.service.Submit(request.Context(), actor, sqlconsole.Request{
		ExecutionID: executionID, Mode: input.Mode, DryRun: input.DryRun, Timeout: timeout,
		IdempotencyKey: idempotencyKey, Statements: statements,
	})
	if err != nil {
		writeSQLConsoleError(w, request, err)
		return
	}
	w.Header().Set("Location", SQLConsoleQueriesPath+"/"+execution.ID.String())
	writeJSON(w, http.StatusAccepted, sqlConsoleExecutionResponse(execution))
}

func (handlers *SQLConsoleHandlers) Get(w http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(request)
	if !ok {
		writeSQLConsoleUnauthorized(w, request)
		return
	}
	executionID, err := uuid.Parse(request.PathValue("id"))
	if err != nil || executionID == uuid.Nil || request.URL.RawQuery != "" {
		writeSQLConsoleNotFound(w, request)
		return
	}
	execution, err := handlers.service.Get(request.Context(), actor, executionID)
	if err != nil {
		writeSQLConsoleError(w, request, err)
		return
	}
	writeJSON(w, http.StatusOK, sqlConsoleExecutionResponse(execution))
}

func (handlers *SQLConsoleHandlers) Cancel(w http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(request)
	if !ok {
		writeSQLConsoleUnauthorized(w, request)
		return
	}
	executionID, err := uuid.Parse(request.PathValue("id"))
	if err != nil || executionID == uuid.Nil || request.URL.RawQuery != "" {
		writeSQLConsoleNotFound(w, request)
		return
	}
	execution, err := handlers.service.Cancel(request.Context(), actor, executionID)
	if err != nil {
		writeSQLConsoleError(w, request, err)
		return
	}
	writeJSON(w, http.StatusAccepted, sqlConsoleExecutionResponse(execution))
}

func (handlers *SQLConsoleHandlers) List(w http.ResponseWriter, request *http.Request) {
	actor, ok := handlers.actor(request)
	if !ok {
		writeSQLConsoleUnauthorized(w, request)
		return
	}
	values, err := url.ParseQuery(request.URL.RawQuery)
	if err != nil || duplicateSingleton(values, "cursor") || hasUnexpectedQuery(values, "cursor") {
		writeSQLConsoleError(w, request, sqlconsole.ErrInvalidRequest)
		return
	}
	cursor, err := decodeSQLConsoleCursor(values.Get("cursor"))
	if err != nil {
		writeSQLConsoleError(w, request, sqlconsole.ErrInvalidRequest)
		return
	}
	page, err := handlers.service.List(request.Context(), actor, cursor)
	if err != nil {
		writeSQLConsoleError(w, request, err)
		return
	}
	response := struct {
		Executions   []sqlConsoleExecutionDTO `json:"executions"`
		NextCursor   string                   `json:"nextCursor,omitempty"`
		WriteEnabled bool                     `json:"writeEnabled"`
		WriteTargets []SQLConsoleWriteTarget  `json:"writeTargets"`
	}{
		Executions:   make([]sqlConsoleExecutionDTO, len(page.Executions)),
		WriteEnabled: len(handlers.writeTargets) > 0,
		WriteTargets: append([]SQLConsoleWriteTarget(nil), handlers.writeTargets...),
	}
	for index, execution := range page.Executions {
		response.Executions[index] = sqlConsoleExecutionResponse(execution)
	}
	if page.Next != nil {
		response.NextCursor = encodeSQLConsoleCursor(*page.Next)
	}
	writeJSON(w, http.StatusOK, response)
}

func (handlers *SQLConsoleHandlers) actor(request *http.Request) (sqlconsole.Actor, bool) {
	principal, ok := AdminPrincipalFromContext(request.Context())
	if !ok || principal.Administrator.ID == uuid.Nil || principal.RawSession == "" {
		return sqlconsole.Actor{}, false
	}
	return sqlconsole.Actor{
		AdministratorID: principal.Administrator.ID,
		SessionKey:      sqlconsole.DeriveSessionKey(handlers.sessionSecret, []byte(principal.RawSession)),
	}, true
}

type sqlConsoleExecutionDTO struct {
	ID          uuid.UUID             `json:"id"`
	Mode        sqlconsole.Mode       `json:"mode"`
	DryRun      bool                  `json:"dryRun"`
	Status      sqlconsole.Status     `json:"status"`
	CreatedAt   time.Time             `json:"createdAt"`
	StartedAt   *time.Time            `json:"startedAt,omitempty"`
	FinishedAt  *time.Time            `json:"finishedAt,omitempty"`
	ResultUntil *time.Time            `json:"resultUntil,omitempty"`
	Result      *sqlConsoleResultDTO  `json:"result,omitempty"`
	Failure     *sqlConsoleFailureDTO `json:"failure,omitempty"`
}

type sqlConsoleResultDTO struct {
	Statements   []sqlconsole.StatementResult `json:"statements,omitempty"`
	Rows         int                          `json:"rows"`
	AffectedRows int64                        `json:"affectedRows"`
	Bytes        int                          `json:"bytes"`
	Truncated    bool                         `json:"truncated"`
	DryRun       bool                         `json:"dryRun"`
	Committed    *bool                        `json:"committed,omitempty"`
	ElapsedMS    int64                        `json:"elapsedMs"`
}

type sqlConsoleFailureDTO struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	SQLState       string `json:"sqlState,omitempty"`
	StatementIndex *int   `json:"statementIndex,omitempty"`
	Position       int    `json:"position,omitempty"`
}

func sqlConsoleExecutionResponse(execution sqlconsole.Execution) sqlConsoleExecutionDTO {
	response := sqlConsoleExecutionDTO{
		ID: execution.ID, Mode: execution.Mode, DryRun: execution.DryRun, Status: execution.Status, CreatedAt: execution.CreatedAt,
	}
	if !execution.StartedAt.IsZero() {
		value := execution.StartedAt
		response.StartedAt = &value
	}
	if !execution.FinishedAt.IsZero() {
		value := execution.FinishedAt
		response.FinishedAt = &value
	}
	if !execution.ResultUntil.IsZero() {
		value := execution.ResultUntil
		response.ResultUntil = &value
	}
	if execution.Result != nil {
		response.Result = &sqlConsoleResultDTO{
			Statements: execution.Result.Statements, Rows: execution.Result.Rows,
			AffectedRows: execution.Result.AffectedRows, Bytes: execution.Result.Bytes,
			Truncated: execution.Result.Truncated, DryRun: execution.Result.DryRun,
			Committed: execution.Result.Committed, ElapsedMS: execution.Result.Elapsed.Milliseconds(),
		}
	}
	if execution.Failure != nil {
		failure := execution.Failure
		response.Failure = &sqlConsoleFailureDTO{
			Code: failure.Code, Message: failure.Message, SQLState: failure.SQLState, Position: failure.Position,
		}
		if failure.HasStatement {
			index := failure.StatementIndex
			response.Failure.StatementIndex = &index
		}
	}
	return response
}

func sqlConsoleExecutionID(values []string) (uuid.UUID, error) {
	if len(values) == 0 {
		return uuid.New(), nil
	}
	if len(values) != 1 {
		return uuid.Nil, sqlconsole.ErrInvalidRequest
	}
	value, err := uuid.Parse(values[0])
	if err != nil || value == uuid.Nil {
		return uuid.Nil, sqlconsole.ErrInvalidRequest
	}
	return value, nil
}

func singletonOptionalHeader(values []string) (string, error) {
	if len(values) == 0 {
		return "", nil
	}
	if len(values) != 1 || strings.TrimSpace(values[0]) != values[0] || values[0] == "" {
		return "", sqlconsole.ErrInvalidRequest
	}
	return values[0], nil
}

func hasUnexpectedQuery(values url.Values, allowed ...string) bool {
	known := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		known[name] = struct{}{}
	}
	for name := range values {
		if _, ok := known[name]; !ok {
			return true
		}
	}
	return false
}

func encodeSQLConsoleCursor(cursor sqlconsole.Cursor) string {
	value, _ := json.Marshal(struct {
		CreatedAt time.Time `json:"createdAt"`
		ID        uuid.UUID `json:"id"`
	}{CreatedAt: cursor.CreatedAt, ID: cursor.ID})
	return base64.RawURLEncoding.EncodeToString(value)
}

func decodeSQLConsoleCursor(value string) (sqlconsole.Cursor, error) {
	if value == "" {
		return sqlconsole.Cursor{}, nil
	}
	if len(value) > 256 {
		return sqlconsole.Cursor{}, sqlconsole.ErrInvalidRequest
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(value)
	if err != nil {
		return sqlconsole.Cursor{}, sqlconsole.ErrInvalidRequest
	}
	var decoded struct {
		CreatedAt time.Time `json:"createdAt"`
		ID        uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.CreatedAt.IsZero() || decoded.ID == uuid.Nil {
		return sqlconsole.Cursor{}, sqlconsole.ErrInvalidRequest
	}
	canonical := encodeSQLConsoleCursor(sqlconsole.Cursor{CreatedAt: decoded.CreatedAt, ID: decoded.ID})
	if canonical != value {
		return sqlconsole.Cursor{}, sqlconsole.ErrInvalidRequest
	}
	return sqlconsole.Cursor{CreatedAt: decoded.CreatedAt, ID: decoded.ID}, nil
}

func writeSQLConsoleUnauthorized(w http.ResponseWriter, request *http.Request) {
	writeAPIError(w, request, APIError{Status: http.StatusUnauthorized, Code: "authentication_required", Message: "Administrator authentication is required"})
}

func writeSQLConsoleNotFound(w http.ResponseWriter, request *http.Request) {
	writeAPIError(w, request, APIError{Status: http.StatusNotFound, Code: "not_found", Message: "SQL execution was not found"})
}

func writeSQLConsoleError(w http.ResponseWriter, request *http.Request, err error) {
	switch {
	case errors.Is(err, sqlconsole.ErrInvalidRequest), errors.Is(err, sqlconsole.ErrRequestTooLarge):
		writeAPIError(w, request, APIError{Status: http.StatusBadRequest, Code: "invalid_request", Message: "Invalid SQL execution request"})
	case errors.Is(err, sqlconsole.ErrWriteDisabled), errors.Is(err, sqlconsole.ErrForbidden):
		writeAPIError(w, request, APIError{Status: http.StatusForbidden, Code: "forbidden", Message: "SQL execution is not allowed"})
	case errors.Is(err, sqlconsole.ErrNotFound), errors.Is(err, pgx.ErrNoRows):
		writeSQLConsoleNotFound(w, request)
	case errors.Is(err, sqlconsole.ErrConflict):
		writeAPIError(w, request, APIError{Status: http.StatusConflict, Code: "conflict", Message: "SQL execution identity conflicts with another request"})
	case errors.Is(err, sqlconsole.ErrBusy):
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, request, APIError{Status: http.StatusTooManyRequests, Code: "busy", Message: "SQL execution queue is full"})
	case errors.Is(err, sqlconsole.ErrClosed), database.Classify(err) == database.KindUnavailable:
		w.Header().Set("Retry-After", "1")
		writeAPIError(w, request, APIError{Status: http.StatusServiceUnavailable, Code: "unavailable", Message: "SQL execution is unavailable"})
	default:
		writeAPIError(w, request, APIError{Status: http.StatusInternalServerError, Code: "internal_error", Message: "Internal server error"})
	}
}
