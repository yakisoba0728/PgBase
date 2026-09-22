// Package runtimesettings persists the administrator-controlled operational
// settings that can safely change while a PgBase process is serving traffic.
package runtimesettings

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/yakisoba0728/PgBase/internal/database"
)

var ErrConflict = errors.New("runtime settings changed")
var ErrInvalid = errors.New("invalid runtime settings")

type Batch struct {
	Enabled      bool  `json:"enabled"`
	MaxRequests  int   `json:"maxRequests"`
	MaxBodyBytes int64 `json:"maxBodyBytes"`
	TimeoutMS    int   `json:"timeoutMs"`
}
type Logs struct {
	RetentionDays int    `json:"retentionDays"`
	Level         string `json:"level"`
	IncludeIP     bool   `json:"includeIP"`
	IncludeAuth   bool   `json:"includeAuth"`
}
type Proxy struct {
	TrustedProxyCIDRs []string `json:"trustedProxyCIDRs"`
}
type Backups struct {
	Schedule string `json:"schedule"`
	Timezone string `json:"timezone"`
	Enabled  bool   `json:"enabled"`
	Keep     int    `json:"keep"`
}
type Application struct {
	Theme    string `json:"theme"`
	Accent   string `json:"accent"`
	Branding string `json:"branding"`
}
type AuthRatePolicy struct {
	AccountMaxFailures int `json:"accountMaxFailures"`
	NetworkMaxFailures int `json:"networkMaxFailures"`
	WindowSeconds      int `json:"windowSeconds"`
	BlockSeconds       int `json:"blockSeconds"`
}
type UserRatePolicy struct {
	AccountMaxFailures             int `json:"accountMaxFailures"`
	NetworkMaxFailures             int `json:"networkMaxFailures"`
	RegistrationNetworkMaxFailures int `json:"registrationNetworkMaxFailures"`
	WindowSeconds                  int `json:"windowSeconds"`
	BlockSeconds                   int `json:"blockSeconds"`
}
type RateLimits struct {
	Admin       AuthRatePolicy `json:"admin"`
	User        UserRatePolicy `json:"user"`
	Enabled     bool           `json:"enabled"`
	Rules       []APIRateRule  `json:"rules"`
	ExcludedIPs []string       `json:"excludedIPs"`
}

// APIRateRule matches a registered route pattern, optionally prefixed by an
// uppercase method. A trailing slash matches a route-pattern prefix.
type APIRateRule struct {
	Label       string `json:"label"`
	Audience    string `json:"audience"`
	Duration    int64  `json:"duration"`
	MaxRequests int    `json:"maxRequests"`
}
type Settings struct {
	Batch       Batch       `json:"batch"`
	Logs        Logs        `json:"logs"`
	Proxy       Proxy       `json:"proxy"`
	Backups     Backups     `json:"backups"`
	Application Application `json:"application"`
	RateLimits  RateLimits  `json:"rateLimits"`
	Revision    int64       `json:"revision"`
}

// MaximumBatchBodyBytes preserves the existing 100 MiB file plus 48 MiB
// escaped record document allowance, including the JSON framing reserve.
const MaximumBatchBodyBytes int64 = 148<<20 + 4<<10

func Default() Settings {
	return Settings{Batch: Batch{Enabled: true, MaxRequests: 50, MaxBodyBytes: MaximumBatchBodyBytes, TimeoutMS: 30000}, Logs: Logs{RetentionDays: 7, Level: "info"}, Backups: Backups{Schedule: "@every 24h", Timezone: "UTC"}, Application: Application{Theme: "system"}, RateLimits: RateLimits{Admin: AuthRatePolicy{AccountMaxFailures: 5, NetworkMaxFailures: 20, WindowSeconds: 900, BlockSeconds: 900}, User: UserRatePolicy{AccountMaxFailures: 5, NetworkMaxFailures: 20, RegistrationNetworkMaxFailures: 5, WindowSeconds: 900, BlockSeconds: 900}}}
}

func validate(v Settings) error {
	if err := validateStored(v); err != nil {
		return err
	}
	for _, rule := range v.RateLimits.Rules {
		if !validAPIRateLabel(rule.Label) {
			return ErrInvalid
		}
	}
	return nil
}

// Preserve reload compatibility with previously accepted labels. New writes
// must repair them, but upgrading must leave settings readable to operators.
func validateStored(v Settings) error {
	if v.Batch.MaxRequests < 1 || v.Batch.MaxRequests > 100 || v.Batch.MaxBodyBytes < 1024 || v.Batch.MaxBodyBytes > MaximumBatchBodyBytes || v.Batch.TimeoutMS < 100 || v.Batch.TimeoutMS > 300000 {
		return ErrInvalid
	}
	if v.Logs.RetentionDays < 1 || v.Logs.RetentionDays > 3650 || (v.Logs.Level != "debug" && v.Logs.Level != "info" && v.Logs.Level != "warn" && v.Logs.Level != "error") {
		return ErrInvalid
	}
	if v.Backups.Keep < 0 || v.Backups.Keep > 10000 || strings.TrimSpace(v.Backups.Timezone) == "" || strings.TrimSpace(v.Backups.Schedule) == "" {
		return ErrInvalid
	}
	if v.Application.Theme != "system" && v.Application.Theme != "light" && v.Application.Theme != "dark" {
		return ErrInvalid
	}
	if v.RateLimits.Admin.AccountMaxFailures < 1 || v.RateLimits.Admin.AccountMaxFailures > 10000 || v.RateLimits.Admin.NetworkMaxFailures < 1 || v.RateLimits.Admin.NetworkMaxFailures > 10000 || v.RateLimits.Admin.WindowSeconds < 1 || v.RateLimits.Admin.WindowSeconds > 86400 || v.RateLimits.Admin.BlockSeconds < 1 || v.RateLimits.Admin.BlockSeconds > 604800 || v.RateLimits.User.AccountMaxFailures < 1 || v.RateLimits.User.AccountMaxFailures > 10000 || v.RateLimits.User.NetworkMaxFailures < 1 || v.RateLimits.User.NetworkMaxFailures > 10000 || v.RateLimits.User.RegistrationNetworkMaxFailures < 1 || v.RateLimits.User.RegistrationNetworkMaxFailures > 10000 || v.RateLimits.User.WindowSeconds < 1 || v.RateLimits.User.WindowSeconds > 86400 || v.RateLimits.User.BlockSeconds < 1 || v.RateLimits.User.BlockSeconds > 604800 {
		return ErrInvalid
	}
	if len(v.RateLimits.Rules) > 100 || len(v.RateLimits.ExcludedIPs) > 100 {
		return ErrInvalid
	}
	seen := make(map[string]bool, len(v.RateLimits.Rules))
	for _, rule := range v.RateLimits.Rules {
		if len(rule.Label) == 0 || len(rule.Label) > 256 || (rule.Audience != "" && rule.Audience != "@guest" && rule.Audience != "@auth") || rule.Duration < 1 || rule.Duration > 86400 || rule.MaxRequests < 1 || rule.MaxRequests > 100000 || seen[rule.Label+"\x00"+rule.Audience] {
			return ErrInvalid
		}
		seen[rule.Label+"\x00"+rule.Audience] = true
	}
	for _, raw := range v.RateLimits.ExcludedIPs {
		if _, err := netip.ParsePrefix(raw); err != nil {
			if _, err := netip.ParseAddr(raw); err != nil {
				return ErrInvalid
			}
		}
	}
	for _, raw := range v.Proxy.TrustedProxyCIDRs {
		p, err := netip.ParsePrefix(raw)
		if err != nil || p != p.Masked() || p.Bits() == 0 {
			return ErrInvalid
		}
	}
	return nil
}

type Controller struct {
	pool       *database.Pool
	key        [32]byte
	ciphertext []byte // ciphertext for the locally published snapshot
	mu         sync.RWMutex
	notifyMu   sync.Mutex
	value      Settings
	listeners  []func(Settings)
}

const encryptionDomain = "pgbase-runtime-settings-v1"

func New(ctx context.Context, pool *database.Pool, encryptionKey []byte) (*Controller, error) {
	if pool == nil || len(encryptionKey) < 32 {
		return nil, ErrInvalid
	}
	c := &Controller{pool: pool, key: deriveKey(encryptionKey)}
	v := Default()
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	ciphertext, err := c.encrypt(raw)
	if err != nil {
		return nil, err
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.runtime_settings(singleton,document_ciphertext) values(true,$1) on conflict(singleton) do nothing`, ciphertext); err != nil {
		return nil, err
	}
	if err = c.Reload(ctx); err != nil {
		return nil, err
	}
	return c, nil
}
func (c *Controller) Get() Settings {
	c.mu.RLock()
	defer c.mu.RUnlock()
	v := c.value
	return clone(v)
}
func (c *Controller) Reload(ctx context.Context) error {
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	var ciphertext, legacy []byte
	v := Default()
	var revision int64
	if err := c.pool.QueryRow(ctx, `select document_ciphertext,document,revision from pgbase_meta.runtime_settings where singleton`).Scan(&ciphertext, &legacy, &revision); err != nil {
		return err
	}
	if len(ciphertext) == 0 {
		if len(legacy) == 0 {
			return ErrInvalid
		}
		ciphertext, err := c.encrypt(legacy)
		if err != nil {
			return err
		}
		_, err = c.pool.Exec(ctx, `update pgbase_meta.runtime_settings set document_ciphertext=$1,document=null where singleton and document_ciphertext is null and document=$2::jsonb and revision=$3`, ciphertext, string(legacy), revision)
		if err != nil {
			return err
		}
		// A concurrent process may have completed the one-time conversion with a
		// different ciphertext. Read the authoritative encrypted row below.
		if err := c.pool.QueryRow(ctx, `select document_ciphertext,revision from pgbase_meta.runtime_settings where singleton`).Scan(&ciphertext, &revision); err != nil {
			return err
		}
	}
	raw, err := c.decrypt(ciphertext)
	if err != nil {
		return fmt.Errorf("decrypt runtime settings: %w", err)
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("decode runtime settings: %w", err)
	}
	// The database revision is the concurrency authority.  A document copied
	// from an older process must never overwrite it during JSON decoding.
	v.Revision = revision
	if err := validateStored(v); err != nil {
		return err
	}
	c.mu.RLock()
	stale := v.Revision < c.value.Revision
	c.mu.RUnlock()
	if stale {
		return nil
	}
	c.mu.Lock()
	c.ciphertext = append(c.ciphertext[:0], ciphertext...)
	c.mu.Unlock()
	c.publish(v)
	return nil
}
func (c *Controller) Update(ctx context.Context, candidate Settings) (Settings, error) {
	// Serialize local commits through listener publication. Without this, two
	// successful PATCHes can commit in order but complete their asynchronous
	// runtime reconfiguration callbacks in reverse order.
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	current := c.Get()
	currentCiphertext := append([]byte(nil), c.ciphertext...)
	if candidate.Revision != current.Revision {
		return Settings{}, ErrConflict
	}
	if err := validate(candidate); err != nil {
		return Settings{}, err
	}
	candidate.Revision = 0
	raw, err := json.Marshal(candidate)
	if err != nil {
		return Settings{}, err
	}
	ciphertext, err := c.encrypt(raw)
	if err != nil {
		return Settings{}, err
	}
	var next int64
	err = c.pool.QueryRow(ctx, `update pgbase_meta.runtime_settings set document_ciphertext=$1,document=null,revision=revision+1,updated_at=now() where singleton and revision=$2 and document_ciphertext=$3 returning revision`, ciphertext, current.Revision, currentCiphertext).Scan(&next)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Settings{}, ErrConflict
		}
		return Settings{}, err
	}
	candidate.Revision = next
	c.mu.Lock()
	if candidate.Revision >= c.value.Revision {
		c.ciphertext = append(c.ciphertext[:0], ciphertext...)
	}
	c.mu.Unlock()
	return c.publish(candidate), nil
}

// RotateKey re-encrypts the durable document without changing its revision or
// republishing it. Callers must distribute the replacement key to every
// process before any old-key process reloads the row.
func (c *Controller) RotateKey(ctx context.Context, encryptionKey []byte) error {
	if len(encryptionKey) < 32 {
		return ErrInvalid
	}
	c.notifyMu.Lock()
	defer c.notifyMu.Unlock()
	nextKey := deriveKey(encryptionKey)
	oldKey := c.key
	var reencrypted []byte
	err := c.pool.InTx(ctx, pgx.TxOptions{}, func(tx pgx.Tx) error {
		var ciphertext []byte
		if err := tx.QueryRow(ctx, `select document_ciphertext from pgbase_meta.runtime_settings where singleton for update`).Scan(&ciphertext); err != nil {
			return err
		}
		raw, err := c.decrypt(ciphertext)
		if err != nil {
			return fmt.Errorf("decrypt runtime settings for rotation: %w", err)
		}
		c.key = nextKey
		reencrypted, err = c.encrypt(raw)
		c.key = oldKey
		if err != nil {
			return err
		}
		if _, err = tx.Exec(ctx, `update pgbase_meta.runtime_settings set document_ciphertext=$1,document=null where singleton`, reencrypted); err != nil {
			return err
		}
		return nil
	})
	if err == nil {
		c.key = nextKey
		c.ciphertext = append(c.ciphertext[:0], reencrypted...)
	}
	return err
}

func deriveKey(secret []byte) [32]byte {
	return sha256.Sum256(append(append([]byte(nil), secret...), []byte("\x00"+encryptionDomain)...))
}

func (c *Controller) encrypt(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return aead.Seal(nonce, nonce, plaintext, []byte(encryptionDomain)), nil
}

func (c *Controller) decrypt(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(c.key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < aead.NonceSize() {
		return nil, ErrInvalid
	}
	return aead.Open(nil, ciphertext[:aead.NonceSize()], ciphertext[aead.NonceSize():], []byte(encryptionDomain))
}

func clone(v Settings) Settings {
	v.Proxy.TrustedProxyCIDRs = append([]string(nil), v.Proxy.TrustedProxyCIDRs...)
	v.RateLimits.Rules = append([]APIRateRule(nil), v.RateLimits.Rules...)
	v.RateLimits.ExcludedIPs = append([]string(nil), v.RateLimits.ExcludedIPs...)
	return v
}

// publish installs an owned settings snapshot, then gives every listener and
// caller its own copy. The caller must hold notifyMu so publications retain
// their durable commit order.
func (c *Controller) publish(v Settings) Settings {
	c.mu.Lock()
	c.value = clone(v)
	listeners := append([]func(Settings){}, c.listeners...)
	c.mu.Unlock()
	for _, listener := range listeners {
		listener(clone(v))
	}
	return clone(v)
}

// TrustedProxies returns the validated runtime snapshot in the representation
// consumed by HTTP address resolution.
func (c *Controller) TrustedProxies() []netip.Prefix {
	values := c.Get().Proxy.TrustedProxyCIDRs
	result := make([]netip.Prefix, 0, len(values))
	for _, value := range values {
		prefix, err := netip.ParsePrefix(value)
		if err == nil {
			result = append(result, prefix)
		}
	}
	return result
}

// Subscribe receives committed settings snapshots. Listeners must return
// promptly; a process restart reconciles a transient runtime failure.
func (c *Controller) Subscribe(listener func(Settings)) {
	if listener == nil {
		return
	}
	c.mu.Lock()
	c.listeners = append(c.listeners, listener)
	c.mu.Unlock()
}
