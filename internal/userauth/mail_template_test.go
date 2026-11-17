package userauth

import (
	"net/url"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestMailTemplateRejectsUnclosedHTMLContextBeforeSaving(t *testing.T) {
	for _, fragment := range []string{`<a href="{{action_url}}`, `<script>let destination = "{{action_url}}`} {
		if validTemplate(MailTemplate{Purpose: "verify", Subject: "Verify", Body: "{{action_url}}", HTMLBody: fragment}) {
			t.Errorf("unrenderable HTML template accepted: %q", fragment)
		}
	}
}

func TestMailPreviewTreatsActionURLAsData(t *testing.T) {
	service := &Service{}
	for _, actionURL := range []string{
		"https://example.test/auth?value=a&mode=verify",
		"https://example.test/auth?next='><img/src=x/onerror=alert(1)>",
	} {
		t.Run(actionURL, func(t *testing.T) {
			message, err := service.PreviewMailTemplate(t.Context(), MailTemplate{
				Purpose: "verify", Subject: "Verify", Body: "{{action_url}}",
				HTMLBody: "<a href='{{action_url}}'>Open</a><p>{{action_url}}</p>",
			}, actionURL)
			if err != nil {
				t.Fatal(err)
			}
			if message.Text != actionURL {
				t.Fatalf("plain-text URL changed: %q", message.Text)
			}
			document, err := html.Parse(strings.NewReader(message.HTML))
			if err != nil {
				t.Fatal(err)
			}
			var href, paragraph string
			var visit func(*html.Node)
			visit = func(node *html.Node) {
				if node.Type == html.ElementNode {
					if node.Data == "img" || node.Data == "script" {
						t.Errorf("action URL created a %s element", node.Data)
					}
					for _, attribute := range node.Attr {
						if strings.HasPrefix(strings.ToLower(attribute.Key), "on") {
							t.Errorf("action URL created an event handler %q", attribute.Key)
						}
						if node.Data == "a" && attribute.Key == "href" {
							href = attribute.Val
						}
					}
				}
				if node.Type == html.TextNode && node.Parent != nil && node.Parent.Data == "p" {
					paragraph += node.Data
				}
				for child := node.FirstChild; child != nil; child = child.NextSibling {
					visit(child)
				}
			}
			visit(document)
			if paragraph != actionURL {
				t.Errorf("visible URL changed: %q", paragraph)
			}
			actual, parseErr := url.Parse(href)
			want, _ := url.Parse(actionURL)
			if parseErr != nil || actual.Scheme != want.Scheme || actual.Host != want.Host || actual.Path != want.Path || !reflect.DeepEqual(actual.Query(), want.Query()) {
				t.Errorf("link destination changed: %q", href)
			}
		})
	}
}
