package adminauth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

const administratorLifecycleLock int64 = 0x7067626173650039

func (s *Service) UpsertAdministrator(ctx context.Context, email, password string) (Administrator, bool, error) {
	if err := s.ready(); err != nil {
		return Administrator{}, false, err
	}
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return Administrator{}, false, err
	}
	hash, err := s.hashPassword(ctx, password)
	if err != nil {
		return Administrator{}, false, err
	}
	now := s.clock.Now().UTC()
	var administrator Administrator
	created := false
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", administratorLifecycleLock); err != nil {
			return err
		}
		store := NewStore(tx)
		existing, _, lookup := store.AdministratorByEmailForUpdate(ctx, normalized)
		if lookup == nil {
			administrator, err = store.ResetAdministratorPassword(ctx, normalized, hash, now)
			return err
		}
		if lookup != ErrInvalidCredentials {
			return lookup
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		administrator, err = store.CreateAdministrator(ctx, Administrator{ID: id, Email: normalized, Active: true, CreatedAt: now, UpdatedAt: now}, hash)
		created = true
		_ = existing
		return err
	})
	return administrator, created, err
}

func (s *Service) DeleteAdministrator(ctx context.Context, email string) error {
	if err := s.ready(); err != nil {
		return err
	}
	normalized, err := NormalizeEmail(email)
	if err != nil {
		return err
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if _, err := tx.Exec(ctx, "select pg_advisory_xact_lock($1)", administratorLifecycleLock); err != nil {
			return err
		}
		store := NewStore(tx)
		count, err := store.ActiveAdministratorCount(ctx)
		if err != nil {
			return err
		}
		if count <= 1 {
			return ErrConflict
		}
		return store.DeleteAdministrator(ctx, normalized)
	})
}

func (s *Service) EnableTOTP(ctx context.Context, email, secret string) error {
	raw, err := decodeTOTPSecret(secret)
	if err != nil {
		return ErrValidation
	}
	id, err := NewStore(s.pool).AdministratorID(ctx, email)
	if err != nil {
		return err
	}
	encrypted, err := s.encryptTOTP(raw)
	if err != nil {
		return err
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		if err := store.SetTOTP(ctx, id, encrypted, s.clock.Now().UTC()); err != nil {
			return err
		}
		return store.DeleteAdministratorSessions(ctx, id)
	})
}
func (s *Service) DisableTOTP(ctx context.Context, email string) error {
	id, err := NewStore(s.pool).AdministratorID(ctx, email)
	if err != nil {
		return err
	}
	return s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		store := NewStore(tx)
		if err := store.DeleteTOTP(ctx, id); err != nil {
			return err
		}
		return store.DeleteAdministratorSessions(ctx, id)
	})
}
func (s *Service) TOTPEnabled(ctx context.Context, email string) (bool, error) {
	id, err := NewStore(s.pool).AdministratorID(ctx, email)
	if err != nil {
		return false, err
	}
	_, enabled, err := NewStore(s.pool).TOTP(ctx, id)
	return enabled, err
}
func (s *Service) AllowIP(ctx context.Context, email, prefix string) error {
	canonical, err := canonicalPrefix(prefix)
	if err != nil {
		return ErrValidation
	}
	id, err := NewStore(s.pool).AdministratorID(ctx, email)
	if err != nil {
		return err
	}
	return NewStore(s.pool).AddIPPrefix(ctx, id, canonical)
}
func (s *Service) DenyIP(ctx context.Context, email, prefix string) error {
	canonical, err := canonicalPrefix(prefix)
	if err != nil {
		return ErrValidation
	}
	id, err := NewStore(s.pool).AdministratorID(ctx, email)
	if err != nil {
		return err
	}
	return NewStore(s.pool).DeleteIPPrefix(ctx, id, canonical)
}
func (s *Service) ListIP(ctx context.Context, email string) ([]string, error) {
	id, err := NewStore(s.pool).AdministratorID(ctx, email)
	if err != nil {
		return nil, err
	}
	return NewStore(s.pool).IPPrefixes(ctx, id)
}
func (s *Service) AuthorizeIP(ctx context.Context, administratorID uuid.UUID, address string) error {
	return authorizeIP(ctx, NewStore(s.pool), administratorID, address)
}

func authorizeIP(ctx context.Context, store *Store, administratorID uuid.UUID, address string) error {
	prefixes, err := store.IPPrefixes(ctx, administratorID)
	if err != nil {
		return err
	}
	if len(prefixes) == 0 {
		return nil
	}
	ip, err := netip.ParseAddr(address)
	if err != nil || ip.Zone() != "" {
		return ErrIPRestricted
	}
	for _, value := range prefixes {
		prefix, parseErr := netip.ParsePrefix(value)
		if parseErr == nil && prefix.Contains(ip.Unmap()) {
			return nil
		}
	}
	return ErrIPRestricted
}

func canonicalPrefix(value string) (string, error) {
	prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
	if err != nil || prefix != prefix.Masked() {
		return "", fmt.Errorf("invalid CIDR")
	}
	return prefix.String(), nil
}
func decodeTOTPSecret(value string) ([]byte, error) {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(normalized, "="))
	if err != nil || len(raw) < 10 || len(raw) > 128 {
		return nil, fmt.Errorf("invalid OTP secret")
	}
	return raw, nil
}
func (s *Service) encryptTOTP(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(totpKey(s.tokenSecret))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plain, nil), nil
}
func (s *Service) decryptTOTP(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(totpKey(s.tokenSecret))
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(ciphertext) < gcm.NonceSize() {
		return nil, fmt.Errorf("invalid encrypted OTP secret")
	}
	return gcm.Open(nil, ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():], nil)
}
func totpKey(secret []byte) []byte {
	sum := sha256.Sum256(append([]byte("pgbase administrator totp v1\x00"), secret...))
	return sum[:]
}
func validTOTP(secret []byte, code string, now time.Time) bool {
	if len(code) != 6 {
		return false
	}
	want, err := strconv.Atoi(code)
	if err != nil {
		return false
	}
	for offset := -1; offset <= 1; offset++ {
		counter := uint64(now.Add(time.Duration(offset)*30*time.Second).Unix() / 30)
		var data [8]byte
		binary.BigEndian.PutUint64(data[:], counter)
		mac := hmac.New(sha1.New, secret)
		mac.Write(data[:])
		digest := mac.Sum(nil)
		start := digest[len(digest)-1] & 15
		value := (int(digest[start])&127)<<24 | int(digest[start+1])<<16 | int(digest[start+2])<<8 | int(digest[start+3])
		if subtle.ConstantTimeEq(int32(value%1000000), int32(want)) == 1 {
			return true
		}
	}
	return false
}
