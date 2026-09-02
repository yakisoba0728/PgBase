// Package releasephase pins behavior that must differ between independently
// buildable deployment artifacts in one rollout.
package releasephase

// SchemaCompilerGeneration is 2 in migration 9's P0 artifact and 3 in F.
const SchemaCompilerGeneration int16 = 2
