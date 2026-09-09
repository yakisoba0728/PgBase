// Package passwordfield hashes and verifies write-only application password fields.
// It is deliberately independent from administrator and public-user authentication.
package passwordfield

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	timeCost    uint32 = 3
	memoryCost  uint32 = 64 * 1024
	parallelism uint8  = 4
	saltBytes          = 16
	keyBytes    uint32 = 32
)

var ErrInvalidHash = errors.New("invalid password hash")

// Hash converts plaintext into the self-describing format stored in record columns.
func Hash(plaintext string) (string, error) {
	salt := make([]byte, saltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(plaintext), salt, timeCost, memoryCost, parallelism, keyBytes)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		memoryCost, timeCost, parallelism, base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

// Verify reports whether plaintext matches a hash produced by Hash.
func Verify(encoded, plaintext string) error {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" ||
		parts[3] != "m=65536,t=3,p=4" {
		return ErrInvalidHash
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != saltBytes {
		return ErrInvalidHash
	}
	expected, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(expected) != int(keyBytes) {
		return ErrInvalidHash
	}
	actual := argon2.IDKey([]byte(plaintext), salt, timeCost, memoryCost, parallelism, keyBytes)
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return ErrInvalidHash
	}
	return nil
}
