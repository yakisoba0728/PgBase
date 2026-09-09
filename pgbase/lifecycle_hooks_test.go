package pgbase

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/webisnoteasy/PgBase/internal/api"
)

type captureHookMail struct{ messages []MailMessage }

func (s *captureHookMail) Send(_ context.Context, message MailMessage) error {
	s.messages = append(s.messages, message)
	return nil
}

func TestLifecycleAPIHookCancellationHasPairedOutcome(t *testing.T) {
	registry := &HookRegistry{}
	var after error
	_, err := registry.On(HookRecordCreateRequest, Hook{After: func(_ context.Context, event *HookEvent) { after = event.Err }})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	finish, err := (lifecycleAPIHooks{registry}).Begin(ctx, &api.LifecycleEvent{Kind: HookRecordCreateRequest, Data: &RecordRequestEvent{}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled request hook = %v", err)
	}
	finish(err)
	if !errors.Is(after, context.Canceled) {
		t.Fatalf("after cancellation = %v", after)
	}
}

func TestLifecycleRequestAndMailAdapters(t *testing.T) {
	registry := &HookRegistry{}
	observed := false
	remove, err := registry.On(HookRequest, Hook{Before: func(_ context.Context, e *HookEvent) error {
		request := e.Data.(*RequestEvent)
		request.Request.Header.Set("X-Extension", "yes")
		return nil
	}, After: func(_ context.Context, e *HookEvent) { observed = e.Err == nil }})
	if err != nil {
		t.Fatal(err)
	}
	handler := requestHooks(registry, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Extension") != "yes" {
			t.Error("request hook not applied")
		}
		w.WriteHeader(204)
	}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/", nil))
	if recorder.Code != 204 || !observed {
		t.Fatal("request hooks not completed")
	}
	remove()
	sender := &captureHookMail{}
	failure := errors.New("mail rejected")
	cancel, _ := registry.On(HookMailSend, Hook{Before: func(context.Context, *HookEvent) error { return failure }})
	wrapped := hookedMailer{registry, sender}
	if err = wrapped.Send(context.Background(), MailMessage{To: "a@example.test", Text: "hello"}); !errors.Is(err, failure) || len(sender.messages) != 0 {
		t.Fatal("mail veto reached transport")
	}
	cancel()
	_, _ = registry.On(HookMailSend, Hook{Before: func(_ context.Context, e *HookEvent) error { e.Data.(*MailMessage).Subject = "Customized"; return nil }})
	if err = wrapped.Send(context.Background(), MailMessage{To: "a@example.test", Text: "hello"}); err != nil || len(sender.messages) != 1 || sender.messages[0].Subject != "Customized" {
		t.Fatalf("mail adapter err=%v messages=%v", err, sender.messages)
	}
	purposeSeen := false
	_, err = registry.On(HookMailOTP, Hook{Before: func(_ context.Context, event *HookEvent) error {
		mail := event.Data.(*MailEvent)
		if mail.Purpose != "otp" {
			t.Fatalf("mail purpose=%q", mail.Purpose)
		}
		purposeSeen = true
		mail.Message.Subject = "OTP customized"
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = wrapped.Send(context.Background(), MailMessage{Purpose: "otp", To: "a@example.test", Text: "hello"}); err != nil || !purposeSeen || len(sender.messages) != 2 || sender.messages[1].Subject != "OTP customized" {
		t.Fatalf("purpose mail err=%v seen=%t messages=%v", err, purposeSeen, sender.messages)
	}
}
