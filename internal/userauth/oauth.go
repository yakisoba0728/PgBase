package userauth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
)

const maximumOAuthStateTTL = 15 * time.Minute

func (s *Service) ConfigureOAuth(providers map[string]OAuthProvider, redirectAllowlist []string, ttl time.Duration) error {
	if s == nil || len(providers) == 0 || len(redirectAllowlist) == 0 || ttl <= 0 || ttl > maximumOAuthStateTTL {
		return ErrValidation
	}
	providerCopy := make(map[string]OAuthProvider, len(providers))
	for name, provider := range providers {
		if !validProviderName(name) || provider == nil {
			return ErrValidation
		}
		providerCopy[name] = provider
	}
	redirects := make(map[string]struct{}, len(redirectAllowlist))
	for _, raw := range redirectAllowlist {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" || parsed.Fragment != "" {
			return ErrValidation
		}
		redirects[raw] = struct{}{}
	}
	s.oauthProviders, s.oauthRedirects, s.oauthStateTTL = providerCopy, redirects, ttl
	return nil
}

// ConfigureOAuthRegistration controls creation of users from unlinked identities.
// It defaults to false, independently of provider configuration.
func (s *Service) ConfigureOAuthRegistration(enabled bool) { s.oauthRegistration = enabled }

func (s *Service) BeginOAuth(ctx context.Context, provider, redirectURI, clientAddress string) (OAuthAuthorization, error) {
	if err := validateClientAddress(clientAddress); err != nil {
		return OAuthAuthorization{}, err
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return OAuthAuthorization{}, err
	}
	bucket := s.subjectHash("oauth-begin", "network", clientAddress)
	if err := s.consumeRateLimit(ctx, bucket, loginNetworkLimit, now); err != nil {
		return OAuthAuthorization{}, err
	}
	return s.beginOAuth(ctx, DefaultCollectionID, provider, redirectURI, uuid.Nil, "")
}

func (s *Service) BeginOAuthInCollection(ctx context.Context, collectionID uuid.UUID, provider, redirectURI, clientAddress string) (OAuthAuthorization, error) {
	if collectionID == uuid.Nil || validateClientAddress(clientAddress) != nil {
		return OAuthAuthorization{}, ErrValidation
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return OAuthAuthorization{}, err
	}
	if err := s.consumeRateLimit(ctx, s.subjectHash("oauth-begin", collectionID.String(), clientAddress), loginNetworkLimit, now); err != nil {
		return OAuthAuthorization{}, err
	}
	return s.beginOAuth(ctx, collectionID, provider, redirectURI, uuid.Nil, "")
}

func (s *Service) BeginOAuthLink(ctx context.Context, userID uuid.UUID, currentPassword, provider, redirectURI string) (OAuthAuthorization, error) {
	user, hash, err := s.verifyUserPassword(ctx, userID, currentPassword, "oauth-link")
	if err != nil {
		return OAuthAuthorization{}, err
	}
	return s.beginOAuth(ctx, user.AuthCollectionID, provider, redirectURI, user.ID, hash)
}

func (s *Service) beginOAuth(ctx context.Context, collectionID uuid.UUID, provider, redirectURI string, linkUser uuid.UUID, passwordHash string) (OAuthAuthorization, error) {
	release, err := admit(s.credentialSlots)
	if err != nil {
		return OAuthAuthorization{}, err
	}
	defer release()
	configured, ok := s.oauthProviders[provider]
	if !ok || configured == nil {
		return OAuthAuthorization{}, ErrValidation
	}
	if _, ok := s.oauthRedirects[redirectURI]; !ok {
		return OAuthAuthorization{}, ErrValidation
	}
	stateBytes := make([]byte, 32)
	verifierBytes := make([]byte, 32)
	if _, err := rand.Read(stateBytes); err != nil {
		return OAuthAuthorization{}, err
	}
	if _, err := rand.Read(verifierBytes); err != nil {
		return OAuthAuthorization{}, err
	}
	state := base64.RawURLEncoding.EncodeToString(stateBytes)
	verifier := base64.RawURLEncoding.EncodeToString(verifierBytes)
	digest := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(digest[:])
	ciphertext, err := s.encrypt("oauth-verifier", []byte(verifier))
	if err != nil {
		return OAuthAuthorization{}, err
	}
	now, err := s.databaseNow(ctx)
	if err != nil {
		return OAuthAuthorization{}, err
	}
	expires := now.Add(s.oauthStateTTL)
	var link any
	var credential any
	if linkUser != uuid.Nil {
		link = linkUser
		credential = s.credentialHash(passwordHash)
	}
	_, err = s.pool.Exec(ctx, `insert into pgbase_meta.user_oauth_states(id,auth_collection_id,provider,state_hash,verifier_ciphertext,redirect_uri,link_user_id,credential_hash,expires_at,created_at) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, uuid.New(), collectionID, provider, s.oauthStateHash(provider, state), ciphertext, redirectURI, link, credential, expires, now)
	if err != nil {
		return OAuthAuthorization{}, fmt.Errorf("create OAuth state: %w", err)
	}
	authorizationURL, err := configured.AuthorizationURL(state, challenge, redirectURI)
	if err != nil {
		return OAuthAuthorization{}, errors.New("create OAuth authorization URL")
	}
	return OAuthAuthorization{AuthorizationURL: authorizationURL, ExpiresAt: expires}, nil
}

func (s *Service) CompleteOAuth(ctx context.Context, provider, state, code string) (Session, error) {
	configured, ok := s.oauthProviders[provider]
	if !ok || configured == nil || validateToken(state) != nil || code == "" || len(code) > 4096 {
		return Session{}, ErrInvalidCredentials
	}
	var encrypted []byte
	var redirect string
	var collectionID uuid.UUID
	err := s.pool.QueryRow(ctx, `select auth_collection_id,verifier_ciphertext,redirect_uri from pgbase_meta.user_oauth_states where provider=$1 and state_hash=$2 and consumed_at is null and expires_at>CURRENT_TIMESTAMP`, provider, s.oauthStateHash(provider, state)).Scan(&collectionID, &encrypted, &redirect)
	if errors.Is(err, pgx.ErrNoRows) {
		return Session{}, ErrInvalidCredentials
	}
	if err != nil {
		return Session{}, err
	}
	verifierBytes, err := s.decrypt("oauth-verifier", encrypted)
	if err != nil {
		return Session{}, err
	}
	identity, err := configured.Exchange(ctx, code, string(verifierBytes), redirect)
	if err != nil {
		return Session{}, ErrInvalidCredentials
	}
	if !validOAuthIdentity(identity) {
		return Session{}, ErrInvalidCredentials
	}
	normalized, err := normalizeEmail(identity.Email)
	if err != nil {
		return Session{}, ErrInvalidCredentials
	}
	identity.Email = normalized
	now, err := s.databaseNow(ctx)
	if err != nil {
		return Session{}, err
	}
	var issued Session
	var operationErr error
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var stateID uuid.UUID
		var persistedCollectionID uuid.UUID
		var linkUser *uuid.UUID
		var binding []byte
		err := tx.QueryRow(ctx, `select id,auth_collection_id,link_user_id,credential_hash from pgbase_meta.user_oauth_states where provider=$1 and state_hash=$2 and redirect_uri=$3 and auth_collection_id=$4 and consumed_at is null and expires_at>CURRENT_TIMESTAMP for update`, provider, s.oauthStateHash(provider, state), redirect, collectionID).Scan(&stateID, &persistedCollectionID, &linkUser, &binding)
		if errors.Is(err, pgx.ErrNoRows) {
			operationErr = ErrInvalidCredentials
			return nil
		}
		if err != nil {
			return err
		}
		var user User
		if linkUser != nil {
			var hash string
			err = tx.QueryRow(ctx, `select id,auth_collection_id,email,verified,created_at,password_hash from pgbase_meta.users where id=$1 and active for update`, *linkUser).Scan(&user.ID, &user.AuthCollectionID, &user.Email, &user.Verified, &user.CreatedAt, &hash)
			if errors.Is(err, pgx.ErrNoRows) {
				operationErr = ErrInvalidCredentials
				return nil
			}
			if err != nil {
				return err
			}
			if !hmac.Equal(binding, s.credentialHash(hash)) {
				operationErr = ErrInvalidCredentials
				return nil
			}
			if user.AuthCollectionID != persistedCollectionID {
				operationErr = ErrInvalidCredentials
				return nil
			}
			_, err = tx.Exec(ctx, `insert into pgbase_meta.user_oauth_identities(auth_collection_id,provider,subject,user_id,created_at) values($1,$2,$3,$4,CURRENT_TIMESTAMP)`, user.AuthCollectionID, provider, identity.Subject, user.ID)
			if database.Classify(err) == database.KindConflict {
				operationErr = ErrConflict
				return nil
			}
			if err != nil {
				return err
			}
		} else {
			err = tx.QueryRow(ctx, `select u.id,u.auth_collection_id,u.email,u.verified,u.created_at from pgbase_meta.user_oauth_identities i join pgbase_meta.users u on u.id=i.user_id where i.auth_collection_id=$1 and i.provider=$2 and i.subject=$3 and u.active for update of u`, persistedCollectionID, provider, identity.Subject).Scan(&user.ID, &user.AuthCollectionID, &user.Email, &user.Verified, &user.CreatedAt)
			if errors.Is(err, pgx.ErrNoRows) {
				var registrationEnabled bool
				if err := tx.QueryRow(ctx, `select registration_enabled from pgbase_meta.auth_collections where id=$1 for share`, persistedCollectionID).Scan(&registrationEnabled); errors.Is(err, pgx.ErrNoRows) {
					operationErr = ErrInvalidCredentials
					return nil
				} else if err != nil {
					return err
				}
				if !s.oauthRegistration || !registrationEnabled || persistedCollectionID == DefaultCollectionID && !s.defaultRegistration {
					operationErr = ErrInvalidCredentials
					return nil
				}
				var emailExists bool
				if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.users where auth_collection_id=$2 and email=$1)`, identity.Email, persistedCollectionID).Scan(&emailExists); err != nil {
					return err
				}
				if emailExists {
					operationErr = ErrConflict
					return nil
				}
				user = User{ID: uuid.New(), Email: identity.Email, Verified: identity.EmailVerified, CreatedAt: now}
				user.AuthCollectionID = persistedCollectionID
				_, err = tx.Exec(ctx, `insert into pgbase_meta.users(id,auth_collection_id,email,password_hash,password_enabled,active,verified,created_at,updated_at) values($1,$2,$3,'!oauth-only',false,true,$4,$5,$5)`, user.ID, user.AuthCollectionID, user.Email, user.Verified, now)
				if database.Classify(err) == database.KindConflict {
					operationErr = ErrConflict
					return nil
				}
				if err != nil {
					return err
				}
				_, err = tx.Exec(ctx, `insert into pgbase_meta.user_oauth_identities(auth_collection_id,provider,subject,user_id,created_at) values($1,$2,$3,$4,$5)`, persistedCollectionID, provider, identity.Subject, user.ID, now)
				if database.Classify(err) == database.KindConflict {
					operationErr = ErrConflict
					return nil
				}
				if err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
		if _, err := tx.Exec(ctx, `update pgbase_meta.user_oauth_states set consumed_at=CURRENT_TIMESTAMP where id=$1`, stateID); err != nil {
			return err
		}
		var active bool
		if err := tx.QueryRow(ctx, `select exists(select 1 from pgbase_meta.user_totp_factors where user_id=$1 and active)`, user.ID).Scan(&active); err != nil {
			return err
		}
		if active {
			challenge, err := s.insertMFAChallenge(ctx, tx, user.ID, now)
			if err != nil {
				return err
			}
			operationErr = challenge
			return nil
		}
		issued, err = s.insertSession(ctx, tx, user, now)
		return err
	})
	if err != nil {
		return Session{}, err
	}
	if operationErr != nil {
		return Session{}, operationErr
	}
	return issued, nil
}

func (s *Service) oauthStateHash(provider, state string) []byte {
	return s.digest("user-oauth-state\x00"+provider+"\x00", state)
}
func validProviderName(v string) bool {
	if len(v) < 1 || len(v) > 64 {
		return false
	}
	for _, r := range v {
		if !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') && r != '-' && r != '_' {
			return false
		}
	}
	return true
}
func validOAuthIdentity(v OAuthIdentity) bool {
	return len(v.Subject) > 0 && len(v.Subject) <= 512 && len(v.Email) <= 254
}
