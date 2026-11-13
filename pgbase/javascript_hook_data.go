package pgbase

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
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
		value = extensionMailHookRequest(*data)
	case *MailEvent:
		value = map[string]any{"purpose": data.Purpose, "message": extensionMailRequest{
			To: data.Message.To, Subject: data.Message.Subject, Text: data.Message.Text, HTML: data.Message.HTML}, "meta": cloneMailMeta(data.Meta)}
	case *api.RecordRequestEvent, *api.BatchRequestEvent, *api.SchemaImportRequestEvent, *api.CollectionRequestEvent, *api.RecordEnrichEvent,
		*schema.CollectionMutationEvent, *filestore.FileEvent, *filestore.FileAccessEvent,
		*ApplicationEvent, *AuthEvent, *StorageSettingsEvent, *backup.LifecycleEvent,
		*api.RealtimeLifecycleEvent, *api.SettingsLifecycleEvent, *SettingsReloadEvent, *ModelMutationEvent, recordops.Mutation, *recordops.Mutation:
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
	return json.Marshal(normalizeJavaScriptHookValue(decoded, reflect.ValueOf(value)))
}

func extensionMailHookRequest(message mailer.Message) extensionMailRequest {
	result := extensionMailRequest{To: message.To, Subject: message.Subject, Text: message.Text, HTML: message.HTML}
	for _, attachment := range message.Attachments {
		result.Attachments = append(result.Attachments, extensionMailAttachment{Filename: attachment.Filename, ContentType: attachment.ContentType, DataBase64: base64.StdEncoding.EncodeToString(attachment.Data)})
	}
	return result
}

func normalizeJavaScriptHookValue(value any, source reflect.Value) any {
	for source.IsValid() && (source.Kind() == reflect.Pointer || source.Kind() == reflect.Interface) {
		if source.IsNil() {
			break
		}
		source = source.Elem()
	}
	if source.IsValid() && source.Type() == reflect.TypeFor[json.RawMessage]() {
		return value
	}
	switch current := value.(type) {
	case map[string]any:
		result := make(map[string]any, len(current))
		for name, nested := range current {
			canonical := name
			var child reflect.Value
			if source.IsValid() && source.Kind() == reflect.Struct {
				for index := 0; index < source.NumField(); index++ {
					field := source.Type().Field(index)
					wireName := strings.Split(field.Tag.Get("json"), ",")[0]
					if wireName == "" {
						wireName = field.Name
					}
					if wireName == name {
						canonical = lowerJavaScriptHookName(name)
						child = source.Field(index)
						break
					}
				}
			} else if source.IsValid() && source.Kind() == reflect.Map && source.Type().Key().Kind() == reflect.String {
				child = source.MapIndex(reflect.ValueOf(name).Convert(source.Type().Key()))
			}
			if forbiddenJavaScriptHookName(canonical) {
				continue
			}
			result[canonical] = normalizeJavaScriptHookValue(nested, child)
		}
		return result
	case []any:
		result := make([]any, len(current))
		for index, nested := range current {
			var child reflect.Value
			if source.IsValid() && (source.Kind() == reflect.Slice || source.Kind() == reflect.Array) && index < source.Len() {
				child = source.Index(index)
			}
			result[index] = normalizeJavaScriptHookValue(nested, child)
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
