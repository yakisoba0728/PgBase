package pgbase

import (
	"context"
	"errors"
)

// RotateRuntimeSettingsEncryptionKey re-encrypts persisted runtime settings
// with key. It preserves their revision and does not publish a settings change.
// Deploy the replacement key to every process only after this call succeeds.
func (a *App) RotateRuntimeSettingsEncryptionKey(ctx context.Context, key []byte) error {
	if a == nil || a.runtimeSettings == nil {
		return errors.New("runtime settings are unavailable")
	}
	return a.runtimeSettings.RotateKey(ctx, key)
}
