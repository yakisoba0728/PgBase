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

func previousChannelVersion(row matrixRow) string {
	switch row.channel {
	case "latest":
		return "v1.2.2"
	case "alpha":
		return "v1.2.3-alpha.0"
	case "beta":
		return "v1.2.3-beta.1"
	case "rc":
		return "v1.2.3-rc.3"
	default:
		panic("unsupported release channel " + row.channel)
	}
}

func TestReleaseTagMatrixCreatesTheExactTagAndMovesOneChannel(t *testing.T) {
	for _, row := range releaseTagMatrix() {
		t.Run(row.name, func(t *testing.T) {
			registry := newFakeRegistry(t)
			// Every channel starts at a foreign digest so an unmoved channel is
			// distinguishable from a channel that happened to already match.
			registry.publishVersioned(releaseImage+":"+row.channel, foreignDigest, previousChannelVersion(row))
			for _, label := range row.otherLabels {
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
			registry.publishVersioned(releaseImage+":"+row.channel, foreignDigest, previousChannelVersion(row))
			for _, label := range row.otherLabels {
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
		registry.publishVersioned(channelReference, foreignDigest, "v1.2.2")
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
		registry.publishVersioned(exactReference, candidateDigest, tag)
		registry.publishVersioned(channelReference, candidateDigest, tag)
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
		registry.publishVersioned(exactReference, candidateDigest, tag)
		registry.publishVersioned(channelReference, foreignDigest, "v1.2.2")
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

func TestMovingReleaseChannelsNeverRegress(t *testing.T) {
	tests := []struct{ channel, current, candidate string }{
		{channel: "latest", current: "v1.2.4", candidate: "v1.2.3"},
		{channel: "alpha", current: "v2.0.0-alpha.10", candidate: "v2.0.0-alpha.9"},
		{channel: "beta", current: "v2.0.0-beta.10", candidate: "v2.0.0-beta.9"},
		{channel: "rc", current: "v2.0.0-rc.10", candidate: "v2.0.0-rc.9"},
	}
	for _, test := range tests {
		t.Run(test.channel, func(t *testing.T) {
			registry := newFakeRegistry(t)
			registry.publishVersioned(releaseImage+":"+test.channel, foreignDigest, test.current)

			output, err := registry.run("promote-release-image.sh",
				releaseImage, test.candidate, releaseImage+"@"+candidateDigest, test.candidate)
			if err == nil {
				t.Fatalf("older release regressed %s:\n%s", test.channel, output)
			}
			if mutations := registry.mutations(); len(mutations) != 0 {
				t.Fatalf("rejected regression still mutated %v", mutations)
			}
			if !strings.Contains(output, "channel_version_before="+test.current) {
				t.Fatalf("rejected regression omitted current version evidence:\n%s", output)
			}
		})
	}
}

func TestMovingReleaseChannelsCompareCanonicalVersionsWithoutIntegerBounds(t *testing.T) {
	tests := []struct{ channel, current, candidate string }{
		{
			channel:   "latest",
			current:   "v999999999999999999999999999999999999999.8.7",
			candidate: "v1000000000000000000000000000000000000000.0.0",
		},
		{
			channel:   "alpha",
			current:   "v2.0.0-alpha.999999999999999999999999999999999999999",
			candidate: "v2.0.0-alpha.1000000000000000000000000000000000000000",
		},
	}
	for _, test := range tests {
		t.Run(test.channel, func(t *testing.T) {
			registry := newFakeRegistry(t)
			registry.publishVersioned(releaseImage+":"+test.channel, foreignDigest, test.current)

			output, err := registry.run("promote-release-image.sh",
				releaseImage, test.candidate, releaseImage+"@"+candidateDigest, test.candidate)
			if err != nil {
				t.Fatalf("newer unbounded release was rejected: %v\n%s", err, output)
			}
			assertMutations(t, registry, map[string]string{
				releaseImage + ":" + test.candidate: candidateDigest,
				releaseImage + ":" + test.channel:   candidateDigest,
			})
		})
	}
}

func TestEqualMovingChannelVersionWithDifferentDigestFailsBeforeMutation(t *testing.T) {
	registry := newFakeRegistry(t)
	registry.publishVersioned(releaseImage+":latest", foreignDigest, "v1.2.3")

	output, err := registry.run("promote-release-image.sh",
		releaseImage, "v1.2.3", releaseImage+"@"+candidateDigest, "v1.2.3")
	if err == nil {
		t.Fatalf("equal version with a different digest was accepted:\n%s", output)
	}
	if mutations := registry.mutations(); len(mutations) != 0 {
		t.Fatalf("equal-version conflict mutated %v", mutations)
	}
}

func TestLegacyUnlabelledChannelRequiresAnExactTagAnchoredBootstrap(t *testing.T) {
	const currentVersion = "v1.2.2"
	channelReference := releaseImage + ":latest"

	t.Run("missing explicit version fails closed", func(t *testing.T) {
		registry := newFakeRegistry(t)
		registry.publish(channelReference, foreignDigest)
		output, err := registry.run("promote-release-image.sh",
			releaseImage, "v1.2.3", releaseImage+"@"+candidateDigest, "v1.2.3")
		if err == nil {
			t.Fatalf("unlabelled channel was accepted without a bootstrap version:\n%s", output)
		}
		if mutations := registry.mutations(); len(mutations) != 0 {
			t.Fatalf("missing bootstrap version mutated %v", mutations)
		}
	})

	t.Run("unanchored version fails closed", func(t *testing.T) {
		registry := newFakeRegistry(t)
		registry.publish(channelReference, foreignDigest)
		output, err := registry.run("promote-release-image.sh",
			releaseImage, "v1.2.3", releaseImage+"@"+candidateDigest, "v1.2.3", currentVersion)
		if err == nil {
			t.Fatalf("unanchored bootstrap version was accepted:\n%s", output)
		}
		if mutations := registry.mutations(); len(mutations) != 0 {
			t.Fatalf("unanchored bootstrap version mutated %v", mutations)
		}
	})

	t.Run("wrong exact-tag digest fails closed", func(t *testing.T) {
		registry := newFakeRegistry(t)
		registry.publish(channelReference, foreignDigest)
		registry.publish(releaseImage+":"+currentVersion, digest("ef"))
		output, err := registry.run("promote-release-image.sh",
			releaseImage, "v1.2.3", releaseImage+"@"+candidateDigest, "v1.2.3", currentVersion)
		if err == nil {
			t.Fatalf("wrong immutable anchor was accepted:\n%s", output)
		}
		if mutations := registry.mutations(); len(mutations) != 0 {
			t.Fatalf("wrong immutable anchor mutated %v", mutations)
		}
	})

	t.Run("matching immutable exact tag permits one-time bootstrap", func(t *testing.T) {
		registry := newFakeRegistry(t)
		registry.publish(channelReference, foreignDigest)
		registry.publish(releaseImage+":"+currentVersion, foreignDigest)
		output, err := registry.run("promote-release-image.sh",
			releaseImage, "v1.2.3", releaseImage+"@"+candidateDigest, "v1.2.3", currentVersion)
		if err != nil {
			t.Fatalf("anchored legacy bootstrap failed: %v\n%s", err, output)
		}
		assertMutations(t, registry, map[string]string{
			releaseImage + ":v1.2.3": candidateDigest,
			channelReference:         candidateDigest,
		})
		if !strings.Contains(output, "channel_version_source=legacy-exact-tag") {
			t.Fatalf("bootstrap audit omitted its evidence source:\n%s", output)
		}
	})
}

func TestMovingChannelRejectsMalformedAndCrossChannelLabelsBeforeMutation(t *testing.T) {
	for _, version := range []string{"v01.2.3", "v1.2.3-rc.1"} {
		t.Run(version, func(t *testing.T) {
			registry := newFakeRegistry(t)
			registry.publishVersioned(releaseImage+":latest", foreignDigest, version)
			output, err := registry.run("promote-release-image.sh",
				releaseImage, "v1.2.4", releaseImage+"@"+candidateDigest, "v1.2.4")
			if err == nil {
				t.Fatalf("invalid current label %q was accepted:\n%s", version, output)
			}
			if mutations := registry.mutations(); len(mutations) != 0 {
				t.Fatalf("invalid current label %q mutated %v", version, mutations)
			}
		})
	}
}

func TestPresentMalformedLabelCannotUseTheLegacyBootstrapEscapeHatch(t *testing.T) {
	for _, malformed := range []string{"", "absent", "v1.2.2\n", "v1.2.2\x00"} {
		t.Run("label="+malformed, func(t *testing.T) {
			registry := newFakeRegistry(t)
			registry.publishVersioned(releaseImage+":latest", foreignDigest, malformed)
			registry.publish(releaseImage+":v1.2.2", foreignDigest)

			output, err := registry.run("promote-release-image.sh",
				releaseImage, "v1.2.3", releaseImage+"@"+candidateDigest, "v1.2.3", "v1.2.2")
			if err == nil {
				t.Fatalf("present malformed label %q used the legacy bootstrap:\n%s", malformed, output)
			}
			if mutations := registry.mutations(); len(mutations) != 0 {
				t.Fatalf("present malformed label %q mutated %v", malformed, mutations)
			}
		})
	}
}

func TestPromotionRejectsCandidateVersionMismatchBeforeMutation(t *testing.T) {
	registry := newFakeRegistry(t)
	registry.candidateVersion = "v1.2.2"
	output, err := registry.run("promote-release-image.sh",
		releaseImage, "v1.2.3", releaseImage+"@"+candidateDigest, "v1.2.3")
	if err == nil {
		t.Fatalf("mislabeled candidate was accepted:\n%s", output)
	}
	if mutations := registry.mutations(); len(mutations) != 0 {
		t.Fatalf("mislabeled candidate mutated %v", mutations)
	}
}

func TestPromotionReadbackRequiresVersionAsWellAsDigest(t *testing.T) {
	registry := newFakeRegistry(t)
	registry.dropCreatedVersion = true
	output, err := registry.run("promote-release-image.sh",
		releaseImage, "v1.2.3", releaseImage+"@"+candidateDigest, "v1.2.3")
	if err == nil {
		t.Fatalf("digest-only readback was accepted:\n%s", output)
	}
	if !strings.Contains(output, "promotion readback did not preserve version") {
		t.Fatalf("version readback failure was not diagnosed:\n%s", output)
	}
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
		"candidate_version=",
		"channel=" + channel,
		"channel_before=",
		"channel_version_before=",
		"exact_after=" + candidateDigest,
		"channel_after=" + candidateDigest,
		"exact_version_after=",
		"channel_version_after=",
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
