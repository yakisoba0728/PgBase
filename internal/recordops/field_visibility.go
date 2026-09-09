package recordops

import (
	"encoding/json"

	"github.com/webisnoteasy/PgBase/internal/schema"
	validationdomain "github.com/webisnoteasy/PgBase/internal/validation"
)

func rejectHiddenInput(collection schema.Collection, principal Principal, input map[string]json.RawMessage) error {
	if principal.Kind == PrincipalAdministrator {
		return nil
	}
	for _, field := range collection.Fields {
		if field.Hidden {
			if _, found := input[field.Name]; found {
				return &validationdomain.Error{Details: []validationdomain.Detail{{Field: field.Name, Code: "hidden_field"}}}
			}
		}
	}
	return nil
}

func projectMutationResult(collection schema.Collection, principal Principal, result *MutationResult) {
	if principal.Kind == PrincipalAdministrator || result == nil || result.Record == nil {
		return
	}
	data := make(map[string]any, len(result.Record.Data))
	for key, value := range result.Record.Data {
		data[key] = value
	}
	for _, field := range collection.Fields {
		if field.Hidden {
			delete(data, field.Name)
		}
	}
	result.Record.Data = data
}
