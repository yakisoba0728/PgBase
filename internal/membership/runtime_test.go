package membership

import (
	"testing"

	"github.com/google/uuid"
)

func TestRuntimeWriterVersionRejectsInvalidRuntime(t *testing.T) {
	var missing *Runtime
	if got := missing.WriterVersion(); got >= WriterA0 {
		t.Fatalf("nil runtime writer version = %d, want invalid", got)
	}
	if got := (&Runtime{}).WriterVersion(); got >= WriterA0 {
		t.Fatalf("zero runtime writer version = %d, want invalid", got)
	}
	runtime := &Runtime{
		service: &Service{}, instanceID: uuid.MustParse("0199b400-0000-7000-8000-000000000071"),
		version: WriterB,
	}
	if got := runtime.WriterVersion(); got != WriterB {
		t.Fatalf("registered runtime writer version = %d, want %d", got, WriterB)
	}
}
