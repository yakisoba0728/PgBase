package userauth

import (
	"crypto/hmac"
	"crypto/sha1"
	"encoding/binary"
	"fmt"
	"testing"
)

func TestHOTPRFC6238SHA1Vectors(t *testing.T) {
	secret := []byte("12345678901234567890")
	vectors := []struct {
		unix int64
		want string
	}{{59, "94287082"}, {1111111109, "07081804"}, {1111111111, "14050471"}, {1234567890, "89005924"}, {2000000000, "69279037"}, {20000000000, "65353130"}}
	for _, vector := range vectors {
		if got := hotpDigits(secret, vector.unix/30, 8); got != vector.want {
			t.Errorf("at %d got %s want %s", vector.unix, got, vector.want)
		}
	}
}

func hotpDigits(secret []byte, counter int64, digits uint32) string {
	var value [8]byte
	binary.BigEndian.PutUint64(value[:], uint64(counter))
	mac := hmac.New(sha1.New, secret)
	mac.Write(value[:])
	sum := mac.Sum(nil)
	offset := sum[len(sum)-1] & 15
	n := (uint32(sum[offset])&127)<<24 | uint32(sum[offset+1])<<16 | uint32(sum[offset+2])<<8 | uint32(sum[offset+3])
	modulus := uint32(1)
	for range digits {
		modulus *= 10
	}
	return fmt.Sprintf("%0*d", digits, n%modulus)
}
