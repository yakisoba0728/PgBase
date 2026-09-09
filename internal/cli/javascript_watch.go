package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/webisnoteasy/PgBase/internal/config"
	"github.com/webisnoteasy/PgBase/internal/jsvm"
	"github.com/webisnoteasy/PgBase/internal/observability"
	"github.com/webisnoteasy/PgBase/pgbase"
)

type watchedJavaScriptSnapshot struct {
	directory string
	digest    string
}

func runJavaScriptWatch(ctx context.Context, args []string, env environment, streams IO) error {
	flags := flag.NewFlagSet("js serve", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	watch := flags.Bool("watch", false, "")
	directory := flags.String("dir", "", "")
	poll := flags.Duration("poll", 250*time.Millisecond, "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 || !*watch || *poll < 50*time.Millisecond || *poll > 5*time.Second {
		return configurationError{err: errors.New("invalid JavaScript watch usage"), message: javaScriptUsage}
	}
	options, err := config.LoadEnv(env)
	if err != nil {
		return configurationError{err: err}
	}
	if options.Backups.Directory != "" {
		return configurationError{err: errors.New("JavaScript watch does not support managed backup mode"), message: "disable managed backup mode for js serve --watch"}
	}
	if options.JavaScript == nil {
		options.JavaScript = &pgbase.JavaScriptOptions{}
	}
	if *directory != "" {
		options.JavaScript.Directory = *directory
	}
	if options.JavaScript.Directory == "" {
		return configurationError{err: errors.New("JavaScript directory is required"), message: javaScriptUsage}
	}
	options.Logger = observability.NewJSONLogger(streams.stderr())
	return superviseJavaScript(ctx, options, *poll, streams)
}

func superviseJavaScript(ctx context.Context, sourceOptions pgbase.Options, poll time.Duration, streams IO) error {
	sourceDirectory := sourceOptions.JavaScript.Directory
	snapshot, err := createJavaScriptSnapshot(sourceOptions.JavaScript)
	if err != nil {
		return configurationError{err: err, message: "JavaScript bundle is invalid: " + err.Error()}
	}
	defer os.RemoveAll(snapshot.directory)
	currentOptions := sourceOptions
	currentJavaScript := *sourceOptions.JavaScript
	currentJavaScript.Directory = snapshot.directory
	currentOptions.JavaScript = &currentJavaScript
	app, result, err := startWatchedApplication(ctx, currentOptions)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(streams.stdout(), "JavaScript generation %s running\n", snapshot.digest); err != nil {
		shutdown, cancel := context.WithTimeout(context.Background(), currentOptions.ShutdownTimeout)
		closeErr := app.Close(shutdown)
		cancel()
		runErr := <-result
		return errors.Join(fmt.Errorf("write JavaScript generation status: %w", err), closeErr, runErr)
	}
	ticker := time.NewTicker(poll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdown, cancel := context.WithTimeout(context.Background(), currentOptions.ShutdownTimeout)
			defer cancel()
			closeErr := app.Close(shutdown)
			runErr := <-result
			return errors.Join(closeErr, runErr)
		case runErr := <-result:
			return fmt.Errorf("watched PgBase application stopped: %w", runErr)
		case <-ticker.C:
			candidateOptions := *sourceOptions.JavaScript
			candidateOptions.Directory = sourceDirectory
			candidate, candidateErr := createJavaScriptSnapshot(&candidateOptions)
			if candidateErr != nil {
				_, _ = fmt.Fprintf(streams.stderr(), "pgbase: JavaScript reload rejected: %v\n", candidateErr)
				continue
			}
			if candidate.digest == snapshot.digest {
				_ = os.RemoveAll(candidate.directory)
				continue
			}
			candidateRunOptions := sourceOptions
			candidateOptions.Directory = candidate.directory
			candidateRunOptions.JavaScript = &candidateOptions
			shutdown, cancel := context.WithTimeout(context.Background(), currentOptions.ShutdownTimeout)
			closeErr := app.Close(shutdown)
			cancel()
			runErr := <-result
			if closeErr != nil || runErr != nil {
				_ = os.RemoveAll(candidate.directory)
				return errors.Join(closeErr, runErr)
			}
			newApp, newResult, startErr := startWatchedApplication(ctx, candidateRunOptions)
			if startErr != nil {
				_ = os.RemoveAll(candidate.directory)
				restoredApp, restoredResult, restoreErr := startWatchedApplication(ctx, currentOptions)
				if restoreErr != nil {
					return errors.Join(fmt.Errorf("start JavaScript generation: %w", startErr), fmt.Errorf("restore previous generation: %w", restoreErr))
				}
				app, result = restoredApp, restoredResult
				_, _ = fmt.Fprintf(streams.stderr(), "pgbase: JavaScript restart failed; previous generation restored: %v\n", startErr)
				continue
			}
			_ = os.RemoveAll(snapshot.directory)
			snapshot, currentOptions, app, result = candidate, candidateRunOptions, newApp, newResult
			_, _ = fmt.Fprintf(streams.stdout(), "JavaScript generation %s running\n", snapshot.digest)
		}
	}
}

func createJavaScriptSnapshot(options *pgbase.JavaScriptOptions) (watchedJavaScriptSnapshot, error) {
	if options == nil {
		return watchedJavaScriptSnapshot{}, errors.New("JavaScript options are unavailable")
	}
	bundle, err := jsvm.LoadBundle(options.Directory)
	if err != nil {
		return watchedJavaScriptSnapshot{}, err
	}
	directory, err := bundle.WriteSnapshot("")
	if err != nil {
		return watchedJavaScriptSnapshot{}, err
	}
	validated := *options
	validated.Directory = directory
	if _, err := pgbase.ValidateJavaScript(context.Background(), validated); err != nil {
		_ = os.RemoveAll(directory)
		return watchedJavaScriptSnapshot{}, err
	}
	return watchedJavaScriptSnapshot{directory: directory, digest: bundle.Digest()}, nil
}

func startWatchedApplication(ctx context.Context, options pgbase.Options) (*pgbase.App, <-chan error, error) {
	app, err := pgbase.New(ctx, options)
	if err != nil {
		return nil, nil, fmt.Errorf("construct watched PgBase application: %w", err)
	}
	result := make(chan error, 1)
	go func() { result <- app.Run(ctx) }()
	return app, result, nil
}
