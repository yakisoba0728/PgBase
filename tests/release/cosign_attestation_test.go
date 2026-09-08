//go:build cosignintegration

package release

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPrivateReleaseAttestationRejectsWrongTrustAndBinding(t *testing.T) {
	cosign := os.Getenv("PGBASE_COSIGN")
	if cosign == "" {
		var err error
		cosign, err = exec.LookPath("cosign")
		if err != nil {
			t.Fatal("set PGBASE_COSIGN to run the Cosign integration test")
		}
	}
	if _, err := exec.LookPath("docker"); err != nil {
		t.Fatal("docker is required for the Cosign integration test")
	}

	const registryFixture = "registry:2@sha256:a3d8aaa63ed8681a604f1dea0aa03f100d5895b6a58ace528858a7b332415373"
	container := runCommand(t, nil, "docker", "run", "--detach", "--publish", "127.0.0.1::5000", registryFixture)
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "--force", "--volumes", container).Run() })
	port := runCommand(t, nil, "docker", "port", container, "5000/tcp")
	registry := strings.TrimPrefix(strings.TrimSpace(port), "127.0.0.1:")
	image := "127.0.0.1:" + registry + "/pgbase/cosign-test"
	for attempt := 0; attempt < 50; attempt++ {
		if err := exec.Command("curl", "--fail", "--silent", "http://127.0.0.1:"+registry+"/v2/").Run(); err == nil {
			break
		}
		if attempt == 49 {
			t.Fatal("local registry did not become ready")
		}
		time.Sleep(100 * time.Millisecond)
	}
	digest := seedOCIManifest(t, "http://127.0.0.1:"+registry, "pgbase/cosign-test")
	candidate := image + "@" + digest

	temporary := t.TempDir()
	password := "disposable-integration-password"
	keyEnvironment := []string{"COSIGN_PASSWORD=" + password}
	keyPrefix := filepath.Join(temporary, "release")
	wrongKeyPrefix := filepath.Join(temporary, "wrong")
	runCommand(t, keyEnvironment, cosign, "generate-key-pair", "--output-key-prefix", keyPrefix)
	runCommand(t, keyEnvironment, cosign, "generate-key-pair", "--output-key-prefix", wrongKeyPrefix)
	privateKey, err := os.ReadFile(keyPrefix + ".key")
	if err != nil {
		t.Fatal(err)
	}
	predicate := filepath.Join(temporary, "predicate.json")
	if err := os.WriteFile(predicate, []byte("{\"release\":\"expected\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	otherPredicate := filepath.Join(temporary, "other-predicate.json")
	if err := os.WriteFile(otherPredicate, []byte("{\"release\":\"other\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	scriptDirectory := filepath.Join(repositoryRoot(t), "scripts", "support")
	sign := filepath.Join(scriptDirectory, "sign-release-attestation.sh")
	verify := filepath.Join(scriptDirectory, "verify-release-attestation.sh")
	commonEnvironment := []string{
		"PGBASE_COSIGN=" + cosign,
		"PGBASE_COSIGN_ALLOW_INSECURE_REGISTRY=1",
		"EXPECTED_VERSION=v1.2.3",
	}
	signingEnvironment := append(append([]string{}, commonEnvironment...),
		"COSIGN_PASSWORD="+password,
		"COSIGN_PRIVATE_KEY="+string(privateKey),
	)
	predicateType := "https://github.com/webisnoteasy/PgBase/attestations/release-test/v1"
	// Two attestations of one type prove the verifier searches all valid DSSE
	// envelopes instead of rejecting the first non-matching predicate.
	runCommand(t, signingEnvironment, sign, candidate, predicateType, otherPredicate)
	runCommand(t, signingEnvironment, sign, candidate, predicateType, predicate)

	if output, err := command(commonEnvironment, verify, candidate, predicateType, predicate, keyPrefix+".pub", filepath.Join(temporary, "valid.json")).CombinedOutput(); err != nil {
		t.Fatalf("valid attestation failed verification: %v\n%s", err, output)
	}

	for name, fixture := range map[string]string{
		"slsaprovenance1": `{"buildDefinition":{"buildType":"https://example.test/build/v1","externalParameters":{},"internalParameters":{},"resolvedDependencies":[{"uri":"git+https://example.test/repository@refs/tags/v1.2.3","digest":{"gitCommit":"0123456789abcdef0123456789abcdef01234567"}}]},"runDetails":{"builder":{"id":"https://example.test/builder"},"metadata":{"invocationId":"test"}}}`,
		"spdxjson":        `{"SPDXID":"SPDXRef-DOCUMENT","spdxVersion":"SPDX-2.3","name":"fixture","dataLicense":"CC0-1.0","documentNamespace":"https://example.test/spdx/fixture","creationInfo":{"created":"2026-09-08T00:00:00Z","creators":["Tool: fixture"]},"packages":[]}`,
	} {
		t.Run(name+" predicate", func(t *testing.T) {
			path := filepath.Join(temporary, name+".json")
			if err := os.WriteFile(path, []byte(fixture+"\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			runCommand(t, signingEnvironment, sign, candidate, name, path)
			if output, err := command(commonEnvironment, verify, candidate, name, path, keyPrefix+".pub", filepath.Join(temporary, name+"-evidence.json")).CombinedOutput(); err != nil {
				t.Fatalf("%s attestation failed verification: %v\n%s", name, err, output)
			}
		})
	}

	t.Run("wrong public key", func(t *testing.T) {
		assertCommandFails(t, commonEnvironment, verify, candidate, predicateType, predicate, wrongKeyPrefix+".pub", filepath.Join(temporary, "wrong-key.json"))
	})
	t.Run("wrong image digest", func(t *testing.T) {
		last := "0"
		if strings.HasSuffix(digest, "0") {
			last = "1"
		}
		wrongCandidate := strings.TrimSuffix(candidate, digest[len(digest)-1:]) + last
		assertCommandFails(t, commonEnvironment, verify, wrongCandidate, predicateType, predicate, keyPrefix+".pub", filepath.Join(temporary, "wrong-digest.json"))
	})
	t.Run("modified expected predicate", func(t *testing.T) {
		modified := filepath.Join(temporary, "modified.json")
		if err := os.WriteFile(modified, []byte("{\"release\":\"modified\"}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		assertCommandFails(t, commonEnvironment, verify, candidate, predicateType, modified, keyPrefix+".pub", filepath.Join(temporary, "modified.json.out"))
	})
}

func TestPrivateReleaseAttestationCommandReturnsOnlyStdout(t *testing.T) {
	output := runCommand(t, nil, "sh", "-c", `printf 'container-id\n'; printf 'pull progress\n' >&2`)
	if output != "container-id" {
		t.Fatalf("runCommand output = %q, want stdout without stderr diagnostics", output)
	}
}

func assertCommandFails(t *testing.T, environment []string, name string, arguments ...string) {
	t.Helper()
	if output, err := command(environment, name, arguments...).CombinedOutput(); err == nil {
		t.Fatalf("command unexpectedly succeeded:\n%s", output)
	}
}

func runCommand(t *testing.T, environment []string, name string, arguments ...string) string {
	t.Helper()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd := command(environment, name, arguments...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		t.Fatalf("%s failed: %v\nstdout:\n%s\nstderr:\n%s", name, err, stdout.String(), stderr.String())
	}
	return strings.TrimSpace(stdout.String())
}

func command(environment []string, name string, arguments ...string) *exec.Cmd {
	cmd := exec.Command(name, arguments...)
	overrides := make(map[string]struct{}, len(environment))
	for _, value := range environment {
		overrides[strings.SplitN(value, "=", 2)[0]] = struct{}{}
	}
	for _, value := range os.Environ() {
		if _, overridden := overrides[strings.SplitN(value, "=", 2)[0]]; !overridden {
			cmd.Env = append(cmd.Env, value)
		}
	}
	cmd.Env = append(cmd.Env, environment...)
	return cmd
}

func seedOCIManifest(t *testing.T, registryURL, repository string) string {
	t.Helper()
	config := []byte("{}")
	configSum := sha256.Sum256(config)
	configDigest := "sha256:" + hex.EncodeToString(configSum[:])
	upload := registryURL + "/v2/" + repository + "/blobs/uploads/"
	request, err := http.NewRequest(http.MethodPost, upload, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusAccepted {
		t.Fatalf("registry blob upload start status = %s", response.Status)
	}
	location := response.Header.Get("Location")
	if strings.HasPrefix(location, "/") {
		location = registryURL + location
	}
	separator := "?"
	if strings.Contains(location, "?") {
		separator = "&"
	}
	request, err = http.NewRequest(http.MethodPut, location+separator+"digest="+configDigest, bytes.NewReader(config))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/octet-stream")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("registry blob upload completion status = %s", response.Status)
	}
	manifest := []byte(`{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json","config":{"mediaType":"application/vnd.oci.image.config.v1+json","digest":"` + configDigest + `","size":2},"layers":[]}`)
	manifestSum := sha256.Sum256(manifest)
	manifestDigest := "sha256:" + hex.EncodeToString(manifestSum[:])
	request, err = http.NewRequest(http.MethodPut, registryURL+"/v2/"+repository+"/manifests/fixture", bytes.NewReader(manifest))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Content-Type", "application/vnd.oci.image.manifest.v1+json")
	response, err = http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("registry manifest upload status = %s", response.Status)
	}
	return manifestDigest
}
