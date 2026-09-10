// Run with the usual PGBASE_* environment: go run ./examples/custom-routes.
package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/yakisoba0728/PgBase/internal/config"
	"github.com/yakisoba0728/PgBase/pgbase"
)

func main() {
	options, err := config.LoadEnv(os.LookupEnv)
	if err != nil {
		log.Fatal(err)
	}
	options.CustomRoutes = []pgbase.CustomRoute{{
		Method: "GET", Pattern: "/application/profile", Access: pgbase.RouteUser,
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, ok := pgbase.PrincipalFromContext(r.Context())
			if !ok {
				http.Error(w, "Authentication required", http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(principal)
		}),
	}}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := pgbase.RunManaged(ctx, options); err != nil {
		log.Fatal(err)
	}
}
