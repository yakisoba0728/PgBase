//go:build e2e

package e2e

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
)

var (
	e2eDigestReference  = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
	e2eCanonicalVersion = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(alpha|beta|rc)\.(0|[1-9][0-9]*))?$`)
)

// resolveE2ECandidate decides whether the suite runs a published release
// candidate or builds from source. Release mode has to be all or nothing: a
// half-configured candidate that silently fell back to `compose up --build`
// would report a green Docker E2E for an image the release never publishes.
func resolveE2ECandidate(image, version string) (release bool, err error) {
	if image == "" && version == "" {
		return false, nil
	}
	if image == "" || version == "" {
		return false, errors.New("PGBASE_E2E_CANDIDATE_IMAGE and PGBASE_E2E_EXPECTED_VERSION must be set together")
	}
	if !e2eDigestReference.MatchString(image) {
		return false, fmt.Errorf("PGBASE_E2E_CANDIDATE_IMAGE %q must be an image@sha256 digest reference without a mutable tag", image)
	}
	name := image[:strings.LastIndex(image, "@")]
	if strings.Contains(name[strings.LastIndex(name, "/")+1:], ":") {
		return false, fmt.Errorf("PGBASE_E2E_CANDIDATE_IMAGE %q must be an image@sha256 digest reference without a mutable tag", image)
	}
	if !e2eCanonicalVersion.MatchString(version) {
		return false, fmt.Errorf("PGBASE_E2E_EXPECTED_VERSION %q is not a supported canonical release version", version)
	}
	return true, nil
}

func TestReleaseModeRefusesAnythingButADigestPinnedCandidate(t *testing.T) {
	digest := "sha256:" + strings.Repeat("ab", 32)
	tests := []struct {
		name        string
		image       string
		version     string
		wantRelease bool
		wantError   bool
	}{
		{name: "local mode sets neither variable", wantRelease: false},
		{name: "image without version", image: "ghcr.io/webisnoteasy/pgbase@" + digest, wantError: true},
		{name: "version without image", version: "v1.2.3", wantError: true},
		{name: "mutable tag", image: "ghcr.io/webisnoteasy/pgbase:v1.2.3", version: "v1.2.3", wantError: true},
		{name: "tag and digest", image: "ghcr.io/webisnoteasy/pgbase:v1.2.3@" + digest, version: "v1.2.3", wantError: true},
		{name: "bare repository", image: "ghcr.io/webisnoteasy/pgbase", version: "v1.2.3", wantError: true},
		{name: "truncated digest", image: "ghcr.io/webisnoteasy/pgbase@sha256:abc", version: "v1.2.3", wantError: true},
		{name: "uncanonical version", image: "ghcr.io/webisnoteasy/pgbase@" + digest, version: "1.2.3", wantError: true},
		{name: "unsupported channel", image: "ghcr.io/webisnoteasy/pgbase@" + digest, version: "v1.2.3-nightly.1", wantError: true},
		{name: "digest pinned release", image: "ghcr.io/webisnoteasy/pgbase@" + digest, version: "v1.2.3-rc.1", wantRelease: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			release, err := resolveE2ECandidate(test.image, test.version)
			if test.wantError {
				if err == nil {
					t.Fatalf("release mode accepted image %q version %q", test.image, test.version)
				}
				if release {
					t.Error("a rejected candidate must not select release mode")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected refusal: %v", err)
			}
			if release != test.wantRelease {
				t.Fatalf("release = %v, want %v", release, test.wantRelease)
			}
		})
	}
}

// A release candidate must never be reached through a source build or a local
// tag, so the suite's own startup path is checked for the fallback it forbids.
func TestReleaseModeStartupNeverBuildsFromSource(t *testing.T) {
	if suite.release {
		if suite.image == "" || !e2eDigestReference.MatchString(suite.image) {
			t.Fatalf("release suite image = %q, want a digest reference", suite.image)
		}
		if suite.version == "e2e-test" {
			t.Fatal("release suite kept the local development version")
		}
		return
	}
	if e2eDigestReference.MatchString(suite.image) {
		t.Fatalf("local suite image %q is digest pinned but release mode is off", suite.image)
	}
}
