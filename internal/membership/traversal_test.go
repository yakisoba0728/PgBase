package membership

import (
	"errors"
	"testing"
)

func TestTraversalValidateV5UsesReadyGenerationAndDurableHighWater(t *testing.T) {
	ready := Traversal{
		Generation:   3,
		HighWater:    12,
		IssueVersion: 4,
		ready:        true,
	}
	for _, signedHighWater := range []int64{0, 7, 12} {
		if err := ready.ValidateV5(3, signedHighWater); err != nil {
			t.Fatalf("ValidateV5(3, %d): %v", signedHighWater, err)
		}
	}

	tests := []struct {
		name       string
		traversal  Traversal
		generation int64
		highWater  int64
	}{
		{name: "not ready", traversal: Traversal{Generation: 3, HighWater: 12}, generation: 3, highWater: 12},
		{name: "zero generation", traversal: ready, generation: 0, highWater: 12},
		{name: "wrong generation", traversal: ready, generation: 2, highWater: 12},
		{name: "negative high water", traversal: ready, generation: 3, highWater: -1},
		{name: "future high water", traversal: ready, generation: 3, highWater: 13},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := test.traversal.ValidateV5(test.generation, test.highWater)
			if !errors.Is(err, ErrTraversalInvalid) {
				t.Fatalf("ValidateV5() error = %v, want ErrTraversalInvalid", err)
			}
		})
	}
}
