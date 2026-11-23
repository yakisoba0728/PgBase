//go:build integration

package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/adminauth"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/mailer"
	"github.com/yakisoba0728/PgBase/internal/migrate"
	"github.com/yakisoba0728/PgBase/internal/testutil"
	"github.com/yakisoba0728/PgBase/internal/userauth"
)

type otpCodeSender struct {
	mu    sync.Mutex
	codes map[string]string
}

func (s *otpCodeSender) Send(_ context.Context, message mailer.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.codes[message.To] = fmt.Sprint(message.Meta["password"])
	return nil
}

// The otp/complete answers for an email must not reveal whether an account
// owns it, also once the per-account failure budget runs out.
func TestEmailOTPCompletionDoesNotRevealAccountExistence(t *testing.T) {
	ctx := context.Background()
	pool, err := database.Open(ctx, testutil.NewDatabase(t), 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	if err := migrate.New(pool, nil).Up(ctx); err != nil {
		t.Fatal(err)
	}
	service := userauth.NewService(pool, adminauth.NewArgon2idPasswords(), []byte("0123456789abcdef0123456789abcdef"), time.Hour)
	sender := &otpCodeSender{codes: map[string]string{}}
	if err := service.ConfigureEmailActions(sender, "http://localhost:8090", time.Hour); err != nil {
		t.Fatal(err)
	}
	if err := service.ConfigureRatePolicy(userauth.RatePolicy{AccountMaxFailures: 5, NetworkMaxFailures: 100, RegistrationNetworkMaxFailures: 100, Window: 15 * time.Minute, Block: 15 * time.Minute}); err != nil {
		t.Fatal(err)
	}
	const existing, missing = "otp-exists@example.test", "otp-missing@example.test"
	if _, err := service.Register(ctx, existing, "correct horse battery staple", "198.51.100.1"); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `update pgbase_meta.auth_collection_policies set otp_enabled=true,otp_duration_seconds=300,otp_max_attempts=3 where auth_collection_id=$1`, userauth.DefaultCollectionID); err != nil {
		t.Fatal(err)
	}
	e := userEndpoints{dependencies: Dependencies{AuthFactors: service}}
	call := func(action, remote string, body any) *httptest.ResponseRecorder {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r := httptest.NewRequest(http.MethodPost, "/api/v1/auth/"+action, strings.NewReader(string(raw)))
		r.Header.Set("Content-Type", "application/json")
		r.RemoteAddr = remote
		w := httptest.NewRecorder()
		e.factorAction(action).ServeHTTP(w, r)
		return w
	}
	type outcome struct{ status, code, retryAfter string }
	observe := func(w *httptest.ResponseRecorder) outcome {
		var body struct {
			Error struct {
				Code string `json:"code"`
			} `json:"error"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return outcome{fmt.Sprint(w.Code), body.Error.Code, w.Header().Get("Retry-After")}
	}
	run := func(email, remote string) []outcome {
		var out []outcome
		request := func() string {
			w := call("otp/request", remote, map[string]string{"email": email})
			out = append(out, observe(w))
			// The code is stored and mailed after the response.
			if err := service.WaitForQueuedMail(ctx); err != nil {
				t.Fatal(err)
			}
			var issued struct {
				OTPID string `json:"otpId"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &issued); err != nil || issued.OTPID == "" {
				t.Fatalf("%s otp/request body=%s", email, w.Body.String())
			}
			return issued.OTPID
		}
		complete := func(id, code string) {
			out = append(out, observe(call("otp/complete", remote, map[string]string{"otpId": id, "code": code})))
		}
		guess := func() string {
			sender.mu.Lock()
			defer sender.mu.Unlock()
			if sender.codes[email] == "000000" {
				return "999999"
			}
			return "000000"
		}
		first := request()
		// Past the code's own three attempts and into the account block.
		for range 6 {
			complete(first, guess())
		}
		// A fresh code during the block, including the right code if one exists.
		second := request()
		complete(second, guess())
		sender.mu.Lock()
		code, ok := sender.codes[email]
		sender.mu.Unlock()
		if !ok {
			code = "123456"
		}
		complete(second, code)
		return out
	}
	gotExisting := run(existing, "198.51.100.10:1000")
	gotMissing := run(missing, "198.51.100.20:1000")
	if _, sent := sender.codes[missing]; sent {
		t.Fatal("missing account received an OTP email")
	}
	if fmt.Sprint(gotExisting) != fmt.Sprint(gotMissing) {
		t.Fatalf("otp responses differ:\nexisting %v\nmissing  %v", gotExisting, gotMissing)
	}
	limited := 0
	for _, o := range gotExisting {
		if o.status == "429" {
			if o.code != "rate_limited" || o.retryAfter == "" {
				t.Fatalf("limited outcome %v", o)
			}
			limited++
		}
	}
	// Five failures exhaust the budget: the fifth guess and every later
	// completion (two guesses, a fresh code, the right code) are refused.
	if limited != 4 {
		t.Fatalf("limited responses = %d in %v", limited, gotExisting)
	}
}
