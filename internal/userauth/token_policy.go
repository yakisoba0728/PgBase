package userauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"strings"
	"time"
)

type TokenPolicy struct {
	Purpose       string `json:"purpose"`
	TTLSeconds    int    `json:"ttlSeconds"`
	KeyVersion    string `json:"keyVersion"`
	KeyConfigured bool   `json:"keyConfigured"`
	Key           string `json:"-"`
}

func (s *Service) ListTokenPolicies(ctx context.Context, realm uuid.UUID) ([]TokenPolicy, error) {
	if realm == uuid.Nil {
		return nil, ErrValidation
	}
	rows, e := s.pool.Query(ctx, `select purpose,ttl_seconds,key_version from pgbase_meta.auth_token_policies where auth_collection_id=$1 order by purpose`, realm)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []TokenPolicy{}
	for rows.Next() {
		var p TokenPolicy
		if e := rows.Scan(&p.Purpose, &p.TTLSeconds, &p.KeyVersion); e != nil {
			return nil, e
		}
		p.KeyConfigured = true
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Service) PutTokenPolicy(ctx context.Context, realm uuid.UUID, p TokenPolicy) (TokenPolicy, error) {
	if realm == uuid.Nil || !tokenPolicyPurpose(p.Purpose) || p.TTLSeconds < 60 || p.TTLSeconds > 86400 || len(p.Key) > 4096 {
		return p, ErrValidation
	}
	if p.KeyVersion == "" {
		p.KeyVersion = uuid.NewString()[:16]
	}
	if len(p.KeyVersion) > 32 {
		return p, ErrValidation
	}
	if p.Key == "" {
		b := make([]byte, 32)
		if _, e := rand.Read(b); e != nil {
			return p, e
		}
		p.Key = base64.RawURLEncoding.EncodeToString(b)
	}
	cipher, e := s.encrypt("auth-token-policy:"+p.Purpose, []byte(p.Key))
	if e != nil {
		return p, e
	}
	_, e = s.pool.Exec(ctx, `insert into pgbase_meta.auth_token_policies(auth_collection_id,purpose,ttl_seconds,key_version,key_ciphertext) values($1,$2,$3,$4,$5) on conflict(auth_collection_id,purpose) do update set ttl_seconds=excluded.ttl_seconds,key_version=excluded.key_version,key_ciphertext=excluded.key_ciphertext,updated_at=CURRENT_TIMESTAMP`, realm, p.Purpose, p.TTLSeconds, p.KeyVersion, cipher)
	p.Key = ""
	p.KeyConfigured = e == nil
	return p, e
}
func tokenPolicyPurpose(v string) bool {
	return v == "verify" || v == "password_reset" || v == "email_change" || v == "session" || v == "otp" || v == "impersonation"
}
func keyedDigest(key []byte, domain, value string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(domain))
	h.Write([]byte(value))
	return h.Sum(nil)
}
func splitPolicyToken(raw string) (string, string, bool) {
	v := strings.Split(raw, ".")
	returnFirst := len(v) == 2 && len(v[0]) > 0 && len(v[1]) > 0
	if !returnFirst {
		return "", "", false
	}
	return v[0], v[1], true
}
func policyTokenValid(raw string) bool {
	_, body, ok := splitPolicyToken(raw)
	if !ok {
		return false
	}
	b, e := base64.RawURLEncoding.Strict().DecodeString(body)
	return e == nil && len(b) == 32 && len(raw) <= maximumTokenBytes
}
func (s *Service) sessionHash(ctx context.Context, raw string) ([]byte, error) {
	p := strings.Split(raw, ".")
	if len(p) == 1 {
		if validateToken(raw) != nil {
			return nil, ErrValidation
		}
		return s.tokenHash(raw), nil
	}
	if len(p) != 3 {
		return nil, ErrValidation
	}
	realm, e := uuid.Parse(p[0])
	if e != nil || !policyTokenValid(p[1]+"."+p[2]) {
		return nil, ErrValidation
	}
	var cipher []byte
	e = s.pool.QueryRow(ctx, `select key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id=$1 and key_version=$2 and purpose='session'`, realm, p[1]).Scan(&cipher)
	if e != nil {
		return nil, ErrInvalidCredentials
	}
	key, e := s.decrypt("auth-token-policy:session", cipher)
	if e != nil {
		return nil, ErrInvalidCredentials
	}
	return keyedDigest(key, "user-session\x00", raw), nil
}
func (s *Service) issueActionPolicy(ctx context.Context, userID uuid.UUID, purpose string) (string, []byte, time.Duration, error) {
	var realm uuid.UUID
	if e := s.pool.QueryRow(ctx, `select auth_collection_id from pgbase_meta.users where id=$1`, userID).Scan(&realm); e != nil {
		return "", nil, 0, e
	}
	var version string
	var ttl int
	var cipher []byte
	e := s.pool.QueryRow(ctx, `select key_version,ttl_seconds,key_ciphertext from pgbase_meta.auth_token_policies where auth_collection_id=$1 and purpose=$2`, realm, purpose).Scan(&version, &ttl, &cipher)
	if e == pgx.ErrNoRows {
		return "", nil, s.emailActionTTL, nil
	}
	if e != nil {
		return "", nil, 0, e
	}
	key, e := s.decrypt("auth-token-policy:"+purpose, cipher)
	if e != nil {
		return "", nil, 0, e
	}
	b := make([]byte, 32)
	if _, e = rand.Read(b); e != nil {
		return "", nil, 0, e
	}
	token := version + "." + base64.RawURLEncoding.EncodeToString(b)
	return token, keyedDigest(key, "user-email-action\x00"+purpose+"\x00", token), time.Duration(ttl) * time.Second, nil
}
