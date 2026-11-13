package pgbase

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"testing"
)

type backgroundFailureRunner struct{ run func(context.Context) error }

func (r backgroundFailureRunner) Run(ctx context.Context) error { return r.run(ctx) }

func TestBackgroundCancellationRecognizesTransportTimeoutWithoutHidingActiveFailures(t *testing.T) {
	timeout := fmt.Errorf("membership schema: %w", &net.OpError{Op: "write", Net: "tcp", Err: os.ErrDeadlineExceeded})
	fatal := errors.New("writer refused")
	for _, test := range []struct {
		name                 string
		cancel               bool
		failure, errorWanted error
	}{
		{"cancelled transport", true, timeout, nil},
		{"active transport failure", false, timeout, timeout},
		{"fatal error during cancellation", true, fatal, fatal},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			started := make(chan struct{})
			app := &App{membershipWriter: backgroundFailureRunner{run: func(ctx context.Context) error {
				close(started)
				if test.cancel {
					<-ctx.Done()
				}
				return test.failure
			}}}
			done := app.startBackground(ctx)
			<-started
			if test.cancel {
				cancel()
			}
			<-done
			if test.errorWanted == nil {
				if app.backgroundErr != nil {
					t.Fatalf("cancelled shutdown=%v", app.backgroundErr)
				}
			} else if !errors.Is(app.backgroundErr, test.errorWanted) {
				t.Fatalf("background error=%v want=%v", app.backgroundErr, test.errorWanted)
			}
		})
	}
}
