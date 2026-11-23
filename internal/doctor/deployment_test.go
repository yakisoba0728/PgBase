package doctor

import (
	"strings"
	"testing"

	"github.com/yakisoba0728/PgBase/internal/database"
)

func TestPoolWarningsFlagOnlyPoolsBelowTheRecommendedMinimum(t *testing.T) {
	for _, gomaxprocs := range []int{1, 4, 16} {
		minimum := database.RecommendedMinimumPoolConnections(gomaxprocs)
		if findings := PoolWarnings(minimum, gomaxprocs); len(findings) != 0 {
			t.Fatalf("GOMAXPROCS=%d pool=%d findings=%+v, want none", gomaxprocs, minimum, findings)
		}
		if findings := PoolWarnings(database.DefaultPoolConnections(gomaxprocs), gomaxprocs); len(findings) != 0 {
			t.Fatalf("GOMAXPROCS=%d default pool findings=%+v, want none", gomaxprocs, findings)
		}
		findings := PoolWarnings(minimum-1, gomaxprocs)
		if len(findings) != 1 || findings[0].Severity != SeverityWarning || findings[0].Code != "database_pool_small" {
			t.Fatalf("GOMAXPROCS=%d pool=%d findings=%+v, want one small-pool warning", gomaxprocs, minimum-1, findings)
		}
		if !strings.Contains(findings[0].Message, "pool_max_conns") {
			t.Fatalf("warning %q does not name the setting to change", findings[0].Message)
		}
	}
}
