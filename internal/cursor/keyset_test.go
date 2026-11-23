package cursor

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
)

func TestKeysetCursorRoundTripAndBinding(t *testing.T) {
	t.Parallel()
	codec := NewCodec(bytes.Repeat([]byte{0x2a}, 32))
	token, err := codec.EncodeKeyset("jobs.runs:a", 42)
	if err != nil {
		t.Fatal(err)
	}
	if before, err := codec.DecodeKeyset("jobs.runs:a", token); err != nil || before != 42 {
		t.Fatalf("round trip = %d, %v", before, err)
	}
	if _, err := codec.DecodeKeyset("jobs.runs:b", token); !IsInvalid(err) {
		t.Fatalf("cross-scope token = %v, want invalid", err)
	}
	if _, err := NewCodec(bytes.Repeat([]byte{0x2b}, 32)).DecodeKeyset("jobs.runs:a", token); !IsInvalid(err) {
		t.Fatalf("foreign secret = %v, want invalid", err)
	}
	// A record cursor envelope signed in the record domain is not a keyset cursor.
	recordDomain, err := codec.signAndEncode([]byte(`{"scope":"jobs.runs:a","before":"42"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := codec.DecodeKeyset("jobs.runs:a", recordDomain); !IsInvalid(err) {
		t.Fatalf("record-domain token = %v, want invalid", err)
	}
	for _, payload := range []string{
		`{"scope":"jobs.runs:a","before":"042"}`,
		`{"scope":"jobs.runs:a","before":"0"}`,
		`{"scope":"jobs.runs:a","before":"-1"}`,
		`{"scope":"jobs.runs:a","before":42}`,
		`{"scope":"jobs.runs:a","before":"42","extra":1}`,
		`{"before":"42","scope":"jobs.runs:a"}`,
	} {
		forged, err := codec.signAndEncodeIn(keysetDomain, []byte(payload))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := codec.DecodeKeyset("jobs.runs:a", forged); !IsInvalid(err) {
			t.Fatalf("non-canonical payload %s = %v, want invalid", payload, err)
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(token)
	if _, err := codec.DecodeKeyset("jobs.runs:a", base64.RawStdEncoding.EncodeToString(raw)+"="); !IsInvalid(err) {
		t.Fatalf("re-encoded token = %v, want invalid", err)
	}
	for _, scope := range []string{"", strings.Repeat("s", maxKeysetScopeBytes+1)} {
		if _, err := codec.EncodeKeyset(scope, 1); !IsInvalid(err) {
			t.Fatalf("scope %d bytes = %v, want invalid", len(scope), err)
		}
	}
	if _, err := codec.EncodeKeyset("jobs.runs:a", 0); !IsInvalid(err) {
		t.Fatalf("zero sequence = %v, want invalid", err)
	}
}
