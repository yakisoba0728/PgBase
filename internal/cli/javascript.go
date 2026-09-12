package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/yakisoba0728/PgBase/internal/config"
	"github.com/yakisoba0728/PgBase/pgbase"
)

const javaScriptUsage = "usage: pgbase js check [--dir DIR] | pgbase js types [FILE] | pgbase js serve --watch [--dir DIR] [--poll DURATION]"

func runJavaScript(ctx context.Context, args []string, env environment, streams IO) error {
	if len(args) == 0 {
		return configurationError{err: errors.New("invalid JavaScript usage"), message: javaScriptUsage}
	}
	switch args[0] {
	case "types":
		if len(args) > 2 {
			return configurationError{err: errors.New("invalid JavaScript types usage"), message: javaScriptUsage}
		}
		contents := []byte(pgbase.JavaScriptTypeDeclarations())
		if len(args) == 1 || args[1] == "-" {
			_, err := streams.stdout().Write(contents)
			return err
		}
		filename := filepath.Clean(args[1])
		if err := os.WriteFile(filename, contents, 0o644); err != nil {
			return fmt.Errorf("write JavaScript type declarations: %w", err)
		}
		_, err := fmt.Fprintln(streams.stdout(), filename)
		return err
	case "check":
		flags := flag.NewFlagSet("js check", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		directory := flags.String("dir", "", "")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return configurationError{err: errors.New("invalid JavaScript check usage"), message: javaScriptUsage}
		}
		options, err := config.LoadJavaScriptEnv(env)
		if err != nil {
			return configurationError{err: err}
		}
		if options == nil {
			options = &pgbase.JavaScriptOptions{}
		}
		if *directory != "" {
			options.Directory = *directory
		}
		if options.Directory == "" {
			return configurationError{err: errors.New("JavaScript directory is required"), message: javaScriptUsage}
		}
		manifest, err := pgbase.ValidateJavaScript(ctx, *options)
		if err != nil {
			return configurationError{err: err, message: "JavaScript bundle is invalid: " + err.Error()}
		}
		_, err = fmt.Fprintf(streams.stdout(), "JavaScript bundle %s routes=%d hooks=%d jobs=%d migrations=%d\n",
			manifest.Digest, len(manifest.Routes), len(manifest.Hooks), len(manifest.Jobs), len(manifest.Migrations))
		return err
	case "serve":
		return runJavaScriptWatch(ctx, args[1:], env, streams)
	default:
		return configurationError{err: errors.New("invalid JavaScript usage"), message: javaScriptUsage}
	}
}
