package adminauth

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"testing"
	"time"
)

func TestTOTPEncryptionFailsClosedWithTheWrongTokenSecret(t *testing.T) {
	first := &Service{tokenSecret: []byte("01234567890123456789012345678901")}
	ciphertext, err := first.encryptTOTP([]byte("Hello!\xde\xad\xbe\xef"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (&Service{tokenSecret: []byte("abcdefghijabcdefghijabcdefghij12")}).decryptTOTP(ciphertext); err == nil {
		t.Fatal("wrong token secret decrypted TOTP material")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	if _, err := first.decryptTOTP(ciphertext); err == nil {
		t.Fatal("modified OTP ciphertext decrypted")
	}
	ciphertext[len(ciphertext)-1] ^= 1
	plain, err := first.decryptTOTP(ciphertext)
	if err != nil || string(plain) != "Hello!\xde\xad\xbe\xef" {
		t.Fatalf("plain=%q err=%v", plain, err)
	}
}

func TestTOTPValidationAndCIDRNormalization(t *testing.T) {
	secret, err := decodeTOTPSecret("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_000, 0)
	if _, valid := matchTOTP(secret, testTOTP(secret, now), now, -1); !valid {
		t.Fatal("current OTP rejected")
	}
	if _, valid := matchTOTP(secret, "000000", now, -1); valid {
		t.Fatal("incorrect OTP accepted")
	}
	if _, err := decodeTOTPSecret("%%%not-base32%%%"); err == nil {
		t.Fatal("invalid OTP secret accepted")
	}
	if got, err := canonicalPrefix("192.0.2.0/24"); err != nil || got != "192.0.2.0/24" {
		t.Fatalf("prefix=%q err=%v", got, err)
	}
	if _, err := canonicalPrefix("192.0.2.4/24"); err == nil {
		t.Fatal("noncanonical CIDR accepted")
	}
}

func TestTOTPRequiresExactlySixASCIIDigits(t *testing.T) {
	secret, err := decodeTOTPSecret("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_270, 0)
	code := testTOTP(secret, now)
	if _, valid := matchTOTP(secret, code, now, -1); code != "070624" || !valid {
		t.Fatalf("leading-zero fixture is not valid: %q", code)
	}
	for _, invalid := range []string{"+70624", "-70624", "70624", "0070624", " 70624", "０70624", "07\x00624", "4295037920", "999999999999999999999"} {
		if _, valid := matchTOTP(secret, invalid, now, -1); valid {
			t.Errorf("noncanonical OTP %q accepted", invalid)
		}
	}
}

func TestTOTPMatchesOnlyUnusedCountersInTheAcceptedWindow(t *testing.T) {
	secret, err := decodeTOTPSecret("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_700_000_270, 0)
	for _, offset := range []int64{-1, 0, 1} {
		step := now.Add(time.Duration(offset) * 30 * time.Second)
		code := testTOTP(secret, step)
		counter, valid := matchTOTP(secret, code, now, -1)
		if !valid || counter != step.Unix()/30 {
			t.Fatalf("offset=%d counter=%d valid=%t", offset, counter, valid)
		}
		if _, valid := matchTOTP(secret, code, now, counter); valid {
			t.Fatalf("consumed counter accepted at offset %d", offset)
		}
	}
	for _, offset := range []int64{-2, 2} {
		code := testTOTP(secret, now.Add(time.Duration(offset)*30*time.Second))
		if _, valid := matchTOTP(secret, code, now, -1); valid {
			t.Fatalf("code outside accepted window matched at offset %d", offset)
		}
	}
	// Accepting the future clock-skew step also consumes older steps.
	if _, valid := matchTOTP(secret, testTOTP(secret, now), now, now.Unix()/30+1); valid {
		t.Fatal("current step accepted after a later counter was consumed")
	}
}

func TestAddressAllowedMatchesTheSessionCheck(t *testing.T) {
	for _, tc := range []struct {
		prefixes []string
		address  string
		allowed  bool
	}{
		{nil, "unknown", true},
		{[]string{"192.0.2.0/24"}, "192.0.2.200", true},
		{[]string{"192.0.2.0/24"}, "::ffff:192.0.2.200", true},
		{[]string{"192.0.2.0/24"}, "198.51.100.1", false},
		{[]string{"192.0.2.9/32", "2001:db8::/48"}, "2001:db8:0:ff::1", true},
		{[]string{"2001:db8::/48"}, "2001:db9::1", false},
		{[]string{"2001:db8::/48"}, "fe80::1%en0", false},
		{[]string{"192.0.2.0/24"}, "unknown", false},
	} {
		if got := addressAllowed(tc.prefixes, tc.address); (got == nil) != tc.allowed || (got != nil && got != ErrIPRestricted) {
			t.Errorf("addressAllowed(%v, %q) = %v, want allowed=%v", tc.prefixes, tc.address, got, tc.allowed)
		}
	}
}

func testTOTP(secret []byte, now time.Time) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, secret)
	mac.Write(counter[:])
	digest := mac.Sum(nil)
	offset := digest[len(digest)-1] & 15
	value := (int(digest[offset])&127)<<24 | int(digest[offset+1])<<16 | int(digest[offset+2])<<8 | int(digest[offset+3])
	return fmt.Sprintf("%06d", value%1_000_000)
}
