package filestore

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/identity"
)

// ResolveDownloadToken authenticates a file token without revealing its file
// key or signature. It is used by the administrator-only compatibility lookup.
func (s *Service) ResolveDownloadToken(ctx context.Context, token string) (identity.Subject, error) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	minimum := 16 + 16 + 1 + 8 + sha256.Size
	if err != nil || len(raw) < minimum {
		return identity.Subject{}, ErrNotFound
	}
	payload, supplied := raw[:len(raw)-sha256.Size], raw[len(raw)-sha256.Size:]
	mac := hmac.New(sha256.New, s.options.TokenSecret)
	_, _ = mac.Write(payload)
	length := int(payload[32])
	if !hmac.Equal(supplied, mac.Sum(nil)) || len(payload) != 33+length+8 || int64(binary.BigEndian.Uint64(payload[len(payload)-8:])) < s.options.Now().Unix() {
		return identity.Subject{}, ErrNotFound
	}
	userID, err := uuid.FromBytes(payload[:16])
	if err != nil {
		return identity.Subject{}, ErrNotFound
	}
	fileID, err := uuid.FromBytes(payload[16:32])
	if err != nil {
		return identity.Subject{}, ErrNotFound
	}
	var exists bool
	if err = s.pool.QueryRow(ctx, `select exists(select 1 from pgbase_meta.record_files where id=$1 and status='ready') and exists(select 1 from pgbase_meta.users where id=$2 and active)`, fileID, userID).Scan(&exists); err != nil || !exists {
		return identity.Subject{}, ErrNotFound
	}
	return identity.Subject{Kind: identity.User, ID: userID}, nil
}
