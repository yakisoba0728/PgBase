package config

import (
	"fmt"
	"strconv"

	"github.com/yakisoba0728/PgBase/pgbase"
)

func loadJobs(lookup func(string) (string, bool), opts *pgbase.Options) error {
	raw, ok := value(lookup, "PGBASE_JOBS_ENABLED")
	if !ok || raw == "" {
		return nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return fmt.Errorf("PGBASE_JOBS_ENABLED: %w", err)
	}
	opts.JobsEnabled = enabled
	return nil
}
