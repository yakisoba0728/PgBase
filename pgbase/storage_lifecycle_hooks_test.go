package pgbase

import (
	"context"
	"errors"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/filestore"
)

type storageAdminStub struct{ settings, updates, tests int }

func (s *storageAdminStub) Settings(context.Context) (filestore.StorageSettings, error) {
	s.settings++
	return filestore.StorageSettings{Driver: "local", Revision: 2}, nil
}
func (s *storageAdminStub) Update(context.Context, filestore.StorageUpdate) (filestore.StorageSettings, error) {
	s.updates++
	return filestore.StorageSettings{Driver: "local", Revision: 3}, nil
}
func (s *storageAdminStub) Test(context.Context) error { s.tests++; return nil }

func TestStorageLifecycleVetoAndSecretFreePayload(t *testing.T) {
	registry, service := &HookRegistry{}, &storageAdminStub{}
	veto := errors.New("veto")
	var payload *StorageSettingsEvent
	_, err := registry.On(HookStorageSettingsUpdate, Hook{Before: func(_ context.Context, event *HookEvent) error {
		payload = event.Data.(*StorageSettingsEvent)
		return veto
	}})
	if err != nil {
		t.Fatal(err)
	}
	hooked := hookedStorageAdministration{service: service, registry: registry}
	_, err = hooked.Update(context.Background(), filestore.StorageUpdate{Driver: "s3", AccessKey: "access-secret", SecretKey: "private-secret", Revision: 2})
	if !errors.Is(err, veto) || service.updates != 0 || payload == nil || payload.Driver != "s3" {
		t.Fatalf("err=%v updates=%d payload=%+v", err, service.updates, payload)
	}
	if _, err := hooked.Settings(context.Background()); err != nil || service.settings != 1 {
		t.Fatalf("Settings err=%v calls=%d", err, service.settings)
	}
	if err := hooked.Test(context.Background()); err != nil || service.tests != 1 {
		t.Fatalf("Test err=%v calls=%d", err, service.tests)
	}
}
