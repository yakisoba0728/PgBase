package filestore

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type failingReadCloser struct{ err error }

func (r failingReadCloser) Read([]byte) (int, error) { return 0, r.err }
func (failingReadCloser) Close() error               { return nil }

func TestLifecycleReadCloserReportsOneFinalStreamOutcome(t *testing.T) {
	t.Run("eof", func(t *testing.T) {
		var outcomes []error
		reader := newLifecycleReadCloser(context.Background(), io.NopCloser(strings.NewReader("ok")), func(err error) { outcomes = append(outcomes, err) })
		if _, err := io.ReadAll(reader); err != nil {
			t.Fatal(err)
		}
		if err := reader.Close(); err != nil {
			t.Fatal(err)
		}
		if len(outcomes) != 1 || outcomes[0] != nil {
			t.Fatalf("outcomes=%v", outcomes)
		}
	})
	t.Run("read failure", func(t *testing.T) {
		failure := errors.New("stream failed")
		var outcome error
		reader := newLifecycleReadCloser(context.Background(), failingReadCloser{failure}, func(err error) { outcome = err })
		_, err := reader.Read(make([]byte, 1))
		if !errors.Is(err, failure) || !errors.Is(outcome, failure) {
			t.Fatalf("read=%v outcome=%v", err, outcome)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		var outcome error
		reader := newLifecycleReadCloser(ctx, io.NopCloser(strings.NewReader("unfinished")), func(err error) { outcome = err })
		if err := reader.Close(); err != nil || !errors.Is(outcome, context.Canceled) {
			t.Fatalf("close=%v outcome=%v", err, outcome)
		}
	})
}
