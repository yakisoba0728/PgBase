// Package sqlconsole implements the bounded administrator SQL execution kernel.
package sqlconsole

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

const (
	MaximumStatements     = 10
	MaximumSQLBytes       = 64 << 10
	MaximumParameters     = 256
	MaximumParameterBytes = 64 << 10
	MaximumResultRows     = 1000
	MaximumResultColumns  = 128
	MaximumResultBytes    = 4 << 20
	MaximumAffectedRows   = 1000
	DefaultTimeout        = 5 * time.Second
	MaximumTimeout        = 30 * time.Second
	LockTimeout           = time.Second
	ResultTTL             = 5 * time.Minute
)

var (
	ErrInvalidRequest  = errors.New("invalid SQL console request")
	ErrRequestTooLarge = errors.New("SQL console request is too large")
	ErrWriteDisabled   = errors.New("SQL console writes are disabled")
	ErrForbidden       = errors.New("SQL console execution is not owned by this session")
	ErrNotFound        = errors.New("SQL console execution was not found")
	ErrConflict        = errors.New("SQL console execution identity conflicts with another request")
	ErrBusy            = errors.New("SQL console execution queue is full")
	ErrClosed          = errors.New("SQL console is closed")
)

type Mode string

const (
	ModeRead  Mode = "read"
	ModeWrite Mode = "write"
)

type Status string

const (
	StatusPending   Status = "pending"
	StatusRunning   Status = "running"
	StatusSucceeded Status = "succeeded"
	StatusFailed    Status = "failed"
	StatusCanceled  Status = "canceled"
	StatusTimedOut  Status = "timed_out"
	StatusUnknown   Status = "unknown"
)

type ParameterKind string

const (
	ParameterNull      ParameterKind = "null"
	ParameterString    ParameterKind = "string"
	ParameterBoolean   ParameterKind = "boolean"
	ParameterInteger   ParameterKind = "integer"
	ParameterDecimal   ParameterKind = "decimal"
	ParameterJSON      ParameterKind = "json"
	ParameterBytes     ParameterKind = "bytes"
	ParameterUUID      ParameterKind = "uuid"
	ParameterTimestamp ParameterKind = "timestamp"
)

type Parameter struct {
	Kind ParameterKind `json:"kind"`
	Text string        `json:"text,omitempty"`
}

type Statement struct {
	SQL        string      `json:"sql"`
	Parameters []Parameter `json:"parameters,omitempty"`
}

type Request struct {
	ExecutionID    uuid.UUID     `json:"-"`
	Mode           Mode          `json:"mode,omitempty"`
	DryRun         bool          `json:"dryRun,omitempty"`
	Timeout        time.Duration `json:"-"`
	IdempotencyKey string        `json:"-"`
	Statements     []Statement   `json:"statements"`
}

type CellKind string

const (
	CellNull     CellKind = "null"
	CellString   CellKind = "string"
	CellBoolean  CellKind = "boolean"
	CellInteger  CellKind = "integer"
	CellDecimal  CellKind = "decimal"
	CellJSON     CellKind = "json"
	CellBytes    CellKind = "bytes"
	CellTemporal CellKind = "temporal"
)

type Cell struct {
	Kind CellKind `json:"kind"`
	Text string   `json:"text,omitempty"`
}

func (cell Cell) MarshalJSON() ([]byte, error) {
	if cell.Kind == CellNull {
		return json.Marshal(struct {
			Kind CellKind `json:"kind"`
		}{Kind: cell.Kind})
	}
	return json.Marshal(struct {
		Kind CellKind `json:"kind"`
		Text string   `json:"text"`
	}{Kind: cell.Kind, Text: cell.Text})
}

type Column struct {
	Name     string `json:"name"`
	TypeOID  uint32 `json:"typeOid"`
	TypeName string `json:"typeName"`
}

type StatementResult struct {
	Columns      []Column `json:"columns,omitempty"`
	Rows         [][]Cell `json:"rows,omitempty"`
	Command      string   `json:"command,omitempty"`
	AffectedRows int64    `json:"affectedRows"`
}

type Result struct {
	Statements   []StatementResult `json:"statements"`
	Rows         int               `json:"rows"`
	AffectedRows int64             `json:"affectedRows"`
	Bytes        int               `json:"bytes"`
	Truncated    bool              `json:"truncated"`
	DryRun       bool              `json:"dryRun"`
	Committed    *bool             `json:"committed,omitempty"`
	Elapsed      time.Duration     `json:"-"`
}

type Failure struct {
	Code           string `json:"code"`
	Message        string `json:"message"`
	SQLState       string `json:"sqlState,omitempty"`
	StatementIndex int    `json:"statementIndex,omitempty"`
	Position       int    `json:"position,omitempty"`
}

type Execution struct {
	ID          uuid.UUID `json:"id"`
	Mode        Mode      `json:"mode"`
	DryRun      bool      `json:"dryRun"`
	Status      Status    `json:"status"`
	CreatedAt   time.Time `json:"createdAt"`
	StartedAt   time.Time `json:"startedAt,omitempty"`
	FinishedAt  time.Time `json:"finishedAt,omitempty"`
	ResultUntil time.Time `json:"resultUntil,omitempty"`
	Result      *Result   `json:"result,omitempty"`
	Failure     *Failure  `json:"failure,omitempty"`
}

var (
	integerPattern = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
	decimalPattern = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?$`)
)

func NormalizeRequest(request Request, writeEnabled bool) (Request, error) {
	if request.ExecutionID == uuid.Nil {
		return Request{}, fmt.Errorf("%w: execution ID is required", ErrInvalidRequest)
	}
	if request.Mode == "" {
		request.Mode = ModeRead
	}
	if request.Mode != ModeRead && request.Mode != ModeWrite {
		return Request{}, fmt.Errorf("%w: unsupported mode", ErrInvalidRequest)
	}
	if len(request.Statements) == 0 {
		return Request{}, fmt.Errorf("%w: at least one statement is required", ErrInvalidRequest)
	}
	if len(request.Statements) > MaximumStatements {
		return Request{}, fmt.Errorf("%w: at most %d statements are allowed", ErrRequestTooLarge, MaximumStatements)
	}
	if request.Timeout == 0 {
		request.Timeout = DefaultTimeout
	}
	if request.Timeout < time.Millisecond || request.Timeout > MaximumTimeout {
		return Request{}, fmt.Errorf("%w: timeout must be between 1ms and %s", ErrInvalidRequest, MaximumTimeout)
	}
	if request.Mode == ModeRead && request.DryRun {
		return Request{}, fmt.Errorf("%w: dry-run is only valid for write mode", ErrInvalidRequest)
	}
	if request.Mode == ModeWrite {
		if !writeEnabled {
			return Request{}, ErrWriteDisabled
		}
		if !validIdempotencyKey(request.IdempotencyKey) {
			return Request{}, fmt.Errorf("%w: write idempotency key is required", ErrInvalidRequest)
		}
	} else if request.IdempotencyKey != "" {
		return Request{}, fmt.Errorf("%w: read mode does not accept an idempotency key", ErrInvalidRequest)
	}

	normalized := request
	normalized.Statements = make([]Statement, len(request.Statements))
	totalSQL, totalParameters := 0, 0
	for index, statement := range request.Statements {
		if !utf8.ValidString(statement.SQL) || strings.TrimSpace(statement.SQL) == "" {
			return Request{}, fmt.Errorf("%w: statement %d must be non-empty UTF-8", ErrInvalidRequest, index)
		}
		totalSQL += len(statement.SQL)
		if totalSQL > MaximumSQLBytes {
			return Request{}, fmt.Errorf("%w: SQL exceeds %d bytes", ErrRequestTooLarge, MaximumSQLBytes)
		}
		if len(statement.Parameters) > MaximumParameters {
			return Request{}, fmt.Errorf("%w: statement %d has too many parameters", ErrRequestTooLarge, index)
		}
		normalized.Statements[index] = Statement{SQL: statement.SQL, Parameters: append([]Parameter(nil), statement.Parameters...)}
		for parameterIndex, parameter := range statement.Parameters {
			totalParameters += len(parameter.Text)
			if totalParameters > MaximumParameterBytes {
				return Request{}, fmt.Errorf("%w: parameters exceed %d bytes", ErrRequestTooLarge, MaximumParameterBytes)
			}
			if err := validateParameter(parameter); err != nil {
				return Request{}, fmt.Errorf("%w: statement %d parameter %d: %v", ErrInvalidRequest, index, parameterIndex, err)
			}
		}
	}
	return normalized, nil
}

func validIdempotencyKey(value string) bool {
	if len(value) < 16 || len(value) > 256 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character <= 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validateParameter(parameter Parameter) error {
	if !utf8.ValidString(parameter.Text) {
		return errors.New("text must be UTF-8")
	}
	switch parameter.Kind {
	case ParameterNull:
		if parameter.Text != "" {
			return errors.New("null must not carry text")
		}
	case ParameterString:
	case ParameterBoolean:
		if parameter.Text != "true" && parameter.Text != "false" {
			return errors.New("boolean must be true or false")
		}
	case ParameterInteger:
		if !integerPattern.MatchString(parameter.Text) {
			return errors.New("integer is not canonical")
		}
	case ParameterDecimal:
		if !decimalPattern.MatchString(parameter.Text) {
			return errors.New("decimal is not canonical")
		}
	case ParameterJSON:
		if _, err := CanonicalJSON([]byte(parameter.Text)); err != nil {
			return errors.New("JSON is invalid")
		}
	case ParameterBytes:
		if _, err := base64.StdEncoding.Strict().DecodeString(parameter.Text); err != nil {
			return errors.New("bytes must be canonical base64")
		}
	case ParameterUUID:
		parsed, err := uuid.Parse(parameter.Text)
		if err != nil || parsed.String() != parameter.Text {
			return errors.New("UUID must be canonical")
		}
	case ParameterTimestamp:
		if _, err := time.Parse(time.RFC3339Nano, parameter.Text); err != nil {
			return errors.New("timestamp must be RFC 3339")
		}
	default:
		return errors.New("kind is unsupported")
	}
	return nil
}

func RequestDigest(secret []byte, request Request) [sha256.Size]byte {
	return digestRequest(secret, "sql-console-request\x00", request, true)
}

func QueryFingerprint(secret []byte, statements []Statement) [sha256.Size]byte {
	return digestRequest(secret, "sql-console-query\x00", Request{Statements: statements}, false)
}

func IdempotencyDigest(secret []byte, key string) [sha256.Size]byte {
	hash := hmac.New(sha256.New, secret)
	writeDigestField(hash, []byte("sql-console-idempotency\x00"))
	writeDigestField(hash, []byte(key))
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

func digestRequest(secret []byte, domain string, request Request, includeRequest bool) [sha256.Size]byte {
	hash := hmac.New(sha256.New, secret)
	writeDigestField(hash, []byte(domain))
	if includeRequest {
		writeDigestField(hash, request.ExecutionID[:])
		writeDigestField(hash, []byte(request.Mode))
		if request.DryRun {
			writeDigestField(hash, []byte{1})
		} else {
			writeDigestField(hash, []byte{0})
		}
		writeDigestField(hash, []byte(request.Timeout.String()))
		writeDigestField(hash, []byte(request.IdempotencyKey))
	}
	for _, statement := range request.Statements {
		writeDigestField(hash, []byte(statement.SQL))
		if includeRequest {
			for _, parameter := range statement.Parameters {
				writeDigestField(hash, []byte(parameter.Kind))
				writeDigestField(hash, []byte(parameter.Text))
			}
		}
	}
	var result [sha256.Size]byte
	copy(result[:], hash.Sum(nil))
	return result
}

type digestWriter interface {
	Write([]byte) (int, error)
}

func writeDigestField(writer digestWriter, value []byte) {
	var size [8]byte
	binary.BigEndian.PutUint64(size[:], uint64(len(value)))
	_, _ = writer.Write(size[:])
	_, _ = writer.Write(value)
}

func EncodeCells(cells []Cell) ([]byte, error) {
	for index, cell := range cells {
		if err := validateCell(cell); err != nil {
			return nil, fmt.Errorf("cell %d: %w", index, err)
		}
	}
	return json.Marshal(cells)
}

func validateCell(cell Cell) error {
	if !utf8.ValidString(cell.Text) {
		return errors.New("cell text must be UTF-8")
	}
	switch cell.Kind {
	case CellNull:
		if cell.Text != "" {
			return errors.New("null cell must not carry text")
		}
	case CellString, CellTemporal:
	case CellBoolean:
		if cell.Text != "true" && cell.Text != "false" {
			return errors.New("boolean cell is invalid")
		}
	case CellInteger:
		if !integerPattern.MatchString(cell.Text) {
			return errors.New("integer cell is invalid")
		}
	case CellDecimal:
		if !decimalPattern.MatchString(cell.Text) {
			return errors.New("decimal cell is invalid")
		}
	case CellJSON:
		if !json.Valid([]byte(cell.Text)) {
			return errors.New("JSON cell is invalid")
		}
	case CellBytes:
		decoded, err := base64.StdEncoding.Strict().DecodeString(cell.Text)
		if err != nil || base64.StdEncoding.EncodeToString(decoded) != cell.Text {
			return errors.New("bytes cell is invalid")
		}
	default:
		return errors.New("cell kind is unsupported")
	}
	return nil
}

func CloneExecution(execution Execution) Execution {
	clone := execution
	if execution.Result != nil {
		result := *execution.Result
		result.Statements = make([]StatementResult, len(execution.Result.Statements))
		for statementIndex, source := range execution.Result.Statements {
			statement := source
			statement.Columns = append([]Column(nil), source.Columns...)
			statement.Rows = make([][]Cell, len(source.Rows))
			for rowIndex, row := range source.Rows {
				statement.Rows[rowIndex] = append([]Cell(nil), row...)
			}
			result.Statements[statementIndex] = statement
		}
		clone.Result = &result
	}
	if execution.Failure != nil {
		failure := *execution.Failure
		clone.Failure = &failure
	}
	return clone
}

func EqualDigest(left, right [sha256.Size]byte) bool {
	return hmac.Equal(left[:], right[:])
}

func CanonicalJSON(value []byte) ([]byte, error) {
	if !json.Valid(value) {
		return nil, errors.New("invalid JSON")
	}
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	if err := consumeUniqueJSONValue(decoder); err != nil {
		return nil, err
	}
	if decoder.More() {
		return nil, errors.New("invalid trailing JSON value")
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, value); err != nil {
		return nil, err
	}
	return compact.Bytes(), nil
}

func consumeUniqueJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, compound := token.(json.Delim)
	if !compound {
		return nil
	}
	var expectedClosing json.Delim
	switch delimiter {
	case '{':
		expectedClosing = '}'
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return errors.New("invalid JSON object key")
			}
			if _, duplicate := keys[key]; duplicate {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			keys[key] = struct{}{}
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	case '[':
		expectedClosing = ']'
		for decoder.More() {
			if err := consumeUniqueJSONValue(decoder); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid JSON delimiter")
	}
	closing, err := decoder.Token()
	if err != nil {
		return err
	}
	if closing != expectedClosing {
		return errors.New("invalid JSON closing delimiter")
	}
	return nil
}
