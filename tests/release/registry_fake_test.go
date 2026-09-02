package release

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The promotion primitive is only meaningful against a registry that answers
// read-backs and records mutations, so the matrix drives the real script
// against a scripted `docker` instead of asserting on its source text.
const fakeDockerProgram = `#!/usr/bin/env bash
set -uo pipefail

state="$PGBASE_FAKE_REGISTRY_STATE"
log="$PGBASE_FAKE_REGISTRY_LOG"

reference_key() {
	printf '%s' "$1" | tr '/:@' '___'
}

if [[ "${1:-}" != "buildx" || "${2:-}" != "imagetools" ]]; then
	echo "fake docker received an unsupported command: $*" >&2
	exit 64
fi

case "${3:-}" in
inspect)
	reference="$4"
	printf 'inspect\t%s\n' "$reference" >>"$log"
	entry="$state/$(reference_key "$reference")"
	if [[ ! -f "$entry" ]]; then
		echo "ERROR: $reference: not found" >&2
		exit 1
	fi
	printf '{"digest":"%s","mediaType":"application/vnd.oci.image.manifest.v1+json"}\n' "$(cat "$entry")"
	exit 0
	;;
create)
	shift 3
	target=""
	source_reference=""
	while (($# > 0)); do
		case "$1" in
		--tag)
			target="$2"
			shift 2
			;;
		--prefer-index=false)
			shift
			;;
		--*)
			echo "fake docker received an unsupported imagetools create flag: $1" >&2
			exit 64
			;;
		*)
			source_reference="$1"
			shift
			;;
		esac
	done
	if [[ -z "$target" || -z "$source_reference" ]]; then
		echo "fake docker imagetools create needs a target and a source" >&2
		exit 64
	fi
	digest="${source_reference##*@}"
	printf 'create\t%s\t%s\n' "$target" "$digest" >>"$log"
	printf '%s' "$digest" >"$state/$(reference_key "$target")"
	exit 0
	;;
*)
	echo "fake docker received an unsupported imagetools subcommand: ${3:-}" >&2
	exit 64
	;;
esac
`

type registryEvent struct {
	action    string
	reference string
	digest    string
}

type fakeRegistry struct {
	t         *testing.T
	stateDir  string
	logPath   string
	pathValue string
}

func newFakeRegistry(t *testing.T) *fakeRegistry {
	t.Helper()
	root := t.TempDir()
	binDir := filepath.Join(root, "bin")
	stateDir := filepath.Join(root, "state")
	for _, dir := range []string{binDir, stateDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(binDir, "docker"), []byte(fakeDockerProgram), 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(root, "registry.log")
	if err := os.WriteFile(logPath, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return &fakeRegistry{
		t: t, stateDir: stateDir, logPath: logPath,
		pathValue: binDir + string(os.PathListSeparator) + os.Getenv("PATH"),
	}
}

func (r *fakeRegistry) publish(reference, digest string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.stateDir, referenceKey(reference)), []byte(digest), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *fakeRegistry) events() []registryEvent {
	r.t.Helper()
	raw, err := os.ReadFile(r.logPath)
	if err != nil {
		r.t.Fatal(err)
	}
	var events []registryEvent
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			r.t.Fatalf("malformed fake registry log line %q", line)
		}
		event := registryEvent{action: fields[0], reference: fields[1]}
		if len(fields) > 2 {
			event.digest = fields[2]
		}
		events = append(events, event)
	}
	return events
}

func (r *fakeRegistry) mutations() []registryEvent {
	r.t.Helper()
	mutations := make([]registryEvent, 0, 2)
	for _, event := range r.events() {
		if event.action == "create" {
			mutations = append(mutations, event)
		}
	}
	return mutations
}

func (r *fakeRegistry) inspected(reference string) bool {
	r.t.Helper()
	for _, event := range r.events() {
		if event.action == "inspect" && event.reference == reference {
			return true
		}
	}
	return false
}

func (r *fakeRegistry) run(script string, arguments ...string) (string, error) {
	r.t.Helper()
	command := exec.Command(filepath.Join(repositoryRoot(r.t), "scripts", "support", script), arguments...)
	command.Env = append(os.Environ(),
		"PATH="+r.pathValue,
		"PGBASE_FAKE_REGISTRY_STATE="+r.stateDir,
		"PGBASE_FAKE_REGISTRY_LOG="+r.logPath,
	)
	output, err := command.CombinedOutput()
	return string(output), err
}

func referenceKey(reference string) string {
	return strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(reference)
}

func repositoryRoot(t *testing.T) string {
	t.Helper()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(currentFile), "..", ".."))
}

func digest(marker string) string {
	return "sha256:" + strings.Repeat(marker, 64/len(marker))
}
