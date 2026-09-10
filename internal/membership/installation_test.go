package membership

import (
	"errors"
	"testing"

	"github.com/yakisoba0728/PgBase/migrations/system"
)

func TestMembershipHistoryAcceptsCompleteKnownFuturePrefix(t *testing.T) {
	embedded := system.Migrations()
	future := system.Migration{Version: 8, Name: "future"}
	future.SHA256[0] = 0x88
	embedded = append(embedded, future)
	applied := make([]membershipMigrationHistory, 7)
	for index, migration := range embedded[:7] {
		applied[index] = membershipMigrationHistory{
			version: migration.Version, checksum: append([]byte(nil), migration.SHA256[:]...),
		}
	}
	if err := validateMembershipHistoryPrefix(applied, embedded, len(embedded)); err != nil {
		t.Fatalf("clean v7 prefix rejected by future binary: %v", err)
	}
	if err := validateMembershipHistoryPrefix(applied[:6], embedded, 6); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("future binary accepted pre-membership v6 history: %v", err)
	}
}
