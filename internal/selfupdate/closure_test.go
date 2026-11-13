package selfupdate

import (
	"strings"
	"testing"
)

func TestHyphenatedPrereleaseVersion(t *testing.T) {
	for _, raw := range []string{"v1.2.3-rc-1", "v1.2.3-nightly-20261113"} {
		if !validVersion(raw) {
			t.Errorf("valid prerelease rejected: %s", raw)
		}
	}
}

func TestAbortedJournalWithoutCandidateDigest(t *testing.T) {
	entry := journal{SchemaVersion: 1, Phase: "aborted", Target: "pgbase", Candidate: ".pgbase.pgbase-candidate-123456789abc", Backup: "backup-123456789abc", Old: Build{Version: "v1.0.0", Commit: strings.Repeat("a", 40), GOOS: "linux", GOARCH: "amd64"}, New: Build{Version: "v1.0.1", Commit: strings.Repeat("b", 40), GOOS: "linux", GOARCH: "amd64"}, OldSHA256: strings.Repeat("a", 64), ManifestSHA256: strings.Repeat("b", 64), SignatureSHA256: strings.Repeat("c", 64), TrustFingerprint: strings.Repeat("d", 64)}
	if !validJournal(entry) {
		t.Fatal("aborted download journal rejected")
	}
}
