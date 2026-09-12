package adminauth

import (
	"context"
	"testing"
	"time"

	"github.com/yakisoba0728/PgBase/internal/mailer"
)

type passwordResetSenderStub struct{}

func (passwordResetSenderStub) Send(context.Context, mailer.Message) error { return nil }

func TestConfigurePasswordResetOnlyPermitsWebURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want bool
	}{
		{name: "https", url: "https://pgbase.example", want: true},
		{name: "https base path", url: "https://pgbase.example/base", want: true},
		{name: "loopback HTTP", url: "http://localhost:8090", want: true},
		{name: "loopback IP HTTP", url: "http://127.0.0.1:8090", want: true},
		{name: "query", url: "https://pgbase.example/base?tenant=one"},
		{name: "empty query", url: "https://pgbase.example/base?"},
		{name: "non-loopback HTTP", url: "http://pgbase.example"},
		{name: "ftp", url: "ftp://pgbase.example"},
		{name: "javascript", url: "javascript://pgbase.example"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (&Service{}).ConfigurePasswordReset(passwordResetSenderStub{}, test.url, time.Hour)
			if (err == nil) != test.want {
				t.Fatalf("ConfigurePasswordReset(%q) error=%v, want accepted=%t", test.url, err, test.want)
			}
		})
	}
}
