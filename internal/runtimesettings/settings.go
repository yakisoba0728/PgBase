// Package runtimesettings persists the administrator-controlled operational
// settings that can safely change while a PgBase process is serving traffic.
package runtimesettings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"

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
type Settings struct {
	Batch       Batch       `json:"batch"`
	Logs        Logs        `json:"logs"`
	Proxy       Proxy       `json:"proxy"`
	Backups     Backups     `json:"backups"`
	Application Application `json:"application"`
	Revision    int64       `json:"revision"`
}

func Default() Settings {
	return Settings{Batch: Batch{Enabled: true, MaxRequests: 50, MaxBodyBytes: 1 << 20, TimeoutMS: 30000}, Logs: Logs{RetentionDays: 7, Level: "info"}, Backups: Backups{Timezone: "UTC"}, Application: Application{Theme: "system"}}
}

func validate(v Settings) error {
	if v.Batch.MaxRequests < 1 || v.Batch.MaxRequests > 100 || v.Batch.MaxBodyBytes < 1024 || v.Batch.MaxBodyBytes > 100<<20 || v.Batch.TimeoutMS < 100 || v.Batch.TimeoutMS > 300000 {
		return ErrInvalid
	}
	if v.Logs.RetentionDays < 1 || v.Logs.RetentionDays > 3650 || (v.Logs.Level != "debug" && v.Logs.Level != "info" && v.Logs.Level != "warn" && v.Logs.Level != "error") {
		return ErrInvalid
	}
	if v.Backups.Keep < 0 || v.Backups.Keep > 10000 || strings.TrimSpace(v.Backups.Timezone) == "" {
		return ErrInvalid
	}
	if v.Application.Theme != "system" && v.Application.Theme != "light" && v.Application.Theme != "dark" {
		return ErrInvalid
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
	pool  *database.Pool
	mu    sync.RWMutex
	value Settings
}

func New(ctx context.Context, pool *database.Pool) (*Controller, error) {
	if pool == nil {
		return nil, ErrInvalid
	}
	c := &Controller{pool: pool}
	v := Default()
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	if _, err = pool.Exec(ctx, `insert into pgbase_meta.runtime_settings(singleton,document) values(true,$1) on conflict(singleton) do nothing`, raw); err != nil {
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
	v.Proxy.TrustedProxyCIDRs = append([]string(nil), v.Proxy.TrustedProxyCIDRs...)
	return v
}
func (c *Controller) Reload(ctx context.Context) error {
	var raw []byte
	var v Settings
	if err := c.pool.QueryRow(ctx, `select document,revision from pgbase_meta.runtime_settings where singleton`).Scan(&raw, &v.Revision); err != nil {
		return err
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return fmt.Errorf("decode runtime settings: %w", err)
	}
	if err := validate(v); err != nil {
		return err
	}
	c.mu.Lock()
	c.value = v
	c.mu.Unlock()
	return nil
}
func (c *Controller) Update(ctx context.Context, candidate Settings) (Settings, error) {
	current := c.Get()
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
	var next int64
	err = c.pool.QueryRow(ctx, `update pgbase_meta.runtime_settings set document=$1,revision=revision+1,updated_at=now() where singleton and revision=$2 returning revision`, raw, current.Revision).Scan(&next)
	if err != nil {
		return Settings{}, err
	}
	candidate.Revision = next
	c.mu.Lock()
	c.value = candidate
	c.mu.Unlock()
	return candidate, nil
}
