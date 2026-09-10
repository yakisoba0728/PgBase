package pgbase

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/filestore"
	"github.com/yakisoba0728/PgBase/internal/mailer"
	"github.com/yakisoba0728/PgBase/internal/recordops"
	"github.com/yakisoba0728/PgBase/internal/schema"
)

func marshalJavaScriptHookData(event *HookEvent) (json.RawMessage, error) {
	if event == nil || event.Data == nil {
		return nil, errors.New("JavaScript hook event data is nil")
	}
	var value any
	switch data := event.Data.(type) {
	case *RequestEvent:
		if data == nil || data.Request == nil {
			return nil, errors.New("JavaScript request hook data is nil")
		}
		principal, _ := PrincipalFromContext(data.Request.Context())
		value = map[string]any{"method": data.Request.Method, "path": data.Request.URL.Path,
			"query": cloneQuery(data.Request.URL.Query()), "headers": safeJavaScriptHeaders(data.Request.Header), "principal": principal}
	case *mailer.Message:
		value = extensionMailRequest{To: data.To, Subject: data.Subject, Text: data.Text, HTML: data.HTML}
	case *MailEvent:
		value = map[string]any{"purpose": data.Purpose, "message": extensionMailRequest{
			To: data.Message.To, Subject: data.Message.Subject, Text: data.Message.Text, HTML: data.Message.HTML}}
	case *api.RecordRequestEvent, *api.BatchRequestEvent, *api.SchemaImportRequestEvent,
		*schema.CollectionMutationEvent, *filestore.FileEvent, *filestore.FileAccessEvent,
		*ApplicationEvent, *AuthEvent, *StorageSettingsEvent, *backup.LifecycleEvent,
		*api.RealtimeLifecycleEvent, recordops.Mutation, *recordops.Mutation:
		value = data
	default:
		return nil, fmt.Errorf("JavaScript hook %s has unsupported data type %T", event.Kind, event.Data)
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("encode JavaScript hook %s data: %w", event.Kind, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("decode JavaScript hook %s data: %w", event.Kind, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("JavaScript hook data has trailing JSON")
	}
	return json.Marshal(normalizeJavaScriptHookValue(decoded))
}

func normalizeJavaScriptHookValue(value any) any {
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for name, nested := range current {
			canonical := lowerJavaScriptHookName(name)
			if forbiddenJavaScriptHookName(canonical) {
				continue
			}
			result[canonical] = normalizeJavaScriptHookValue(nested)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, nested := range current {
			result[index] = normalizeJavaScriptHookValue(nested)
		}
		return result
	default:
		return value
	}
}

func lowerJavaScriptHookName(name string) string {
	if name == "" || name[0] < 'A' || name[0] > 'Z' {
		return name
	}
	return strings.ToLower(name[:1]) + name[1:]
}

func forbiddenJavaScriptHookName(name string) bool {
	compact := strings.ToLower(strings.NewReplacer("_", "", "-", "").Replace(name))
	switch compact {
	case "authorization", "cookie", "password", "token", "oauthcode", "otpcode", "accesskey", "secretkey",
		"objectkey", "thumbnailkey", "databaseurl", "error":
		return true
	default:
		return false
	}
}
