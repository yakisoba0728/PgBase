package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"
)

type cliGolden struct {
	Cases []cliGoldenCase `json:"cases"`
}

type cliGoldenCase struct {
	Name   string `json:"name"`
	Exit   int    `json:"exit"`
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
}

func TestCLIOutputGolden(t *testing.T) {
	const secret = "postgres://admin:must-not-leak@database/private?sslpassword=also-secret"
	tests := []struct {
		name     string
		args     []string
		commands CommandSet
	}{
		{name: "ordinary help", commands: DefaultCommands()},
		{name: "unknown command", args: []string{"unknown"}, commands: DefaultCommands()},
		{name: "invalid configuration", args: []string{"migrate", "status"}, commands: DefaultCommands()},
		{
			name: "successful command streams",
			args: []string{"migrate", "status"},
			commands: CommandSet{Migrate: func(_ context.Context, _ []string, _ func(string) (string, bool), stdout, stderr io.Writer) error {
				if _, err := io.WriteString(stdout, "system migrations: current=3 latest=3 dirty=false\n"); err != nil {
					return err
				}
				_, err := io.WriteString(stderr, "operator notice\n")
				return err
			}},
		},
		{
			name: "operational failure redaction",
			args: []string{"migrate", "status"},
			commands: CommandSet{Migrate: func(_ context.Context, _ []string, _ func(string) (string, bool), stdout, _ io.Writer) error {
				if _, err := io.WriteString(stdout, "partial result\n"); err != nil {
					return err
				}
				return errors.New(secret + " SQLSTATE 42P01 select password_hash")
			}},
		},
	}

	golden := cliGolden{Cases: make([]cliGoldenCase, 0, len(tests))}
	for _, test := range tests {
		var stdout, stderr bytes.Buffer
		exit := Run(context.Background(), test.args, emptyEnv, &stdout, &stderr, test.commands)
		golden.Cases = append(golden.Cases, cliGoldenCase{
			Name: test.name, Exit: exit, Stdout: stdout.String(), Stderr: stderr.String(),
		})
	}

	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(golden); err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile("testdata/cli_output.golden.json")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(encoded.Bytes(), want) {
		t.Fatalf("CLI golden mismatch\n--- got ---\n%s--- want ---\n%s", encoded.Bytes(), want)
	}
	if strings.Contains(encoded.String(), secret) || strings.Contains(encoded.String(), "also-secret") || strings.Contains(encoded.String(), "password_hash") {
		t.Fatalf("CLI golden leaked an operational error: %s", encoded.Bytes())
	}
}
