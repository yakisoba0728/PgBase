package database

import (
	"testing"
	"time"
)

func TestNilPoolStatsIsZeroSnapshot(t *testing.T) {
	var pool *Pool
	if got := pool.Stats(); got != (PoolStats{}) {
		t.Fatalf("Stats() = %+v, want zero snapshot", got)
	}
}

func TestPostgresDurationRoundsPositiveSubMillisecondValuesUpWithoutGoUnits(t *testing.T) {
	tests := []struct {
		duration time.Duration
		want     string
	}{
		{duration: 0, want: "0"},
		{duration: 100 * time.Microsecond, want: "1"},
		{duration: time.Millisecond, want: "1"},
		{duration: 90 * time.Second, want: "90000"},
	}
	for _, test := range tests {
		if got := postgresDuration(test.duration); got != test.want {
			t.Fatalf("postgresDuration(%s) = %q, want %q", test.duration, got, test.want)
		}
	}
}
