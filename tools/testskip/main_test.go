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

// A timed-out test binary reports no test-level failure: its panic and
// goroutine dump belong to a test that never ends, followed only by the
// package failure. That output must still be printed.
func TestOutputRetainsUnfinishedTestsOfAFailedPackage(t *testing.T) {
	stream := strings.NewReader(strings.Join([]string{
		`{"Action":"run","Package":"example.test/hung","Test":"TestPass"}`,
		`{"Action":"output","Package":"example.test/hung","Test":"TestPass","Output":"=== RUN   TestPass\n"}`,
		`{"Action":"pass","Package":"example.test/hung","Test":"TestPass"}`,
		`{"Action":"output","Package":"example.test/hung","Test":"TestStuck","Output":"=== CONT  TestStuck\n"}`,
		`{"Action":"output","Package":"example.test/hung","Test":"TestStuck","Output":"panic: test timed out after 20m0s\n"}`,
		`{"Action":"output","Package":"example.test/hung","Test":"TestStuck","Output":"goroutine 7 [select]:\n"}`,
		`{"Action":"output","Package":"example.test/other","Test":"TestRunning","Output":"=== RUN   TestRunning\n"}`,
		`{"Action":"output","Package":"example.test/hung","Output":"FAIL\texample.test/hung\t1200.0s\n"}`,
		`{"Action":"fail","Package":"example.test/hung"}`,
		`{"Action":"output","Package":"example.test/crashed","Test":"TestCrash","Output":"panic: boom\n"}`,
	}, "\n"))
	var output bytes.Buffer
	if _, err := unexpectedSkips(stream, &output, nil); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, want := range []string{"panic: test timed out after 20m0s", "goroutine 7 [select]:", "panic: boom"} {
		if !strings.Contains(text, want) {
			t.Fatalf("output %q is missing %q", text, want)
		}
	}
	if strings.Contains(text, "TestPass") {
		t.Fatalf("successful per-test chatter was printed: %q", text)
	}
	// The other package's running test prints only at end of stream, after
	// the hung package's dump.
	if strings.Index(text, "TestRunning") < strings.Index(text, "goroutine 7") {
		t.Fatalf("unrelated package output was flushed with the failed package: %q", text)
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
