package mailer

import (
	"html/template"
	"strings"
)

const HTMLLayout = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>body{margin:0;padding:24px;background:#f5f5f5;color:#171717;font:16px/1.5 system-ui,sans-serif}.card{max-width:620px;margin:auto;padding:28px;background:#fff;border-radius:10px}.button{display:inline-block;padding:10px 18px;border-radius:6px;background:#171717;color:#fff!important;text-decoration:none}</style></head><body><main class="card">{{template "content" .}}</main></body></html>`

// HTMLVariables are data substituted into an administrator-authored template.
// Values must be plain strings, never pre-escaped HTML or template source.
type HTMLVariables struct {
	ActionURL string
	OTP       string
	OTPID     string
	Alert     string
}

// RenderHTML keeps the existing placeholder syntax while applying contextual
// escaping to its values. The fragment itself is trusted administrator HTML;
// this function does not sanitize markup deliberately authored by an operator.
func RenderHTML(fragment string, values HTMLVariables) (string, error) {
	t, err := template.New("mail").Funcs(template.FuncMap{
		"action_url": func() string { return values.ActionURL },
		"otp":        func() string { return values.OTP },
		"otp_id":     func() string { return values.OTPID },
		"alert":      func() string { return values.Alert },
	}).Parse(HTMLLayout)
	if err != nil {
		return "", err
	}
	if _, err = t.New("content").Parse(fragment); err != nil {
		return "", err
	}
	var out strings.Builder
	if err = t.Execute(&out, nil); err != nil {
		return "", err
	}
	return out.String(), nil
}
