package mailer

import (
	"strings"
	"testing"

	"golang.org/x/net/html"
)

func TestRenderHTMLPreservesActionLinkAndEscapesValues(t *testing.T) {
	const actionURL = "https://example.test/auth/action#action=verify&token=v1.opaque%2Bvalue"
	values := HTMLVariables{ActionURL: actionURL, OTP: `<script>alert("x")</script>`, OTPID: `a" autofocus onfocus="x`, Alert: `a&b <tag>`}
	output, err := RenderHTML(`<a href="{{action_url}}">Verify</a><p>{{otp}}</p><span title="{{otp_id}}">{{alert}}</span>`, values)
	if err != nil {
		t.Fatal(err)
	}
	document, err := html.Parse(strings.NewReader(output))
	if err != nil {
		t.Fatal(err)
	}
	var href, otp, id, alert string
	var visit func(*html.Node)
	visit = func(node *html.Node) {
		if node.Type == html.ElementNode {
			if node.Data == "script" {
				t.Fatal("OTP became script markup")
			}
			for _, attribute := range node.Attr {
				if strings.HasPrefix(attribute.Key, "on") || attribute.Key == "autofocus" {
					t.Fatalf("OTP ID became attribute %q", attribute.Key)
				}
				if node.Data == "a" && attribute.Key == "href" {
					href = attribute.Val
				}
				if node.Data == "span" && attribute.Key == "title" {
					id = attribute.Val
				}
			}
		}
		if node.Type == html.TextNode && node.Parent != nil {
			if node.Parent.Data == "p" {
				otp += node.Data
			}
			if node.Parent.Data == "span" {
				alert += node.Data
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child)
		}
	}
	visit(document)
	if href != actionURL || otp != values.OTP || id != values.OTPID || alert != values.Alert {
		t.Fatalf("rendered values changed: href=%q otp=%q id=%q alert=%q", href, otp, id, alert)
	}
}

func TestRenderHTMLRejectsUnsafeDynamicURLAndMalformedTemplate(t *testing.T) {
	output, err := RenderHTML(`<a href="{{action_url}}">Verify</a>`, HTMLVariables{ActionURL: "javascript:alert(1)"})
	if err != nil || !strings.Contains(output, `href="#ZgotmplZ"`) {
		t.Fatalf("unsafe dynamic URL was not rejected: output=%q err=%v", output, err)
	}
	for _, fragment := range []string{`<a href="{{action_url}}`, `{{unknown}}`} {
		if output, err := RenderHTML(fragment, HTMLVariables{}); err == nil || output != "" {
			t.Fatalf("malformed fragment exposed partial output: %q, %v", output, err)
		}
	}
}
