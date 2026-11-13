package mailer

import (
	"bytes"
	"encoding/base64"
	"io"
	"mime/multipart"
	"strings"
	"testing"
)

func TestAttachmentMIMEBase64LineLength(t *testing.T) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	original := bytes.Repeat([]byte{0, 1, 2, 255}, 1024)
	if err := writeMailPart(w, "application/octet-stream", "data.bin", original, true); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	r := multipart.NewReader(&body, w.Boundary())
	p, err := r.NextPart()
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := io.ReadAll(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(encoded), "\r\n") {
		if len(line) > 76 {
			t.Fatalf("MIME line length=%d", len(line))
		}
	}
	decoded, err := io.ReadAll(base64.NewDecoder(base64.StdEncoding, bytes.NewReader(encoded)))
	if err != nil || !bytes.Equal(decoded, original) {
		t.Fatalf("roundtrip failed: %v", err)
	}
}
