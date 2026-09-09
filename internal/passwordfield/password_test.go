package passwordfield

import (
	"strings"
	"testing"
)

func TestHashVerifyAndRejectMalformed(t *testing.T) {
	hash, err := Hash("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if hash == "correct horse battery staple" || !strings.HasPrefix(hash, "$argon2id$") {
		t.Fatalf("Hash() = %q", hash)
	}
	if err := Verify(hash, "correct horse battery staple"); err != nil {
		t.Fatalf("Verify(correct) = %v", err)
	}
	for _, test := range []struct{ hash, plain string }{{hash, "wrong"}, {"broken", "correct horse battery staple"}} {
		if err := Verify(test.hash, test.plain); err == nil {
			t.Fatalf("Verify(%q, %q) succeeded", test.hash, test.plain)
		}
	}
}

func TestHashUsesFreshSalt(t *testing.T) {
	first, err := Hash("same")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Hash("same")
	if err != nil {
		t.Fatal(err)
	}
	if first == second {
		t.Fatal("two hashes reused a salt")
	}
}
