package filestore

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestStorageControllerDoesNotUseForeignLeaseAfterCancellation(t *testing.T) {
	for _, operation := range []string{"put", "open", "delete"} {
		t.Run(operation, func(t *testing.T) {
			foreign := &StorageController{}
			target := &StorageController{}
			store := &migrationMemoryStore{values: map[string][]byte{"objects/key": []byte("foreign")}}
			ctx, cancel := context.WithCancel(context.WithValue(t.Context(), storageLeaseContextKey{}, storageLease{controller: foreign, store: store}))
			cancel()
			// With no available operation slot, the target must honor its own
			// canceled admission rather than borrowing another controller's store.
			var err error
			switch operation {
			case "put":
				err = target.Put(ctx, "objects/key", strings.NewReader("replacement"))
			case "open":
				var reader io.ReadCloser
				reader, err = target.Open(ctx, "objects/key")
				if reader != nil {
					_ = reader.Close()
				}
			case "delete":
				err = target.Delete(ctx, "objects/key")
			}
			if !errors.Is(err, context.Canceled) {
				t.Errorf("foreign lease bypassed target admission: %v", err)
			}
			if got := string(store.values["objects/key"]); got != "foreign" {
				t.Errorf("foreign object changed to %q", got)
			}
		})
	}
}
