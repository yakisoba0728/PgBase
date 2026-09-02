package adminauth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 4
	argonSaltLen        = 16
	argonKeyLen  uint32 = 32
)

// Argon2idPasswords uses the fixed Production Kernel password parameters.
type Argon2idPasswords struct{}

func NewArgon2idPasswords() *Argon2idPasswords { return &Argon2idPasswords{} }

func (*Argon2idPasswords) Hash(password string) (string, error) {
	if err := validatePassword(password); err != nil {
		return "", err
	}
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func (*Argon2idPasswords) Verify(encodedHash, password string) error {
	if validatePassword(password) != nil {
		return ErrInvalidCredentials
	}
	salt, expected, ok := parsePasswordHash(encodedHash)
	if !ok {
		return ErrInvalidCredentials
	}
	actual := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	if subtle.ConstantTimeCompare(actual, expected) != 1 {
		return ErrInvalidCredentials
	}
	return nil
}

func validatePassword(password string) error {
	if !utf8.ValidString(password) || len(password) < 12 || len(password) > 1024 {
		return ErrValidation
	}
	return nil
}

func parsePasswordHash(encoded string) ([]byte, []byte, bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != "v=19" || parts[3] != "m=65536,t=3,p=4" {
		return nil, nil, false
	}
	if version, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v=")); err != nil || version != argon2.Version {
		return nil, nil, false
	}
	salt, err := base64.RawStdEncoding.Strict().DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltLen {
		return nil, nil, false
	}
	key, err := base64.RawStdEncoding.Strict().DecodeString(parts[5])
	if err != nil || len(key) != int(argonKeyLen) {
		return nil, nil, false
	}
	return salt, key, true
}

func NormalizeEmail(email string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(email))
	if normalized == "" || len(normalized) > 254 {
		return "", ErrValidation
	}
	for i := range len(normalized) {
		if normalized[i] >= utf8.RuneSelf {
			return "", ErrValidation
		}
	}
	address, err := mail.ParseAddress(normalized)
	if err != nil || address.Name != "" || address.Address != normalized {
		return "", ErrValidation
	}
	return normalized, nil
}
