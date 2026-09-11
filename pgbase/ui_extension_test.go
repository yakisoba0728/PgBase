package pgbase

import (
	"testing"
	"testing/fstest"
)

func TestValidateUIExtensionsAcceptsOperatorOwnedFilesystem(t *testing.T) {
	extensions := []UIExtension{{Name: "reports 2", FS: fstest.MapFS{"main.js": {Data: []byte("void 0")}}}}
	if err := validateUIExtensions(extensions); err != nil {
		t.Fatalf("validateUIExtensions: %v", err)
	}
}

func TestValidateUIExtensionsRejectsUnsafeAndDuplicateRegistration(t *testing.T) {
	filesystem := fstest.MapFS{}
	for _, extensions := range [][]UIExtension{
		{{Name: "../escape", FS: filesystem}},
		{{Name: "reports", FS: nil}},
		{{Name: "reports", FS: filesystem}, {Name: "reports", FS: filesystem}},
	} {
		if err := validateUIExtensions(extensions); err == nil {
			t.Fatalf("unsafe extensions accepted: %#v", extensions)
		}
	}
}
