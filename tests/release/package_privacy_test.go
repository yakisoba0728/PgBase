package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPackagePrivacyGateFailsClosed(t *testing.T) {
	temporary := t.TempDir()
	mockCurl := filepath.Join(temporary, "curl")
	mock := `#!/usr/bin/env bash
set -euo pipefail
output=""
previous=""
for argument in "$@"; do
  if [[ "$argument" == *super-secret* ]]; then exit 90; fi
  if [[ "$previous" == "--output" ]]; then output="$argument"; fi
  previous="$argument"
done
[[ "$*" == *'https://api.github.com/user/packages/container/pgbase'* ]] || exit 91
printf '%s\n' "$MOCK_BODY" > "$output"
printf '%s' "$MOCK_STATUS"
`
	if err := os.WriteFile(mockCurl, []byte(mock), 0o700); err != nil {
		t.Fatal(err)
	}
	script := filepath.Join(repositoryRoot(t), "scripts", "support", "validate-private-package.sh")
	run := func(mode, status, body string) error {
		cmd := exec.Command(script, mode, "yakisoba0728", "PgBase", filepath.Join(temporary, mode+"-evidence.json"))
		cmd.Env = append(os.Environ(),
			"PATH="+temporary+":"+os.Getenv("PATH"),
			"GH_TOKEN=super-secret",
			"MOCK_STATUS="+status,
			"MOCK_BODY="+body,
		)
		return cmd.Run()
	}

	if err := run("before", "404", `{}`); err != nil {
		t.Fatalf("confirmed absence before publication was rejected: %v", err)
	}
	if err := run("after", "200", `{"id":1,"name":"pgbase","package_type":"container","visibility":"private","html_url":"private","owner":{"login":"yakisoba0728"}}`); err != nil {
		t.Fatalf("private package after publication was rejected: %v", err)
	}
	for name, test := range map[string]struct{ mode, status, body string }{
		"authorization failure": {"before", "403", `{}`},
		"missing after push":    {"after", "404", `{}`},
		"public package":        {"after", "200", `{"name":"pgbase","package_type":"container","visibility":"public","owner":{"login":"yakisoba0728"}}`},
		"different owner":       {"after", "200", `{"name":"pgbase","package_type":"container","visibility":"private","owner":{"login":"another-owner"}}`},
	} {
		t.Run(name, func(t *testing.T) {
			if err := run(test.mode, test.status, test.body); err == nil {
				t.Fatal("privacy gate unexpectedly succeeded")
			}
		})
	}

	source, err := os.ReadFile(script)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(source), `--header "Authorization: Bearer $GH_TOKEN"`) {
		t.Fatal("privacy gate exposes its token in the curl argument vector")
	}
}
