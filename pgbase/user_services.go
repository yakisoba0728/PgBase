package pgbase

import (
	"time"

	"github.com/webisnoteasy/PgBase/internal/adminauth"
	"github.com/webisnoteasy/PgBase/internal/api"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/mailer"
	"github.com/webisnoteasy/PgBase/internal/recordaccess"
	"github.com/webisnoteasy/PgBase/internal/serviceauth"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

// withUserServices installs the public identity capability as one unit, so the
// bearer routes cannot be exposed without the collection policy service.
func withUserServices(dependencies api.Dependencies, pool *database.Pool, options Options) (api.Dependencies, error) {
	auth := userauth.NewService(pool, adminauth.NewArgon2idPasswords(), options.TokenSecret, 24*time.Hour)
	if options.Hooks != nil {
		auth.ConfigureMailerDecorator(func(sender mailer.Sender) mailer.Sender { return hookedMailer{options.Hooks, sender} })
	}
	if options.PublicURL != nil {
		if err := auth.ConfigureEmailActionBaseURL(options.PublicURL.String(), time.Hour); err != nil {
			return api.Dependencies{}, err
		}
	}
	if options.SMTP.Address != "" {
		sender, err := mailer.NewSMTP(options.SMTP.config())
		if err != nil {
			return api.Dependencies{}, err
		}
		if err := auth.ConfigureEmailActions(sender, options.PublicURL.String(), time.Hour); err != nil {
			return api.Dependencies{}, err
		}
	}
	if len(options.OAuthProviders) > 0 {
		providers, err := oauthProviders(options.OAuthProviders)
		if err != nil {
			return api.Dependencies{}, err
		}
		if err := auth.ConfigureOAuth(providers, options.OAuthRedirects, 5*time.Minute); err != nil {
			return api.Dependencies{}, err
		}
	}
	auth.ConfigureOAuthRegistration(options.UserRegistrationEnabled)
	auth.ConfigureDefaultRegistration(options.UserRegistrationEnabled)
	var userService api.UserAuth = auth
	var emailActions api.EmailActions = auth
	var authCollections api.AuthCollections = auth
	var authAdministration api.AuthAdministration = auth
	var authFactors api.AuthFactors = auth
	if options.Hooks != nil {
		hooked := hookedUserAuth{Service: auth, registry: options.Hooks}
		userService, emailActions, authCollections, authAdministration, authFactors = hooked, hooked, hooked, hooked, hooked
	}
	dependencies.EmailActions = emailActions
	dependencies.AuthCollections = authCollections
	dependencies.AuthAdministration = authAdministration
	dependencies.ServiceTokens = serviceauth.NewService(pool, options.TokenSecret)
	dependencies.AuthFactors = authFactors
	dependencies.UserAuth = userService
	dependencies.RecordAccess = recordaccess.NewService(pool)
	dependencies.UserRegistrationEnabled = options.UserRegistrationEnabled
	return dependencies, nil
}
