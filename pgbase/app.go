package pgbase

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/migrate"
)

// App owns the pgBase HTTP server and its required process resources.
type App struct {
	options    Options
	pool       *database.Pool
	handler    http.Handler
	httpServer *http.Server
	readiness  *readinessState
	cleaner    *adminauth.Cleaner
	closeOnce  sync.Once

	closeMu          sync.Mutex
	closeErr         error
	backgroundCancel context.CancelFunc
	backgroundDone   chan struct{}
	backgroundErr    error
}

type readinessState struct {
	accepting    atomic.Bool
	dependencies []api.Readiness
}

func (s *readinessState) Ready(ctx context.Context) error {
	if !s.accepting.Load() {
		return readinessFailure{components: []string{"http"}}
	}
	for _, dependency := range s.dependencies {
		if err := dependency.Ready(ctx); err != nil {
			if named, ok := dependency.(interface{ Component() string }); ok {
				return readinessFailure{components: []string{named.Component()}}
			}
			return readinessFailure{components: []string{"dependency"}}
		}
	}
	return nil
}

type readinessFunc func(context.Context) error

func (f readinessFunc) Ready(ctx context.Context) error { return f(ctx) }

type namedReadiness struct {
	name  string
	check api.Readiness
}

func (r namedReadiness) Component() string               { return r.name }
func (r namedReadiness) Ready(ctx context.Context) error { return r.check.Ready(ctx) }

type readinessFailure struct{ components []string }

func (e readinessFailure) Error() string        { return "required component is not ready" }
func (e readinessFailure) Components() []string { return append([]string(nil), e.components...) }

// New validates options, establishes PostgreSQL connectivity, verifies the
// system schema, and builds an inactive HTTP application. It starts no
// goroutines; callers must invoke Run.
func New(ctx context.Context, options Options) (*App, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	normalized, err := options.Normalized()
	if err != nil {
		return nil, err
	}
	pool, err := database.Open(ctx, normalized.DatabaseURL, normalized.StatementTimeout)
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL pool: %w", err)
	}
	keepPool := false
	defer func() {
		if !keepPool {
			pool.Close()
		}
	}()

	runner := migrate.New(pool, normalized.Logger)
	if normalized.AutoMigrate {
		if err := runner.Up(ctx); err != nil {
			return nil, fmt.Errorf("apply system migrations: %w", err)
		}
	}
	status, err := runner.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("read system migration status: %w", err)
	}
	if status.Dirty || status.Current != status.Latest {
		return nil, errors.New("system migrations are not current")
	}

	readiness := &readinessState{dependencies: []api.Readiness{
		namedReadiness{name: "database", check: readinessFunc(pool.Ping)},
		namedReadiness{name: "migrations", check: readinessFunc(func(ctx context.Context) error {
			status, err := runner.Status(ctx)
			if err != nil {
				return err
			}
			if status.Dirty || status.Current != status.Latest {
				return errors.New("system migrations are not current")
			}
			return nil
		})},
	}}
	authService := adminauth.NewService(
		pool,
		adminauth.NewArgon2idPasswords(),
		normalized.TokenSecret,
		normalized.SetupToken,
		normalized.AdminSessionTTL,
		nil,
	)
	handler := api.NewRouter(api.Dependencies{
		Readiness:          readiness,
		Logger:             normalized.Logger,
		CORSAllowedOrigins: normalized.CORSAllowedOrigins,
		AdminAuth:          authService,
		PublicURL:          normalized.PublicURL,
		TrustedProxies:     normalized.TrustedProxies,
		TokenSecret:        normalized.TokenSecret,
	})
	app := &App{
		options:   normalized,
		pool:      pool,
		handler:   handler,
		readiness: readiness,
		cleaner:   adminauth.NewCleaner(pool, nil),
	}
	app.httpServer = &http.Server{
		Addr:              normalized.HTTPAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	keepPool = true
	return app, nil
}

// Handler returns the application's HTTP handler without starting a listener.
func (a *App) Handler() http.Handler { return a.handler }

// Run serves HTTP until the context is cancelled, the server is closed, or it
// encounters an operational error.
func (a *App) Run(ctx context.Context) error {
	if a == nil || a.httpServer == nil {
		return errors.New("pgbase app is not initialized")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	listener, err := net.Listen("tcp", a.options.HTTPAddress)
	if err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		defer cancel()
		return errors.Join(fmt.Errorf("listen for HTTP: %w", err), a.Close(closeCtx))
	}
	serverDone := make(chan error, 1)
	go func() {
		serverDone <- a.httpServer.Serve(listener)
	}()
	backgroundDone := a.startBackground(ctx)
	a.readiness.accepting.Store(true)

	select {
	case serverErr := <-serverDone:
		a.readiness.accepting.Store(false)
		if errors.Is(serverErr, http.ErrServerClosed) {
			return a.Close(context.Background())
		}
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		closeErr := a.Close(closeCtx)
		cancel()
		return errors.Join(serverErr, closeErr)
	case <-backgroundDone:
		a.readiness.accepting.Store(false)
		a.closeMu.Lock()
		backgroundErr := a.backgroundErr
		a.closeMu.Unlock()
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		closeErr := a.Close(closeCtx)
		cancel()
		serverErr := <-serverDone
		if errors.Is(serverErr, http.ErrServerClosed) {
			serverErr = nil
		}
		return errors.Join(backgroundErr, closeErr, serverErr)
	case <-ctx.Done():
		a.readiness.accepting.Store(false)
		closeCtx, cancel := context.WithTimeout(context.Background(), a.options.ShutdownTimeout)
		closeErr := a.Close(closeCtx)
		cancel()
		serverErr := <-serverDone
		if errors.Is(serverErr, http.ErrServerClosed) {
			serverErr = nil
		}
		return errors.Join(closeErr, serverErr)
	}
}

func (a *App) startBackground(ctx context.Context) <-chan struct{} {
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	a.closeMu.Lock()
	a.backgroundCancel = cancel
	a.backgroundDone = done
	a.closeMu.Unlock()
	go func() {
		err := a.cleaner.Run(runCtx)
		if runCtx.Err() != nil && errors.Is(err, runCtx.Err()) {
			err = nil
		}
		a.closeMu.Lock()
		a.backgroundErr = err
		close(done)
		a.closeMu.Unlock()
	}()
	return done
}

// Close stops accepting HTTP requests, drains active requests, and closes the
// PostgreSQL pool. It is safe to call more than once.
func (a *App) Close(ctx context.Context) error {
	if a == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	a.closeOnce.Do(func() {
		if a.readiness != nil {
			a.readiness.accepting.Store(false)
		}
		var shutdownErr error
		a.closeMu.Lock()
		backgroundCancel := a.backgroundCancel
		backgroundDone := a.backgroundDone
		a.closeMu.Unlock()
		if backgroundCancel != nil {
			backgroundCancel()
		}
		if a.httpServer != nil {
			shutdownErr = a.httpServer.Shutdown(ctx)
			if errors.Is(shutdownErr, http.ErrServerClosed) {
				shutdownErr = nil
			}
		}
		if backgroundDone != nil {
			<-backgroundDone
		}
		if a.pool != nil {
			a.pool.Close()
		}
		a.closeMu.Lock()
		a.closeErr = errors.Join(shutdownErr, a.backgroundErr)
		a.closeMu.Unlock()
	})
	a.closeMu.Lock()
	defer a.closeMu.Unlock()
	return a.closeErr
}
