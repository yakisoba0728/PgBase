package mailer

import (
	"html/template"
	"strings"
)

const HTMLLayout = `<!doctype html><html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><style>body{margin:0;padding:24px;background:#f5f5f5;color:#171717;font:16px/1.5 system-ui,sans-serif}.card{max-width:620px;margin:auto;padding:28px;background:#fff;border-radius:10px}.button{display:inline-block;padding:10px 18px;border-radius:6px;background:#171717;color:#fff!important;text-decoration:none}</style></head><body><main class="card">{{.}}</main></body></html>`

func WrapHTML(fragment string) (string, error) {
	t, err := template.New("mail").Parse(HTMLLayout)
	if err != nil {
		return "", err
	}
	var out strings.Builder
	err = t.Execute(&out, template.HTML(fragment)) // administrators explicitly author this trusted template.
	return out.String(), err
}
