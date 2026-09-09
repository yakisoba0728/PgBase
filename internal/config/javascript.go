package config

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/pgbase"
)

func LoadJavaScriptEnv(lookup func(string) (string, bool)) (*pgbase.JavaScriptOptions, error) {
	directory, hasDirectory := value(lookup, "PGBASE_JS_DIR")
	rawCapabilities, hasCapabilities := value(lookup, "PGBASE_JS_CAPABILITIES")
	rawSubjects, hasSubjects := value(lookup, "PGBASE_JS_JOB_SERVICE_SUBJECTS")
	if !hasDirectory || directory == "" {
		if hasCapabilities && rawCapabilities != "" || hasSubjects && rawSubjects != "" {
			return nil, fmt.Errorf("PGBASE_JS_DIR: required when JavaScript settings are configured")
		}
		return nil, nil
	}
	options := &pgbase.JavaScriptOptions{Directory: directory, Capabilities: map[string]bool{}}
	if rawCapabilities != "" {
		for _, item := range strings.Split(rawCapabilities, ",") {
			name := strings.TrimSpace(item)
			if name == "" || options.Capabilities[name] {
				return nil, fmt.Errorf("PGBASE_JS_CAPABILITIES: invalid or duplicate capability")
			}
			options.Capabilities[name] = true
		}
	}
	if rawSubjects != "" {
		var encoded map[string]string
		decoder := json.NewDecoder(strings.NewReader(rawSubjects))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&encoded); err != nil {
			return nil, fmt.Errorf("PGBASE_JS_JOB_SERVICE_SUBJECTS: %w", err)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, fmt.Errorf("PGBASE_JS_JOB_SERVICE_SUBJECTS: trailing JSON")
		}
		options.JobServiceSubjects = make(map[string]uuid.UUID, len(encoded))
		for name, raw := range encoded {
			id, err := uuid.Parse(raw)
			if name == "" || err != nil || id == uuid.Nil || id.String() != strings.ToLower(raw) {
				return nil, fmt.Errorf("PGBASE_JS_JOB_SERVICE_SUBJECTS: invalid job subject")
			}
			options.JobServiceSubjects[name] = id
		}
	}
	return options, nil
}
