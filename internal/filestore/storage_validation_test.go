package filestore

import (
	"errors"
	"github.com/yakisoba0728/PgBase/internal/validation"
	"testing"
)

func TestStorageValidationIdentifiesInvalidFields(t *testing.T) {
	c := &StorageController{localDirectory: "/files"}
	for _, test := range []struct {
		config StorageSettings
		field  string
	}{
		{StorageSettings{Driver: "unknown"}, "driver"},
		{StorageSettings{Driver: "s3", Region: "region"}, "bucket"},
		{StorageSettings{Driver: "s3", Bucket: "bucket"}, "region"},
		{StorageSettings{Driver: "s3", Bucket: "bucket", Region: "region", Endpoint: "https://user:secret@host"}, "endpoint"},
	} {
		err := c.validate(test.config)
		var invalid *validation.Error
		if !errors.Is(err, ErrInvalid) || !errors.As(err, &invalid) || invalid.Details[0].Field != test.field {
			t.Fatalf("field=%s error=%v", test.field, err)
		}
	}
}
