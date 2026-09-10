package pgbase

import (
	"context"

	"github.com/yakisoba0728/PgBase/internal/api"
	"github.com/yakisoba0728/PgBase/internal/filestore"
)

const (
	HookStorageSettingsList   = "storage.settings.list"
	HookStorageSettingsUpdate = "storage.settings.update"
	HookStorageSettingsTest   = "storage.settings.test"
)

// StorageSettingsEvent deliberately excludes access and secret keys.
type StorageSettingsEvent struct {
	Driver       string
	Bucket       string
	Region       string
	Endpoint     string
	UsePathStyle bool
	Revision     int64
	Result       StorageSettingsResult
}

type StorageSettingsResult struct {
	Driver, Bucket, Region, Endpoint string
	UsePathStyle, SecretConfigured   bool
	Revision                         int64
}

func safeStorageSettings(value filestore.StorageSettings) StorageSettingsResult {
	return StorageSettingsResult{Driver: value.Driver, Bucket: value.Bucket, Region: value.Region, Endpoint: value.Endpoint, UsePathStyle: value.UsePathStyle, SecretConfigured: value.SecretConfigured, Revision: value.Revision}
}

type hookedStorageAdministration struct {
	service  api.StorageAdministration
	registry *HookRegistry
}

func (h hookedStorageAdministration) begin(ctx context.Context, kind string, event *StorageSettingsEvent) (func(error), error) {
	return h.registry.Begin(ctx, &HookEvent{Kind: kind, Data: event})
}
func (h hookedStorageAdministration) Settings(ctx context.Context) (result filestore.StorageSettings, err error) {
	event := &StorageSettingsEvent{}
	finish, err := h.begin(ctx, HookStorageSettingsList, event)
	defer func() { finish(err) }()
	if err != nil {
		return result, err
	}
	result, err = h.service.Settings(ctx)
	if err == nil {
		event.Result = safeStorageSettings(result)
	}
	return result, err
}
func (h hookedStorageAdministration) Update(ctx context.Context, input filestore.StorageUpdate) (result filestore.StorageSettings, err error) {
	event := &StorageSettingsEvent{Driver: input.Driver, Bucket: input.Bucket, Region: input.Region, Endpoint: input.Endpoint, UsePathStyle: input.UsePathStyle, Revision: input.Revision}
	finish, err := h.begin(ctx, HookStorageSettingsUpdate, event)
	defer func() { finish(err) }()
	if err != nil {
		return result, err
	}
	result, err = h.service.Update(ctx, input)
	if err == nil {
		event.Result = safeStorageSettings(result)
	}
	return result, err
}
func (h hookedStorageAdministration) Test(ctx context.Context) (err error) {
	event := &StorageSettingsEvent{}
	finish, err := h.begin(ctx, HookStorageSettingsTest, event)
	defer func() { finish(err) }()
	if err != nil {
		return err
	}
	return h.service.Test(ctx)
}
