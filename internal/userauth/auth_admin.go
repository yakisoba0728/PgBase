package userauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/mail"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

// PublicAuthMethods is the non-sensitive authentication capability discovery
// contract for one auth collection. It intentionally exposes provider names only;
// provider endpoints, client IDs, and secrets remain administrator-only settings.
type PublicAuthMethods struct {
	PasswordEnabled bool     `json:"passwordEnabled"`
	IdentityFields  []string `json:"identityFields"`
	OTPEnabled      bool     `json:"otpEnabled"`
	MFAEnabled      bool     `json:"mfaEnabled"`
	OAuthProviders  []string `json:"oauthProviders"`
}

type AuthPolicy struct {
	PasswordEnabled      bool     `json:"passwordEnabled"`
	MinimumPasswordBytes int      `json:"minimumPasswordBytes"`
	MaximumPasswordBytes int      `json:"maximumPasswordBytes"`
	PasswordPattern      string   `json:"passwordPattern"`
	IdentityFields       []string `json:"identityFields"`
	MFAEnabled           bool     `json:"mfaEnabled"`
	MFADurationSeconds   int      `json:"mfaDurationSeconds"`
	MFARule              string   `json:"mfaRule"`
	OTPEnabled           bool     `json:"otpEnabled"`
	OTPDurationSeconds   int      `json:"otpDurationSeconds"`
	OTPMaxAttempts       int      `json:"otpMaxAttempts"`
}

type OAuthProviderConfig struct {
	Name                  string   `json:"name"`
	AuthorizationURL      string   `json:"authorizationUrl"`
	TokenURL              string   `json:"tokenUrl"`
	UserInfoURL           string   `json:"userInfoUrl"`
	ClientID              string   `json:"clientId"`
	Enabled               bool     `json:"enabled"`
	SecretConfigured      bool     `json:"secretConfigured"`
	AllowInsecureLoopback bool     `json:"allowInsecureLoopback"`
	Scopes                []string `json:"scopes"`
	RedirectURIs          []string `json:"redirectUris"`
	SubjectField          string   `json:"subjectField"`
	EmailField            string   `json:"emailField"`
	EmailVerifiedField    string   `json:"emailVerifiedField"`
	AvatarField           string   `json:"avatarField"`
	ClientSecret          string   `json:"-"`
}

// OAuthProviderCatalog is the safe configuration starter returned by the PB
// compatible helper route. It contains no tenant credentials or secrets.
type OAuthProviderCatalog struct {
	Name             string   `json:"name"`
	AuthorizationURL string   `json:"authorizationUrl"`
	TokenURL         string   `json:"tokenUrl"`
	UserInfoURL      string   `json:"userInfoUrl"`
	Scopes           []string `json:"scopes"`
}

func OAuthProviderCatalogs() []OAuthProviderCatalog {
	return []OAuthProviderCatalog{
		{Name: "google", AuthorizationURL: "https://accounts.google.com/o/oauth2/v2/auth", TokenURL: "https://oauth2.googleapis.com/token", UserInfoURL: "https://openidconnect.googleapis.com/v1/userinfo", Scopes: []string{"openid", "email", "profile"}},
		{Name: "github", AuthorizationURL: "https://github.com/login/oauth/authorize", TokenURL: "https://github.com/login/oauth/access_token", UserInfoURL: "https://api.github.com/user", Scopes: []string{"user:email"}},
		{Name: "microsoft", AuthorizationURL: "https://login.microsoftonline.com/common/oauth2/v2.0/authorize", TokenURL: "https://login.microsoftonline.com/common/oauth2/v2.0/token", UserInfoURL: "https://graph.microsoft.com/oidc/userinfo", Scopes: []string{"openid", "email", "profile"}},
	}
}

type MailTemplate struct {
	Purpose  string `json:"purpose"`
	Subject  string `json:"subject"`
	Body     string `json:"body"`
	HTMLBody string `json:"htmlBody"`
}
type SMTPAdminConfig struct {
	Address               string `json:"address"`
	From                  string `json:"from"`
	Username              string `json:"username"`
	Password              string `json:"-"`
	ServerName            string `json:"serverName"`
	ImplicitTLS           bool   `json:"implicitTls"`
	AllowInsecureLoopback bool   `json:"allowInsecureLoopback"`
	PasswordConfigured    bool   `json:"passwordConfigured"`
}

type AuthAdministration struct {
	Policy    AuthPolicy            `json:"policy"`
	Providers []OAuthProviderConfig `json:"providers"`
	Templates []MailTemplate        `json:"templates"`
	SMTP      *SMTPAdminConfig      `json:"smtp,omitempty"`
}

func (s *Service) GetAuthAdministration(ctx context.Context, collectionID uuid.UUID) (AuthAdministration, error) {
	if collectionID == uuid.Nil {
		return AuthAdministration{}, ErrValidation
	}
	out := AuthAdministration{Providers: []OAuthProviderConfig{}, Templates: []MailTemplate{}}
	if err := s.pool.QueryRow(ctx, `select password_enabled,minimum_password_bytes,maximum_password_bytes,password_pattern,identity_fields,mfa_enabled,mfa_duration_seconds,mfa_rule,otp_enabled,otp_duration_seconds,otp_max_attempts from pgbase_meta.auth_collection_policies where auth_collection_id=$1`, collectionID).Scan(&out.Policy.PasswordEnabled, &out.Policy.MinimumPasswordBytes, &out.Policy.MaximumPasswordBytes, &out.Policy.PasswordPattern, &out.Policy.IdentityFields, &out.Policy.MFAEnabled, &out.Policy.MFADurationSeconds, &out.Policy.MFARule, &out.Policy.OTPEnabled, &out.Policy.OTPDurationSeconds, &out.Policy.OTPMaxAttempts); errors.Is(err, pgx.ErrNoRows) {
		return out, ErrNotFound
	} else if err != nil {
		return out, err
	}
	rows, err := s.pool.Query(ctx, `select name,enabled,authorization_url,token_url,user_info_url,client_id,client_secret_ciphertext is not null,scopes,redirect_uris,subject_field,email_field,email_verified_field,avatar_field,allow_insecure_loopback from pgbase_meta.auth_oauth_providers where auth_collection_id=$1 order by name limit 32`, collectionID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var p OAuthProviderConfig
		var scopes, redirects []byte
		if err := rows.Scan(&p.Name, &p.Enabled, &p.AuthorizationURL, &p.TokenURL, &p.UserInfoURL, &p.ClientID, &p.SecretConfigured, &scopes, &redirects, &p.SubjectField, &p.EmailField, &p.EmailVerifiedField, &p.AvatarField, &p.AllowInsecureLoopback); err != nil {
			return out, err
		}
		if json.Unmarshal(scopes, &p.Scopes) != nil || json.Unmarshal(redirects, &p.RedirectURIs) != nil {
			return out, errors.New("invalid stored OAuth configuration")
		}
		if p.Scopes == nil {
			p.Scopes = []string{}
		}
		if p.RedirectURIs == nil {
			p.RedirectURIs = []string{}
		}
		out.Providers = append(out.Providers, p)
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	rows, err = s.pool.Query(ctx, `select purpose,subject,body,html_body from pgbase_meta.auth_mail_templates where auth_collection_id=$1 order by purpose`, collectionID)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var t MailTemplate
		if err := rows.Scan(&t.Purpose, &t.Subject, &t.Body, &t.HTMLBody); err != nil {
			return out, err
		}
		out.Templates = append(out.Templates, t)
	}
	var smtp SMTPAdminConfig
	err = s.pool.QueryRow(ctx, `select address,sender,username,password_ciphertext is not null,server_name,implicit_tls,allow_insecure_loopback from pgbase_meta.auth_smtp_configs where auth_collection_id=$1`, collectionID).Scan(&smtp.Address, &smtp.From, &smtp.Username, &smtp.PasswordConfigured, &smtp.ServerName, &smtp.ImplicitTLS, &smtp.AllowInsecureLoopback)
	if err == nil {
		out.SMTP = &smtp
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return out, err
	}
	return out, nil
}

func (s *Service) SetAuthPolicy(ctx context.Context, collectionID uuid.UUID, policy AuthPolicy) (AuthPolicy, error) {
	if policy.MaximumPasswordBytes == 0 {
		policy.MaximumPasswordBytes = maximumPasswordBytes
	}
	if policy.MFADurationSeconds == 0 {
		policy.MFADurationSeconds = 1800
	}
	if policy.IdentityFields == nil {
		policy.IdentityFields = []string{"email"}
	}
	if policy.OTPDurationSeconds == 0 {
		policy.OTPDurationSeconds = 300
	}
	if policy.OTPMaxAttempts == 0 {
		policy.OTPMaxAttempts = 5
	}
	if collectionID == uuid.Nil || policy.MinimumPasswordBytes < minimumPasswordBytes || policy.MinimumPasswordBytes > 128 || policy.MaximumPasswordBytes < policy.MinimumPasswordBytes || policy.MaximumPasswordBytes > 4096 || len(policy.PasswordPattern) > 512 || (policy.PasswordPattern != "" && !validPasswordPattern(policy.PasswordPattern)) || !validIdentityFields(policy.IdentityFields) || policy.MFADurationSeconds < 60 || policy.MFADurationSeconds > 86400 || len(policy.MFARule) > 1024 || policy.OTPDurationSeconds < 60 || policy.OTPDurationSeconds > 900 || policy.OTPMaxAttempts < 1 || policy.OTPMaxAttempts > 10 {
		return AuthPolicy{}, ErrValidation
	}
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var previous AuthPolicy
		if err := tx.QueryRow(ctx, `select password_enabled,minimum_password_bytes,maximum_password_bytes,password_pattern,identity_fields,mfa_enabled,mfa_duration_seconds,mfa_rule,otp_enabled,otp_duration_seconds,otp_max_attempts from pgbase_meta.auth_collection_policies where auth_collection_id=$1 for update`, collectionID).Scan(&previous.PasswordEnabled, &previous.MinimumPasswordBytes, &previous.MaximumPasswordBytes, &previous.PasswordPattern, &previous.IdentityFields, &previous.MFAEnabled, &previous.MFADurationSeconds, &previous.MFARule, &previous.OTPEnabled, &previous.OTPDurationSeconds, &previous.OTPMaxAttempts); errors.Is(err, pgx.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		identityFields, _ := json.Marshal(policy.IdentityFields)
		tag, err := tx.Exec(ctx, `update pgbase_meta.auth_collection_policies set password_enabled=$2,minimum_password_bytes=$3,maximum_password_bytes=$4,password_pattern=$5,identity_fields=$6,mfa_enabled=$7,mfa_duration_seconds=$8,mfa_rule=$9,otp_enabled=$10,otp_duration_seconds=$11,otp_max_attempts=$12,updated_at=CURRENT_TIMESTAMP where auth_collection_id=$1`, collectionID, policy.PasswordEnabled, policy.MinimumPasswordBytes, policy.MaximumPasswordBytes, policy.PasswordPattern, identityFields, policy.MFAEnabled, policy.MFADurationSeconds, policy.MFARule, policy.OTPEnabled, policy.OTPDurationSeconds, policy.OTPMaxAttempts)
		if err != nil {
			return err
		}
		if tag.RowsAffected() != 1 {
			return ErrNotFound
		}
		// A policy tightening takes effect for already-issued sessions too.
		if (previous.PasswordEnabled && !policy.PasswordEnabled) || policy.MinimumPasswordBytes > previous.MinimumPasswordBytes || (!previous.MFAEnabled && policy.MFAEnabled) || (previous.OTPEnabled && !policy.OTPEnabled) {
			_, err = tx.Exec(ctx, `update pgbase_meta.user_sessions set revoked_at=coalesce(revoked_at,CURRENT_TIMESTAMP) where user_id in (select id from pgbase_meta.users where auth_collection_id=$1)`, collectionID)
		}
		if err == nil && !policy.MFAEnabled {
			_, err = tx.Exec(ctx, `delete from pgbase_meta.user_totp_recovery_codes where user_id in (select id from pgbase_meta.users where auth_collection_id=$1)`, collectionID)
			if err == nil {
				_, err = tx.Exec(ctx, `delete from pgbase_meta.user_totp_factors where user_id in (select id from pgbase_meta.users where auth_collection_id=$1)`, collectionID)
			}
		}
		if err == nil && previous.OTPEnabled && !policy.OTPEnabled {
			_, err = tx.Exec(ctx, `update pgbase_meta.user_email_otps set consumed_at=CURRENT_TIMESTAMP where auth_collection_id=$1 and consumed_at is null`, collectionID)
		}
		return err
	})
	return policy, err
}

func validPasswordPattern(pattern string) bool { _, err := regexp.Compile(pattern); return err == nil }
func validIdentityFields(fields []string) bool {
	if len(fields) == 0 || len(fields) > 8 {
		return false
	}
	seen := map[string]bool{}
	for _, field := range fields {
		if field != "email" && !authCollectionName.MatchString(field) || seen[field] {
			return false
		}
		seen[field] = true
	}
	return true
}

func validateStrings(values []string, maxItems, maxBytes int) bool {
	if len(values) > maxItems {
		return false
	}
	for _, v := range values {
		if v == "" || len(v) > maxBytes {
			return false
		}
	}
	return true
}

func (s *Service) PutOAuthProvider(ctx context.Context, collectionID uuid.UUID, p OAuthProviderConfig) (OAuthProviderConfig, error) {
	if collectionID == uuid.Nil || !validProviderName(p.Name) || !validateStrings(p.Scopes, 32, 128) || !validateStrings(p.RedirectURIs, 16, 2048) {
		return p, ErrValidation
	}
	if p.Scopes == nil {
		p.Scopes = []string{}
	}
	if p.RedirectURIs == nil {
		p.RedirectURIs = []string{}
	}
	provider, err := NewHTTPOAuthProvider(HTTPOAuthConfig{ProviderName: p.Name, AuthorizationURL: p.AuthorizationURL, TokenURL: p.TokenURL, UserInfoURL: p.UserInfoURL, ClientID: p.ClientID, ClientSecret: p.ClientSecret, Scopes: p.Scopes, SubjectField: p.SubjectField, EmailField: p.EmailField, EmailVerifiedField: p.EmailVerifiedField, AvatarField: p.AvatarField, AllowInsecureLoopback: p.AllowInsecureLoopback})
	if err != nil {
		return p, err
	}
	_ = provider
	for _, redirect := range p.RedirectURIs {
		if !validEndpoint(redirect, p.AllowInsecureLoopback) {
			return p, ErrValidation
		}
	}
	scopes, _ := json.Marshal(p.Scopes)
	redirects, _ := json.Marshal(p.RedirectURIs)
	var encrypted []byte
	if p.ClientSecret != "" {
		encrypted, err = s.encrypt("oauth-client-secret", []byte(p.ClientSecret))
		if err != nil {
			return p, err
		}
	}
	err = s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if encrypted == nil {
			return tx.QueryRow(ctx, `insert into pgbase_meta.auth_oauth_providers(auth_collection_id,name,enabled,authorization_url,token_url,user_info_url,client_id,scopes,redirect_uris,subject_field,email_field,email_verified_field,avatar_field,allow_insecure_loopback) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) on conflict(auth_collection_id,name) do update set enabled=excluded.enabled,authorization_url=excluded.authorization_url,token_url=excluded.token_url,user_info_url=excluded.user_info_url,client_id=excluded.client_id,scopes=excluded.scopes,redirect_uris=excluded.redirect_uris,subject_field=excluded.subject_field,email_field=excluded.email_field,email_verified_field=excluded.email_verified_field,avatar_field=excluded.avatar_field,allow_insecure_loopback=excluded.allow_insecure_loopback,updated_at=CURRENT_TIMESTAMP returning client_secret_ciphertext is not null`, collectionID, p.Name, p.Enabled, p.AuthorizationURL, p.TokenURL, p.UserInfoURL, p.ClientID, scopes, redirects, p.SubjectField, p.EmailField, p.EmailVerifiedField, p.AvatarField, p.AllowInsecureLoopback).Scan(&p.SecretConfigured)
		}
		_, err := tx.Exec(ctx, `insert into pgbase_meta.auth_oauth_providers(auth_collection_id,name,enabled,authorization_url,token_url,user_info_url,client_id,client_secret_ciphertext,scopes,redirect_uris,subject_field,email_field,email_verified_field,avatar_field,allow_insecure_loopback) values($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15) on conflict(auth_collection_id,name) do update set enabled=excluded.enabled,authorization_url=excluded.authorization_url,token_url=excluded.token_url,user_info_url=excluded.user_info_url,client_id=excluded.client_id,client_secret_ciphertext=excluded.client_secret_ciphertext,scopes=excluded.scopes,redirect_uris=excluded.redirect_uris,subject_field=excluded.subject_field,email_field=excluded.email_field,email_verified_field=excluded.email_verified_field,avatar_field=excluded.avatar_field,allow_insecure_loopback=excluded.allow_insecure_loopback,updated_at=CURRENT_TIMESTAMP`, collectionID, p.Name, p.Enabled, p.AuthorizationURL, p.TokenURL, p.UserInfoURL, p.ClientID, encrypted, scopes, redirects, p.SubjectField, p.EmailField, p.EmailVerifiedField, p.AvatarField, p.AllowInsecureLoopback)
		p.SecretConfigured = true
		return err
	})
	p.ClientSecret = ""
	return p, err
}

func (s *Service) DeleteOAuthProvider(ctx context.Context, collectionID uuid.UUID, name string) error {
	if collectionID == uuid.Nil || !validProviderName(name) {
		return ErrValidation
	}
	tag, err := s.pool.Exec(ctx, `delete from pgbase_meta.auth_oauth_providers where auth_collection_id=$1 and name=$2`, collectionID, name)
	if err == nil && tag.RowsAffected() != 1 {
		return ErrNotFound
	}
	return err
}

func validTemplate(t MailTemplate) bool {
	if t.Purpose != "verify" && t.Purpose != "password_reset" && t.Purpose != "email_change" && t.Purpose != "otp" && t.Purpose != "auth_alert" {
		return false
	}
	if len(t.Subject) < 1 || len(t.Subject) > 200 || strings.ContainsAny(t.Subject, "\r\n") || len(t.Body) < 1 || len(t.Body) > 16384 || len(t.HTMLBody) > 32768 {
		return false
	}
	placeholder := "{{action_url}}"
	if t.Purpose == "otp" {
		placeholder = "{{otp}}"
	}
	if t.Purpose == "auth_alert" {
		placeholder = "{{alert}}"
	}
	if strings.Count(t.Body, placeholder) == 0 || t.HTMLBody != "" && strings.Count(t.HTMLBody, placeholder) == 0 {
		return false
	}
	cleaned := strings.ReplaceAll(strings.ReplaceAll(t.Body+t.HTMLBody, placeholder, ""), "{{otp_id}}", "")
	return !strings.Contains(cleaned, "{{") && !strings.Contains(cleaned, "}}")
}
func (s *Service) PutMailTemplate(ctx context.Context, collectionID uuid.UUID, t MailTemplate) (MailTemplate, error) {
	if collectionID == uuid.Nil || !validTemplate(t) {
		return t, ErrValidation
	}
	_, err := s.pool.Exec(ctx, `insert into pgbase_meta.auth_mail_templates(auth_collection_id,purpose,subject,body,html_body) values($1,$2,$3,$4,$5) on conflict(auth_collection_id,purpose) do update set subject=excluded.subject,body=excluded.body,html_body=excluded.html_body,updated_at=CURRENT_TIMESTAMP`, collectionID, t.Purpose, t.Subject, t.Body, t.HTMLBody)
	return t, err
}

func (s *Service) PreviewMailTemplate(_ context.Context, t MailTemplate, actionURL string) (mailer.Message, error) {
	if !validTemplate(t) || !validEndpoint(actionURL, true) {
		return mailer.Message{}, ErrValidation
	}
	replace := func(v string) string {
		v = strings.ReplaceAll(v, "{{action_url}}", actionURL)
		v = strings.ReplaceAll(v, "{{otp}}", "123456")
		v = strings.ReplaceAll(v, "{{alert}}", "A sign-in occurred on your account.")
		return strings.ReplaceAll(v, "{{otp_id}}", "00000000-0000-4000-8000-000000000000")
	}
	message := mailer.Message{Subject: t.Subject, Text: replace(t.Body)}
	if t.HTMLBody != "" {
		var err error
		message.HTML, err = mailer.WrapHTML(replace(t.HTMLBody))
		if err != nil {
			return mailer.Message{}, ErrValidation
		}
	}
	return message, nil
}

func (s *Service) PutSMTPConfig(ctx context.Context, collectionID uuid.UUID, c SMTPAdminConfig) (SMTPAdminConfig, error) {
	if collectionID == uuid.Nil {
		return c, ErrValidation
	}
	sender, err := mailer.NewSMTP(mailer.SMTPConfig{Address: c.Address, From: c.From, Username: c.Username, Password: c.Password, ServerName: c.ServerName, ImplicitTLS: c.ImplicitTLS, AllowInsecure: c.AllowInsecureLoopback, Timeout: 10 * time.Second})
	if err != nil {
		return c, ErrValidation
	}
	_ = sender
	var encrypted []byte
	if c.Password != "" {
		encrypted, err = s.encrypt("smtp-password", []byte(c.Password))
		if err != nil {
			return c, err
		}
	}
	if encrypted == nil {
		err = s.pool.QueryRow(ctx, `insert into pgbase_meta.auth_smtp_configs(auth_collection_id,address,sender,username,server_name,implicit_tls,allow_insecure_loopback) values($1,$2,$3,$4,$5,$6,$7) on conflict(auth_collection_id) do update set address=excluded.address,sender=excluded.sender,username=excluded.username,server_name=excluded.server_name,implicit_tls=excluded.implicit_tls,allow_insecure_loopback=excluded.allow_insecure_loopback,updated_at=CURRENT_TIMESTAMP returning password_ciphertext is not null`, collectionID, c.Address, c.From, c.Username, c.ServerName, c.ImplicitTLS, c.AllowInsecureLoopback).Scan(&c.PasswordConfigured)
	} else {
		_, err = s.pool.Exec(ctx, `insert into pgbase_meta.auth_smtp_configs(auth_collection_id,address,sender,username,password_ciphertext,server_name,implicit_tls,allow_insecure_loopback) values($1,$2,$3,$4,$5,$6,$7,$8) on conflict(auth_collection_id) do update set address=excluded.address,sender=excluded.sender,username=excluded.username,password_ciphertext=excluded.password_ciphertext,server_name=excluded.server_name,implicit_tls=excluded.implicit_tls,allow_insecure_loopback=excluded.allow_insecure_loopback,updated_at=CURRENT_TIMESTAMP`, collectionID, c.Address, c.From, c.Username, encrypted, c.ServerName, c.ImplicitTLS, c.AllowInsecureLoopback)
		c.PasswordConfigured = true
	}
	c.Password = ""
	return c, err
}

func (s *Service) TestSMTP(ctx context.Context, collectionID uuid.UUID, recipient string) error {
	a, err := mail.ParseAddress(recipient)
	if err != nil || a.Name != "" || a.Address != recipient {
		return ErrValidation
	}
	sender, err := s.smtpSender(ctx, collectionID)
	if err != nil {
		return err
	}
	return sender.Send(ctx, mailer.Message{Purpose: "smtp_test", To: recipient, Subject: "PgBase SMTP test", Text: "Your PgBase SMTP configuration is working."})
}

func (s *Service) SetUserVerified(ctx context.Context, id uuid.UUID, verified bool) (ManagedUser, error) {
	if id == uuid.Nil {
		return ManagedUser{}, ErrValidation
	}
	var u ManagedUser
	err := s.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		if err := lockManagedUser(ctx, tx, id, &u); err != nil {
			return err
		}
		if err := tx.QueryRow(ctx, `update pgbase_meta.users set verified=$2,updated_at=CURRENT_TIMESTAMP where id=$1 returning updated_at`, id, verified).Scan(&u.UpdatedAt); err != nil {
			return err
		}
		u.Verified = verified
		if !verified {
			_, err := revokeSessionsInTx(ctx, tx, id)
			return err
		}
		return nil
	})
	return u, err
}
func (s *Service) AdminRequestVerification(ctx context.Context, id uuid.UUID) error {
	return s.RequestVerification(ctx, id)
}
func (s *Service) AdminRequestPasswordReset(ctx context.Context, id uuid.UUID) error {
	u, _, err := userPasswordByIDDB(ctx, s.pool, id, false)
	if err != nil {
		return ErrNotFound
	}
	return s.RequestPasswordResetInCollection(ctx, u.AuthCollectionID, u.Email, "admin-local")
}

func (s *Service) authPolicy(ctx context.Context, collectionID uuid.UUID) (AuthPolicy, error) {
	var p AuthPolicy
	err := s.pool.QueryRow(ctx, `select password_enabled,minimum_password_bytes,maximum_password_bytes,password_pattern,identity_fields,mfa_enabled,mfa_duration_seconds,mfa_rule,otp_enabled,otp_duration_seconds,otp_max_attempts from pgbase_meta.auth_collection_policies where auth_collection_id=$1`, collectionID).Scan(&p.PasswordEnabled, &p.MinimumPasswordBytes, &p.MaximumPasswordBytes, &p.PasswordPattern, &p.IdentityFields, &p.MFAEnabled, &p.MFADurationSeconds, &p.MFARule, &p.OTPEnabled, &p.OTPDurationSeconds, &p.OTPMaxAttempts)
	return p, err
}
func (s *Service) smtpSender(ctx context.Context, collectionID uuid.UUID) (sender mailer.Sender, resultErr error) {
	defer func() {
		if resultErr == nil && sender != nil && s.decorateMailer != nil {
			sender = s.decorateMailer(sender)
		}
	}()
	var c mailer.SMTPConfig
	var encrypted []byte
	err := s.pool.QueryRow(ctx, `select address,sender,username,password_ciphertext,server_name,implicit_tls,allow_insecure_loopback from pgbase_meta.auth_smtp_configs where auth_collection_id=$1`, collectionID).Scan(&c.Address, &c.From, &c.Username, &encrypted, &c.ServerName, &c.ImplicitTLS, &c.AllowInsecure)
	if errors.Is(err, pgx.ErrNoRows) {
		if s.mailer == nil {
			return nil, errors.New("user email actions unavailable")
		}
		return s.mailer, nil
	}
	if err != nil {
		return nil, err
	}
	if encrypted != nil {
		plain, err := s.decrypt("smtp-password", encrypted)
		if err != nil {
			return nil, err
		}
		c.Password = string(plain)
	}
	c.Timeout = 10 * time.Second
	return mailer.NewSMTP(c)
}

func (s *Service) configuredOAuthProvider(ctx context.Context, collectionID uuid.UUID, name string) (OAuthProvider, map[string]struct{}, error) {
	var c HTTPOAuthConfig
	c.ProviderName = name
	var encrypted, scopesRaw, redirectsRaw []byte
	err := s.pool.QueryRow(ctx, `select authorization_url,token_url,user_info_url,client_id,client_secret_ciphertext,scopes,redirect_uris,subject_field,email_field,email_verified_field,avatar_field,allow_insecure_loopback from pgbase_meta.auth_oauth_providers where auth_collection_id=$1 and name=$2 and enabled`, collectionID, name).Scan(&c.AuthorizationURL, &c.TokenURL, &c.UserInfoURL, &c.ClientID, &encrypted, &scopesRaw, &redirectsRaw, &c.SubjectField, &c.EmailField, &c.EmailVerifiedField, &c.AvatarField, &c.AllowInsecureLoopback)
	if errors.Is(err, pgx.ErrNoRows) {
		p, ok := s.oauthProviders[name]
		if !ok {
			return nil, nil, ErrValidation
		}
		return p, s.oauthRedirects, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if encrypted != nil {
		plain, err := s.decrypt("oauth-client-secret", encrypted)
		if err != nil {
			return nil, nil, err
		}
		c.ClientSecret = string(plain)
	}
	var redirects []string
	if json.Unmarshal(scopesRaw, &c.Scopes) != nil || json.Unmarshal(redirectsRaw, &redirects) != nil {
		return nil, nil, ErrValidation
	}
	p, err := NewHTTPOAuthProvider(c)
	if err != nil {
		return nil, nil, err
	}
	allowed := make(map[string]struct{}, len(redirects))
	for _, v := range redirects {
		allowed[v] = struct{}{}
	}
	return p, allowed, nil
}
