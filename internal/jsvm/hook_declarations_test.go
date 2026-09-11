package jsvm

import (
	"os"
	"regexp"
	"testing"
)

func TestSupportedHookGlobalsAreDeclared(t *testing.T) {
	events := []string{
		"request", "record.before", "record.after", "mail.send", "mail.otp", "mail.verification", "mail.password_reset", "mail.email_change", "mail.smtp_test",
		"file.upload", "file.delete", "file.token", "file.download", "file.transform", "record.list", "record.view", "record.create", "record.update", "record.delete", "record.batch", "record.enrich",
		"schema.import.preview", "schema.import.apply", "collection.create", "collection.update", "collection.delete", "collection.request.list", "collection.request.view", "collection.request.create", "collection.request.update", "collection.request.delete",
		"app.boot", "app.serve", "app.terminate", "auth.password", "auth.refresh", "auth.oauth.begin", "auth.oauth.complete", "auth.otp.request", "auth.otp.complete", "auth.verification.request", "auth.verification.confirm", "auth.password_reset.request", "auth.password_reset.confirm", "auth.email_change.request", "auth.email_change.confirm", "auth.impersonate", "auth.alert",
		"realtime.connect", "realtime.subscribe", "realtime.message", "storage.settings.list", "storage.settings.update", "storage.settings.test", "settings.list", "settings.update", "settings.reload", "backup.create", "backup.restore", "backup.delete", "object.write", "object.delete",
		"model.execute", "model.success", "model.error",
	}
	for _, family := range []string{"model", "record.model", "collection.model"} {
		for _, phase := range []string{"validate", "create", "create.execute", "create.success", "create.error", "update", "update.execute", "update.success", "update.error", "delete", "delete.execute", "delete.success", "delete.error"} {
			events = append(events, family+"."+phase)
		}
	}
	declarations, err := os.ReadFile("../../sdk/javascript/pgbase.d.ts")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range events {
		for _, name := range hookGlobalNames(event) {
			if !regexp.MustCompile(`\b` + regexp.QuoteMeta(name) + `\s*:`).Match(declarations) {
				t.Errorf("supported hook %q runtime global %s is not declared", event, name)
			}
		}
	}
}
