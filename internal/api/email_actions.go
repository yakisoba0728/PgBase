package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/userauth"
)

type EmailActions interface {
	RequestVerification(context.Context, uuid.UUID) error
	ConfirmVerification(context.Context, string) error
	RequestPasswordReset(context.Context, string, string) error
	ConfirmPasswordReset(context.Context, string, string) error
	RequestEmailChange(context.Context, uuid.UUID, string, string) error
	ConfirmEmailChange(context.Context, string) error
}

func (e userEndpoints) emailRoutes() []route {
	if e.dependencies.EmailActions == nil {
		return nil
	}
	actions := []struct {
		name   string
		access accessPolicy
	}{
		{"request-verification", accessUserSession}, {"confirm-verification", accessPublic},
		{"request-password-reset", accessPublic}, {"confirm-password-reset", accessPublic},
		{"request-email-change", accessUserSession}, {"confirm-email-change", accessPublic},
	}
	routes := make([]route, 0, len(actions)+1)
	for _, action := range actions {
		routes = append(routes, route{method: "POST", pattern: "/api/v1/auth/" + action.name, access: action.access, response: atomicJSON(maximumAdminResponseBytes), deadline: deadlineDefault, maximumRequestBytes: maximumCredentialRequestBytes, handler: e.emailAction(action.name)})
	}
	routes = append(routes, route{method: "GET", pattern: "/auth/action", access: accessPublic, response: passthrough(), deadline: deadlineDefault, handler: http.HandlerFunc(emailActionPage)})
	return routes
}

func (e userEndpoints) emailAction(action string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Token           string `json:"token"`
			Email           string `json:"email"`
			CurrentPassword string `json:"currentPassword"`
			NewPassword     string `json:"newPassword"`
			NewEmail        string `json:"newEmail"`
		}
		if decodeStrictJSON(r, &input) != nil {
			writeUserAuthError(w, r, userauth.ErrValidation)
			return
		}
		current, _ := r.Context().Value(userPrincipalKey{}).(authenticatedUser)
		service := e.dependencies.EmailActions
		var err error
		switch action {
		case "request-verification":
			err = service.RequestVerification(r.Context(), current.principal.User.ID)
		case "confirm-verification":
			err = service.ConfirmVerification(r.Context(), input.Token)
		case "request-password-reset":
			address, ok := clientAddress(r, e.dependencies.TrustedProxies)
			if !ok {
				writeUserAuthError(w, r, userauth.ErrValidation)
				return
			}
			err = service.RequestPasswordReset(r.Context(), input.Email, address)
		case "confirm-password-reset":
			err = service.ConfirmPasswordReset(r.Context(), input.Token, input.NewPassword)
		case "request-email-change":
			err = service.RequestEmailChange(r.Context(), current.principal.User.ID, input.CurrentPassword, input.NewEmail)
		case "confirm-email-change":
			err = service.ConfirmEmailChange(r.Context(), input.Token)
		}
		if err != nil {
			writeUserAuthError(w, r, err)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}

// Tokens remain in the URL fragment, outside request logs and Referer. A
// deliberate submit also prevents email link scanners from consuming actions.
func emailActionPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; connect-src 'self'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
	_, _ = w.Write([]byte(`<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width"><title>PgBase account confirmation</title><style>body{font:18px system-ui;max-width:36rem;margin:12vh auto;padding:1rem}input,button{font:inherit;padding:.6rem;margin:.5rem 0}label{display:block}</style><h1>Confirm your account action</h1><form><label id="password-label" hidden>New password <input id="password" type="password" autocomplete="new-password" minlength="12" maxlength="1024"></label><button>Confirm</button></form><p role="status"></p><script>
const values=new URLSearchParams(location.hash.slice(1));const token=values.get('token');const action=values.get('action');history.replaceState(null,'',location.pathname);const routes={verify:'confirm-verification',reset:'confirm-password-reset','email-change':'confirm-email-change'};const form=document.querySelector('form');const status=document.querySelector('[role=status]');const password=document.querySelector('#password');if(action==='reset'){document.querySelector('#password-label').hidden=false;password.required=true}if(!token||!routes[action]){form.hidden=true;status.textContent='This confirmation link is invalid.'}form.addEventListener('submit',async event=>{event.preventDefault();const button=form.querySelector('button');button.disabled=true;try{const body={token};if(action==='reset')body.newPassword=password.value;const response=await fetch('/api/v1/auth/'+routes[action],{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body),credentials:'omit'});if(!response.ok)throw Error();form.hidden=true;status.textContent='Confirmed. You can return to your application and sign in.'}catch{status.textContent='Unable to confirm. The link may be expired or already used.';button.disabled=false}});
</script></html>`))
}
