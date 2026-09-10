package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/selfupdate"
)

func TestProductionCommandsComposeVersionAndUpdate(t *testing.T) {
	commands := productionCommands()
	if commands["version"] == nil || commands["update"] == nil {
		t.Fatalf("production commands omit N9: %#v", commands)
	}

	var stdout, stderr bytes.Buffer
	if code := Execute(context.Background(), []string{"version", "--json"}, emptyEnv, testIO(nil, &stdout, &stderr)); code != 0 {
		t.Fatalf("version exit=%d stderr=%q", code, stderr.String())
	}
	if stderr.Len() != 0 || !json.Valid(stdout.Bytes()) {
		t.Fatalf("version stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
}

func TestExecuteRendersStableRedactedUpdateError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := execute(context.Background(), []string{"update"}, emptyEnv, testIO(nil, &stdout, &stderr), commandTable{
		"update": func(context.Context, []string, environment, IO) error {
			return updateFailure(selfupdate.ErrUntrustedManifest)
		},
	})
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
	var envelope struct {
		SchemaVersion int `json:"schemaVersion"`
		Error         struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &envelope); err != nil {
		t.Fatalf("stdout JSON=%q: %v", stdout.String(), err)
	}
	if envelope.SchemaVersion != 1 || envelope.Error.Code != "untrusted_manifest" || envelope.Error.Message != "update command failed" {
		t.Fatalf("stdout envelope=%+v", envelope)
	}

	if code := execute(context.Background(), []string{"update"}, emptyEnv, testIO(nil, io.Discard, io.Discard), commandTable{
		"update": func(context.Context, []string, environment, IO) error {
			return configurationError{err: selfupdate.ErrInvalidInput, message: updateUsage}
		},
	}); code != 2 {
		t.Fatalf("malformed update exit=%d", code)
	}
}
