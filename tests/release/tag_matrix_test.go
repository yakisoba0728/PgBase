package release

import (
	"sort"
	"strings"
	"testing"
)

const releaseImage = "ghcr.io/webisnoteasy/pgbase"

var (
	candidateDigest = digest("ab")
	foreignDigest   = digest("cd")
)

// The plan's release tag matrix is a promotion contract, not documentation:
// every row fixes which exact tag is created, which single channel may move,
// and which channels must observe zero mutations.
type matrixRow struct {
	name        string
	tag         string
	channel     string
	otherLabels []string
}

func releaseTagMatrix() []matrixRow {
	return []matrixRow{
		{name: "stable moves only latest", tag: "v1.2.3", channel: "latest", otherLabels: []string{"alpha", "beta", "rc"}},
		{name: "alpha moves only alpha", tag: "v1.2.3-alpha.1", channel: "alpha", otherLabels: []string{"latest", "beta", "rc"}},
		{name: "beta moves only beta", tag: "v1.2.3-beta.2", channel: "beta", otherLabels: []string{"latest", "alpha", "rc"}},
		{name: "rc moves only rc", tag: "v1.2.3-rc.4", channel: "rc", otherLabels: []string{"latest", "alpha", "beta"}},
	}
}

func TestReleaseTagMatrixCreatesTheExactTagAndMovesOneChannel(t *testing.T) {
	for _, row := range releaseTagMatrix() {
		t.Run(row.name, func(t *testing.T) {
			registry := newFakeRegistry(t)
			// Every channel starts at a foreign digest so an unmoved channel is
			// distinguishable from a channel that happened to already match.
			for _, label := range append([]string{row.channel}, row.otherLabels...) {
				registry.publish(releaseImage+":"+label, foreignDigest)
			}
			output, err := registry.run("promote-release-image.sh",
				releaseImage, row.tag, releaseImage+"@"+candidateDigest, row.tag)
			if err != nil {
				t.Fatalf("promotion failed: %v\n%s", err, output)
			}
			assertMutations(t, registry, map[string]string{
				releaseImage + ":" + row.tag:     candidateDigest,
				releaseImage + ":" + row.channel: candidateDigest,
			})
			assertAuditRecordsDigests(t, output, row.channel)
		})
	}
}

func TestReleaseTagMatrixLeavesEveryOtherChannelUntouched(t *testing.T) {
	for _, row := range releaseTagMatrix() {
		t.Run(row.name, func(t *testing.T) {
			registry := newFakeRegistry(t)
			for _, label := range append([]string{row.channel}, row.otherLabels...) {
				registry.publish(releaseImage+":"+label, foreignDigest)
			}
			if output, err := registry.run("promote-release-image.sh",
				releaseImage, row.tag, releaseImage+"@"+candidateDigest, row.tag); err != nil {
				t.Fatalf("promotion failed: %v\n%s", err, output)
			}
			for _, label := range row.otherLabels {
				reference := releaseImage + ":" + label
				for _, mutation := range registry.mutations() {
					if mutation.reference == reference {
						t.Errorf("%s tag mutated %s", row.tag, reference)
					}
				}
				if registry.inspected(reference) {
					t.Errorf("%s tag read the unrelated channel %s", row.tag, reference)
				}
			}
		})
	}
}

func TestExactVersionTagAbsentSameAndDifferentDigest(t *testing.T) {
	const tag = "v1.2.3"
	exactReference := releaseImage + ":" + tag
	channelReference := releaseImage + ":latest"

	t.Run("absent creates the exact tag and moves the channel", func(t *testing.T) {
		registry := newFakeRegistry(t)
		registry.publish(channelReference, foreignDigest)
		output, err := registry.run("promote-release-image.sh", releaseImage, tag, releaseImage+"@"+candidateDigest, tag)
		if err != nil {
			t.Fatalf("promotion failed: %v\n%s", err, output)
		}
		assertMutations(t, registry, map[string]string{
			exactReference:   candidateDigest,
			channelReference: candidateDigest,
		})
		if !strings.Contains(output, "exact_before=absent") {
			t.Errorf("audit did not record the absent exact tag:\n%s", output)
		}
	})

	t.Run("same digest is an idempotent no-op on both references", func(t *testing.T) {
		registry := newFakeRegistry(t)
		registry.publish(exactReference, candidateDigest)
		registry.publish(channelReference, candidateDigest)
		output, err := registry.run("promote-release-image.sh", releaseImage, tag, releaseImage+"@"+candidateDigest, tag)
		if err != nil {
			t.Fatalf("promotion failed: %v\n%s", err, output)
		}
		if mutations := registry.mutations(); len(mutations) != 0 {
			t.Errorf("re-running promotion mutated %v", mutations)
		}
	})

	t.Run("same exact digest still moves a stale channel", func(t *testing.T) {
		registry := newFakeRegistry(t)
		registry.publish(exactReference, candidateDigest)
		registry.publish(channelReference, foreignDigest)
		output, err := registry.run("promote-release-image.sh", releaseImage, tag, releaseImage+"@"+candidateDigest, tag)
		if err != nil {
			t.Fatalf("promotion failed: %v\n%s", err, output)
		}
		assertMutations(t, registry, map[string]string{channelReference: candidateDigest})
	})

	t.Run("different digest fails without touching any channel", func(t *testing.T) {
		registry := newFakeRegistry(t)
		registry.publish(exactReference, foreignDigest)
		registry.publish(channelReference, foreignDigest)
		output, err := registry.run("promote-release-image.sh", releaseImage, tag, releaseImage+"@"+candidateDigest, tag)
		if err == nil {
			t.Fatalf("promotion accepted a conflicting immutable tag:\n%s", output)
		}
		if mutations := registry.mutations(); len(mutations) != 0 {
			t.Errorf("conflicting immutable tag still mutated %v", mutations)
		}
		if registry.inspected(channelReference) {
			t.Error("conflicting immutable tag was detected only after reading the channel")
		}
		if !strings.Contains(output, "immutable exact tag") {
			t.Errorf("conflict did not name the immutable exact tag:\n%s", output)
		}
	})
}

func TestUnsupportedReleaseInputIsRejectedBeforeAnyRegistryCall(t *testing.T) {
	unsupported := []string{
		"1.2.3",            // no v prefix
		"v1.2",             // not three components
		"v01.2.3",          // leading zero
		"v1.2.3+build.5",   // build metadata
		"v1.2.3-rc1",       // no prerelease separator
		"v1.2.3-alpha",     // no prerelease ordinal
		"v1.2.3-alpha.01",  // leading zero ordinal
		"v1.2.3-nightly.1", // unsupported channel
		"latest",
		"",
	}
	for _, tag := range unsupported {
		t.Run("tag="+tag, func(t *testing.T) {
			registry := newFakeRegistry(t)
			if output, err := registry.run("release-tag-policy.sh", tag, tag); err == nil {
				t.Fatalf("tag policy accepted %q:\n%s", tag, output)
			}
			if output, err := registry.run("promote-release-image.sh",
				releaseImage, tag, releaseImage+"@"+candidateDigest, tag); err == nil {
				t.Fatalf("promotion accepted %q:\n%s", tag, output)
			}
			if events := registry.events(); len(events) != 0 {
				t.Errorf("rejected input still reached the registry: %v", events)
			}
		})
	}
}

func TestPromotionRequiresTheTagToMatchTheCandidateVersionAndDigest(t *testing.T) {
	tests := []struct {
		name            string
		tag             string
		candidateImage  string
		expectedVersion string
	}{
		{name: "tag disagrees with expected version", tag: "v1.2.3", candidateImage: releaseImage + "@" + candidateDigest, expectedVersion: "v1.2.4"},
		{name: "candidate carries a mutable tag", tag: "v1.2.3", candidateImage: releaseImage + ":v1.2.3", expectedVersion: "v1.2.3"},
		{name: "candidate belongs to another repository", tag: "v1.2.3", candidateImage: "ghcr.io/other/pgbase@" + candidateDigest, expectedVersion: "v1.2.3"},
		{name: "candidate digest is truncated", tag: "v1.2.3", candidateImage: releaseImage + "@sha256:abc", expectedVersion: "v1.2.3"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			registry := newFakeRegistry(t)
			output, err := registry.run("promote-release-image.sh",
				releaseImage, test.tag, test.candidateImage, test.expectedVersion)
			if err == nil {
				t.Fatalf("promotion accepted the candidate:\n%s", output)
			}
			if events := registry.events(); len(events) != 0 {
				t.Errorf("rejected candidate still reached the registry: %v", events)
			}
		})
	}
}

func TestCandidateValidationAcceptsOnlyDigestPinnedCanonicalReleases(t *testing.T) {
	registry := newFakeRegistry(t)
	for _, version := range []string{"v1.2.3", "v1.2.3-alpha.1", "v1.2.3-beta.2", "v1.2.3-rc.4"} {
		if output, err := registry.run("validate-release-candidate.sh", releaseImage+"@"+candidateDigest, version); err != nil {
			t.Errorf("validation rejected %s: %v\n%s", version, err, output)
		}
	}
	rejected := []struct{ image, version string }{
		{releaseImage + ":latest", "v1.2.3"},
		{releaseImage, "v1.2.3"},
		{releaseImage + "@sha256:NOTHEX" + strings.Repeat("0", 58), "v1.2.3"},
		{releaseImage + "@" + candidateDigest, "1.2.3"},
		{releaseImage + "@" + candidateDigest, "v1.2.3-nightly.1"},
		{"", "v1.2.3"},
	}
	for _, test := range rejected {
		if output, err := registry.run("validate-release-candidate.sh", test.image, test.version); err == nil {
			t.Errorf("validation accepted image %q version %q:\n%s", test.image, test.version, output)
		}
	}
}

func assertMutations(t *testing.T, registry *fakeRegistry, want map[string]string) {
	t.Helper()
	got := make(map[string]string, len(want))
	for _, mutation := range registry.mutations() {
		if previous, seen := got[mutation.reference]; seen {
			t.Errorf("reference %s was mutated twice (%s then %s)", mutation.reference, previous, mutation.digest)
		}
		got[mutation.reference] = mutation.digest
	}
	for reference, wantDigest := range want {
		if got[reference] != wantDigest {
			t.Errorf("mutation for %s = %q, want %q", reference, got[reference], wantDigest)
		}
	}
	for reference := range got {
		if _, expected := want[reference]; !expected {
			t.Errorf("unexpected mutation of %s", reference)
		}
	}
	if len(got) != len(want) {
		t.Errorf("mutated references = %v, want %v", sortedKeys(got), sortedKeys(want))
	}
}

func assertAuditRecordsDigests(t *testing.T, output, channel string) {
	t.Helper()
	for _, field := range []string{
		"exact_before=",
		"channel=" + channel,
		"channel_before=",
		"exact_after=" + candidateDigest,
		"channel_after=" + candidateDigest,
	} {
		if !strings.Contains(output, field) {
			t.Errorf("promotion audit is missing %q:\n%s", field, output)
		}
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
