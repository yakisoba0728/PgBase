//go:build integration

package userauth

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/recordidentity"
)

// TestAuthenticateProjectsIdentityFromTheSessionRead covers the auth identity
// now joined into the session statement: the public ID is the stored mapping,
// and a session whose identity row is missing still fails exactly as the
// former separate PublicAuth lookup did.
func TestAuthenticateProjectsIdentityFromTheSessionRead(t *testing.T) {
	pool := newUserAuthPool(t)
	service := NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	ctx := context.Background()
	registered, err := service.Register(ctx, "identity-join@example.com", "correct horse battery staple", "192.0.2.10")
	if err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := pool.QueryRow(ctx, `select external_id from pgbase_meta.record_identities
		where collection_kind='auth' and collection_id=$1 and internal_id=$2`,
		registered.User.AuthCollectionID, registered.User.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	principal, err := service.Authenticate(ctx, registered.RawToken)
	if err != nil {
		t.Fatal(err)
	}
	if principal.User.PublicID != stored || principal.User != registered.User {
		t.Fatalf("principal user = %#v, want public ID %q and %#v", principal.User, stored, registered.User)
	}

	if _, err := pool.Exec(ctx, `delete from pgbase_meta.record_identities
		where collection_kind='auth' and collection_id=$1 and internal_id=$2`,
		registered.User.AuthCollectionID, registered.User.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Authenticate(ctx, registered.RawToken); !errors.Is(err, recordidentity.ErrNotFound) {
		t.Fatalf("Authenticate without identity = %v, want recordidentity.ErrNotFound", err)
	}
}
