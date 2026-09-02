package release

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

type releasePhaseManifest struct {
	SchemaVersion int              `json:"schemaVersion"`
	Rollouts      []releaseRollout `json:"rollouts"`
}

type releaseRollout struct {
	Name        string            `json:"name"`
	Migration   int               `json:"migration"`
	Predecessor *releaseOperator  `json:"predecessor,omitempty"`
	Operator    *releaseOperator  `json:"operator,omitempty"`
	Artifacts   []releaseArtifact `json:"artifacts"`
}

type releaseOperator struct {
	Commit  string `json:"commit"`
	Version string `json:"version"`
}

type releaseArtifact struct {
	Phase                    string `json:"phase"`
	Commit                   string `json:"commit"`
	Version                  string `json:"version"`
	WriterVersion            *int   `json:"writerVersion,omitempty"`
	IssuesCursor             *int   `json:"issuesCursor,omitempty"`
	DecodesCursors           []int  `json:"decodesCursors,omitempty"`
	SchemaCompilerGeneration *int   `json:"schemaCompilerGeneration,omitempty"`
	AcceptedMigrations       []int  `json:"acceptedMigrations,omitempty"`
	CompanionEnabled         *bool  `json:"companionEnabled,omitempty"`
}

func TestMembershipRolloutManifestPinsFourRealReleaseArtifacts(t *testing.T) {
	manifest := readReleasePhaseManifest(t)
	if manifest.SchemaVersion != 1 {
		t.Fatalf("release phase manifest schema = %d, want 1", manifest.SchemaVersion)
	}
	var membership releaseRollout
	for _, rollout := range manifest.Rollouts {
		if rollout.Name == "record-membership-v7" {
			membership = rollout
			break
		}
	}
	if membership.Name == "" || membership.Migration != 7 {
		t.Fatalf("record membership rollout = %#v, want migration 7", membership)
	}
	wantOperator := &releaseOperator{
		Commit: "f7ec0ff54953417dd0b41331f0b9d02b023195d8", Version: "v0.0.7-rc.1",
	}
	if !reflect.DeepEqual(membership.Operator, wantOperator) {
		t.Fatalf("record membership operator = %#v, want %#v", membership.Operator, wantOperator)
	}
	want := []releaseArtifact{
		{Phase: "A0", Commit: "9672d695c315dbcf36f993cec78eebe9fa5faac9", Version: "v0.0.7-alpha.0", WriterVersion: integer(0), IssuesCursor: integer(4), DecodesCursors: []int{4, 5}},
		{Phase: "A", Commit: "451e715938e9d58f50970080d34142776ef256b2", Version: "v0.0.7-alpha.1", WriterVersion: integer(1), IssuesCursor: integer(4), DecodesCursors: []int{4, 5}},
		{Phase: "B", Commit: "a554198b3faa474fd481f7bf01e232eb7d19db54", Version: "v0.0.7-beta.0", WriterVersion: integer(2), IssuesCursor: integer(5), DecodesCursors: []int{4, 5}},
		{Phase: "C", Commit: "bd7f75c62294a6b319971279f3d3c273fd5834c2", Version: "v0.0.7-rc.0", WriterVersion: integer(3), IssuesCursor: integer(5), DecodesCursors: []int{5}},
	}
	if !reflect.DeepEqual(membership.Artifacts, want) {
		t.Fatalf("record membership artifacts = %#v, want %#v", membership.Artifacts, want)
	}
}

func readReleasePhaseManifest(t *testing.T) releasePhaseManifest {
	t.Helper()
	contents, err := os.ReadFile(filepath.Join("..", "..", "deployments", "release-phases.json"))
	if err != nil {
		t.Fatalf("read release phase manifest: %v", err)
	}
	var manifest releasePhaseManifest
	if err := json.Unmarshal(contents, &manifest); err != nil {
		t.Fatalf("decode release phase manifest: %v", err)
	}
	return manifest
}

func integer(value int) *int { return &value }

func boolean(value bool) *bool { return &value }
