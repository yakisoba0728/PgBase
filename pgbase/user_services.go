package pgbase

import (
	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/userauth"
	"time"
)

// withUserServices installs the public identity capability as one unit, so the
// bearer routes cannot be exposed without the collection policy service.
func withUserServices(dependencies api.Dependencies, pool *database.Pool, options Options) api.Dependencies {
	dependencies.UserAuth = userauth.NewService(pool, adminauth.NewArgon2idPasswords(), options.TokenSecret, 24*time.Hour)
	dependencies.RecordAccess = recordaccess.NewService(pool)
	dependencies.UserRegistrationEnabled = options.UserRegistrationEnabled
	return dependencies
}
