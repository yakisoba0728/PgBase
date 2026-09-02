package release

import (
	"reflect"
	"testing"
)

func TestSchemaCompilerRolloutManifestPinsThePreparationAndFeatureArtifacts(t *testing.T) {
	manifest := readReleasePhaseManifest(t)
	var rollout releaseRollout
	for _, candidate := range manifest.Rollouts {
		if candidate.Name == "schema-compiler-v9" {
			rollout = candidate
			break
		}
	}
	if rollout.Name == "" || rollout.Migration != 9 {
		t.Fatalf("schema compiler rollout = %#v, want migration 9", rollout)
	}
	wantPredecessor := &releaseOperator{
		Commit: "d3fb5eb9e5480ea1fdba30fcd81ff85608e2755b", Version: "v0.0.8-rc.1",
	}
	if !reflect.DeepEqual(rollout.Predecessor, wantPredecessor) {
		t.Fatalf("schema compiler predecessor = %#v, want %#v", rollout.Predecessor, wantPredecessor)
	}
	want := []releaseArtifact{
		{
			Phase: "P0", Commit: "b0a3c17c923bc0b403b0f8ffe5b0a566ce144d14",
			Version: "v0.0.9-alpha.0", SchemaCompilerGeneration: integer(2),
			AcceptedMigrations: []int{8, 9}, CompanionEnabled: boolean(false),
		},
		{
			Phase: "F", Commit: "1f00fb8c05d62ba5b3a6dde3861d86e916f490cf",
			Version: "v0.0.9-rc.0", SchemaCompilerGeneration: integer(3),
			AcceptedMigrations: []int{9}, CompanionEnabled: boolean(true),
		},
	}
	if !reflect.DeepEqual(rollout.Artifacts, want) {
		t.Fatalf("schema compiler artifacts = %#v, want %#v", rollout.Artifacts, want)
	}
}
