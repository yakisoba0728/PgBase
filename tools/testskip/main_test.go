package main

import (
	"bytes"
	"io"
	"regexp"
	"strings"
	"testing"
)

func mustPattern(t *testing.T, expression string) *regexp.Regexp {
	t.Helper()
	pattern, err := regexp.Compile(expression)
	if err != nil {
		t.Fatal(err)
	}
	return pattern
}

func TestUnexpectedSkipsAreComparedByPackageAndTest(t *testing.T) {
	allowlist := []allowlistedSkip{{
		packagePath: "example.test/schema",
		testPattern: mustPattern(t, `^FuzzRelationNames/invalid_utf8$`),
	}}
	stream := strings.NewReader(strings.Join([]string{
		`{"Action":"skip","Package":"example.test/no-tests"}`,
		`{"Action":"skip","Package":"example.test/schema","Test":"FuzzRelationNames/invalid_utf8"}`,
		`{"Action":"skip","Package":"example.test/schema","Test":"TestNeedsDatabase"}`,
	}, "\n"))

	got, err := unexpectedSkips(stream, io.Discard, allowlist)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "example.test/schema\tTestNeedsDatabase" {
		t.Fatalf("unexpected skips = %#v", got)
	}
}

func TestMalformedGoTestJSONFailsValidation(t *testing.T) {
	if _, err := unexpectedSkips(strings.NewReader("not-json\n"), io.Discard, nil); err == nil {
		t.Fatal("malformed go test JSON was accepted")
	}
}

func TestOutputIsConciseButRetainsFailedTestDiagnostics(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`{"Action":"output","Package":"example.test/pkg","Test":"TestPass","Output":"=== RUN   TestPass\n"}`,
		`{"Action":"output","Package":"example.test/pkg","Test":"TestPass","Output":"--- PASS: TestPass (0.00s)\n"}`,
		`{"Action":"pass","Package":"example.test/pkg","Test":"TestPass"}`,
		`{"Action":"output","Package":"example.test/pkg","Test":"TestFail","Output":"    sample_test.go:12: got 1, want 2\n"}`,
		`{"Action":"fail","Package":"example.test/pkg","Test":"TestFail"}`,
		`{"Action":"output","Package":"example.test/pkg","Output":"FAIL\n"}`,
	}, "\n"))
	var output bytes.Buffer
	if _, err := unexpectedSkips(stream, &output, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "TestPass") {
		t.Fatalf("successful per-test chatter was printed: %q", output.String())
	}
	for _, want := range []string{"sample_test.go:12: got 1, want 2", "FAIL"} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("concise output %q is missing %q", output.String(), want)
		}
	}
}
