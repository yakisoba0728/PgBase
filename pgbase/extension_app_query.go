package pgbase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/yakisoba0728/PgBase/internal/identity"
	"github.com/yakisoba0728/PgBase/internal/observability"
)

type extensionAppQueryRequest struct {
	Collection string          `json:"collection,omitempty"`
	ID         json.RawMessage `json:"id,omitempty"`
	IDs        []string        `json:"ids,omitempty"`
	Filter     string          `json:"filter,omitempty"`
	Sort       string          `json:"sort,omitempty"`
	Limit      int             `json:"limit,omitempty"`
	Cursor     string          `json:"cursor,omitempty"`
	Field      string          `json:"field,omitempty"`
	Value      json.RawMessage `json:"value,omitempty"`
	Expand     string          `json:"expand,omitempty"`
	Before     uint64          `json:"before,omitempty"`
	Status     int             `json:"statusClass,omitempty"`
	Method     string          `json:"method,omitempty"`
	Route      string          `json:"route,omitempty"`
	From       *time.Time      `json:"from,omitempty"`
	To         *time.Time      `json:"to,omitempty"`
	Rule       string          `json:"rule,omitempty"`
}

func (host *extensionHost) callAppQueries(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
	if host.principal.Kind != identity.Administrator || host.bridge.requestLog == nil {
		return nil, extensionError("permission_denied", errors.New("administrator request-log access required"))
	}
	var request extensionAppQueryRequest
	if err := decodeExtensionInput(raw, &request); err != nil {
		return nil, extensionError("invalid_app_query", err)
	}
	switch method {
	case "app.log.query":
		page, err := host.bridge.requestLog.Search(ctx, observability.RequestFilter{Before: request.Before, StatusClass: request.Status, Method: request.Method, Route: request.Route, From: request.From, To: request.To, Limit: request.Limit})
		return marshalExtensionResult(page, err)
	case "app.log.find":
		id, err := rawUint(request.ID)
		if err != nil {
			return nil, extensionError("invalid_app_query", err)
		}
		entry, err := host.bridge.requestLog.Detail(ctx, id)
		return marshalExtensionResult(entry, err)
	case "app.log.stats":
		stats, err := host.bridge.requestLog.Statistics(ctx)
		return marshalExtensionResult(stats, err)
	case "app.log.deleteOld":
		if request.To == nil {
			return nil, extensionError("invalid_app_query", errors.New("to is required"))
		}
		count, err := host.bridge.requestLog.DeleteBefore(ctx, *request.To)
		return marshalExtensionResult(map[string]int64{"deleted": count}, err)
	}
	return nil, extensionError("unsupported_operation", nil)
}

func (host *extensionHost) callAppRecordQuery(ctx context.Context, method string, request extensionAppQueryRequest) (json.RawMessage, error) {
	call := func(name string, value any) (json.RawMessage, error) {
		encoded, _ := json.Marshal(value)
		return host.callRecords(ctx, name, encoded)
	}
	switch method {
	case "app.record.findByIds", "app.record.expandMany":
		items := make([]map[string]any, 0, len(request.IDs))
		if len(request.IDs) > 1000 {
			return nil, extensionError("invalid_record_request", errors.New("too many ids"))
		}
		for _, id := range request.IDs {
			raw, err := call("records.get", extensionRecordRequest{Collection: request.Collection, ID: id, Expand: request.Expand})
			if err != nil {
				return nil, err
			}
			var item extensionRecordResult
			if json.Unmarshal(raw, &item) != nil {
				return nil, extensionError("invalid_record_result", nil)
			}
			items = append(items, item.Data)
		}
		return json.Marshal(extensionRecordResult{Items: items})
	case "app.record.findAll", "app.record.count":
		cursor, items, count := "", []map[string]any{}, int64(0)
		for {
			raw, err := call("records.list", extensionRecordRequest{Collection: request.Collection, Cursor: cursor, Limit: 200, Filter: request.Filter, Sort: request.Sort, Expand: request.Expand})
			if err != nil {
				return nil, err
			}
			var page extensionRecordResult
			if json.Unmarshal(raw, &page) != nil {
				return nil, extensionError("invalid_record_result", nil)
			}
			count += int64(len(page.Items))
			if method == "app.record.findAll" {
				items = append(items, page.Items...)
			}
			if page.NextCursor == "" {
				break
			}
			cursor = page.NextCursor
		}
		if method == "app.record.count" {
			return json.Marshal(count)
		}
		return json.Marshal(extensionRecordResult{Items: items})
	case "app.record.findFirstByData":
		if request.Field == "" || strings.ContainsAny(request.Field, "'\" ") {
			return nil, extensionError("invalid_record_request", nil)
		}
		request.Filter = request.Field + " = " + pocketBaseFilterLiteral(request.Value)
		request.Limit = 1
		raw, err := call("records.list", extensionRecordRequest{Collection: request.Collection, Filter: request.Filter, Limit: 1})
		if err != nil {
			return nil, err
		}
		var page extensionRecordResult
		if json.Unmarshal(raw, &page) != nil || len(page.Items) == 0 {
			return nil, extensionError("record_not_found", nil)
		}
		return json.Marshal(page.Items[0])
	case "app.record.canAccess":
		id, decodeErr := rawString(request.ID)
		if decodeErr != nil {
			return nil, extensionError("invalid_record_request", decodeErr)
		}
		filter := "id = '" + strings.ReplaceAll(id, "'", "''") + "'"
		if request.Rule != "" {
			filter = "(" + request.Rule + ") && " + filter
		}
		raw, err := call("records.list", extensionRecordRequest{Collection: request.Collection, Filter: filter, Limit: 1})
		if err != nil {
			return json.Marshal(false)
		}
		var page extensionRecordResult
		if json.Unmarshal(raw, &page) != nil {
			return nil, extensionError("invalid_record_result", nil)
		}
		return json.Marshal(len(page.Items) == 1)
	case "app.record.expand":
		id, err := rawString(request.ID)
		if err != nil {
			return nil, extensionError("invalid_record_request", err)
		}
		return call("records.get", extensionRecordRequest{Collection: request.Collection, ID: id, Expand: request.Expand})
	}
	return nil, extensionError("unsupported_operation", fmt.Errorf("unsupported app record query"))
}

func rawUint(raw json.RawMessage) (uint64, error) {
	var value uint64
	if len(raw) == 0 || json.Unmarshal(raw, &value) != nil {
		return 0, errors.New("invalid id")
	}
	return value, nil
}
func rawString(raw json.RawMessage) (string, error) {
	var value string
	if json.Unmarshal(raw, &value) != nil || value == "" {
		return "", errors.New("invalid id")
	}
	return value, nil
}
func pocketBaseFilterLiteral(raw json.RawMessage) string {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return "'" + strings.ReplaceAll(s, "'", "''") + "'"
	}
	return string(raw)
}

var extensionDBXField = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.:]{0,191}$`)

type extensionDBXNode struct {
	Op   string            `json:"op"`
	Args []json.RawMessage `json:"args"`
}

func compileExtensionDBX(raw json.RawMessage) (string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var literal string
	if json.Unmarshal(raw, &literal) == nil {
		return literal, nil
	}
	var node extensionDBXNode
	if json.Unmarshal(raw, &node) != nil || len(node.Args) == 0 || len(node.Args) > 32 {
		return "", errors.New("invalid dbx expression")
	}
	compound := func(join string) (string, error) {
		parts := make([]string, len(node.Args))
		for i, arg := range node.Args {
			value, err := compileExtensionDBX(arg)
			if err != nil {
				return "", err
			}
			parts[i] = "(" + value + ")"
		}
		return strings.Join(parts, join), nil
	}
	switch node.Op {
	case "exp":
		if len(node.Args) != 2 {
			return "", errors.New("invalid dbx exp")
		}
		var expression string
		var params map[string]json.RawMessage
		if json.Unmarshal(node.Args[0], &expression) != nil || json.Unmarshal(node.Args[1], &params) != nil {
			return "", errors.New("invalid dbx exp")
		}
		if len(params) > 64 {
			return "", errors.New("too many dbx params")
		}
		for name, value := range params {
			if !extensionDBXField.MatchString(name) {
				return "", errors.New("invalid dbx param")
			}
			expression = strings.ReplaceAll(expression, "{:"+name+"}", pocketBaseFilterLiteral(value))
		}
		if strings.Contains(expression, "{:") {
			return "", errors.New("missing dbx param")
		}
		return expression, nil
	case "and":
		return compound(" && ")
	case "or":
		return compound(" || ")
	case "not":
		return "", errors.New("generic dbx not is unsupported; use a typed negative operator")
	case "in", "notIn", "like", "notLike", "orLike", "orNotLike", "between", "notBetween":
		var field string
		if json.Unmarshal(node.Args[0], &field) != nil || !extensionDBXField.MatchString(field) {
			return "", errors.New("invalid dbx field")
		}
		switch node.Op {
		case "in", "notIn":
			if len(node.Args) != 2 {
				return "", errors.New("invalid in")
			}
			var values []json.RawMessage
			if json.Unmarshal(node.Args[1], &values) != nil || len(values) == 0 || len(values) > 1000 {
				return "", errors.New("invalid in values")
			}
			parts := make([]string, len(values))
			op := " = "
			join := " || "
			if node.Op == "notIn" {
				op = " != "
				join = " && "
			}
			for i, v := range values {
				parts[i] = field + op + pocketBaseFilterLiteral(v)
			}
			return "(" + strings.Join(parts, join) + ")", nil
		case "between", "notBetween":
			if len(node.Args) != 3 {
				return "", errors.New("invalid between")
			}
			low, high := pocketBaseFilterLiteral(node.Args[1]), pocketBaseFilterLiteral(node.Args[2])
			if node.Op == "notBetween" {
				return "(" + field + " < " + low + " || " + field + " > " + high + ")", nil
			}
			return "(" + field + " >= " + low + " && " + field + " <= " + high + ")", nil
		default:
			if len(node.Args) != 2 {
				return "", errors.New("invalid like")
			}
			op := " ~ "
			if strings.Contains(node.Op, "not") || strings.Contains(node.Op, "Not") {
				op = " !~ "
			}
			return field + op + pocketBaseFilterLiteral(node.Args[1]), nil
		}
	}
	return "", errors.New("unsupported dbx expression")
}
