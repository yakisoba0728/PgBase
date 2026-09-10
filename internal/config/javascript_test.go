package config

import (
	"testing"

	"github.com/google/uuid"
)

func TestLoadJavaScriptEnvParsesCapabilitiesAndSubjects(t *testing.T) {
	subject := uuid.New()
	values := map[string]string{
		"PGBASE_JS_DIR":                  "/srv/application",
		"PGBASE_JS_CAPABILITIES":         "records, files",
		"PGBASE_JS_JOB_SERVICE_SUBJECTS": `{"cleanup":"` + subject.String() + `"}`,
	}
	options, err := LoadJavaScriptEnv(func(name string) (string, bool) { value, ok := values[name]; return value, ok })
	if err != nil {
		t.Fatal(err)
	}
	if options.Directory != "/srv/application" || !options.Capabilities["records"] || !options.Capabilities["files"] || options.JobServiceSubjects["cleanup"] != subject {
		t.Fatalf("JavaScript options = %#v", options)
	}
}

func TestLoadJavaScriptEnvRequiresDirectory(t *testing.T) {
	_, err := LoadJavaScriptEnv(func(name string) (string, bool) {
		if name == "PGBASE_JS_CAPABILITIES" {
			return "records", true
		}
		return "", false
	})
	if err == nil {
		t.Fatal("JavaScript capabilities were accepted without directory")
	}
}
