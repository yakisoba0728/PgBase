package jsvm

import (
	"bytes"
	"testing"
)

func TestSecureRandomStringRejectsBiasedSamples(t *testing.T) {
	alphabet := make([]byte, 255)
	for i := range alphabet {
		alphabet[i] = byte(i + 1)
	}
	sample := make([]byte, 128)
	sample[0], sample[1], sample[2] = 0, 255, 1
	got, err := secureRandomStringFromReader(bytes.NewReader(sample), 2, string(alphabet))
	if err != nil {
		t.Fatal(err)
	}
	if got != string([]byte{1, 2}) {
		t.Fatalf("random string = %v, want samples 0 and 1 after rejecting 255", []byte(got))
	}
}
