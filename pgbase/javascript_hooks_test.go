package pgbase

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/mailer"
)

func TestJavaScriptHookUsesPriorityAndTypedMailPatch(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{
 "format":"pgbase.scripts/v1",
 "hooks":[{"event":"mail.send","priority":10,"before":{"module":"route.js","export":"before"},"after":{"module":"route.js","export":"after"}}]
}`, `
exports.before = function (event) {
  if (event.phase !== "before" || event.data.subject !== "from-go") throw new Error("priority");
  return {patch:{to:event.data.to,subject:"from-js",text:event.data.text},state:{value:"seventeen"}};
};
exports.after = function (event) {
  if (event.phase !== "after" || event.state.value !== "seventeen" || event.failed) throw new Error("state");
  return {};
};`)
	options, err := (JavaScriptOptions{Directory: directory}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := prepareJavaScript(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	registry := &HookRegistry{}
	_, err = registry.On(HookMailSend, Hook{Priority: 10, Before: func(_ context.Context, event *HookEvent) error {
		event.Data.(*mailer.Message).Subject = "from-go"
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.installHooks(registry); err != nil {
		t.Fatal(err)
	}
	message := &mailer.Message{Purpose: "verification", To: "person@example.test", Subject: "original", Text: "body"}
	if err := registry.Run(context.Background(), &HookEvent{Kind: HookMailSend, Data: message}, func() error { return nil }); err != nil {
		t.Fatal(err)
	}
	if message.Subject != "from-js" || message.Purpose != "verification" {
		t.Fatalf("message = %#v", message)
	}
}

func TestJavaScriptHookRejectsPatchForReadOnlyAuthEvent(t *testing.T) {
	directory := t.TempDir()
	writeJavaScriptFixture(t, directory, `{
 "format":"pgbase.scripts/v1",
 "hooks":[{"event":"auth.password","before":{"module":"route.js","export":"before"}}]
}`, `exports.before = function () { return {patch:{userId:"00000000-0000-0000-0000-000000000001"}}; };`)
	options, err := (JavaScriptOptions{Directory: directory}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := prepareJavaScript(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close(context.Background()) })
	registry := &HookRegistry{}
	if err := runtime.installHooks(registry); err != nil {
		t.Fatal(err)
	}
	event := &HookEvent{Kind: HookAuthPassword, Data: &AuthEvent{Flow: "login", UserID: uuid.New()}}
	finish, err := registry.Begin(context.Background(), event)
	finish(err)
	if err == nil || !strings.Contains(err.Error(), "read-only") {
		t.Fatalf("read-only patch error = %v", err)
	}
}
