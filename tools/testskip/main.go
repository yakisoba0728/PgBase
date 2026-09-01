// Command testskip validates test-level skip events emitted by go test -json.
package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strings"
)

type allowlistedSkip struct {
	packagePath string
	testPattern *regexp.Regexp
}

type testEvent struct {
	Action  string `json:"Action"`
	Package string `json:"Package"`
	Test    string `json:"Test"`
	Output  string `json:"Output"`
}

func main() {
	allowlistPath := flag.String("allowlist", "", "tab-separated package/test-regexp allowlist")
	flag.Parse()
	if *allowlistPath == "" || flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "usage: testskip -allowlist PATH < go-test.json")
		os.Exit(2)
	}

	allowlist, err := readAllowlist(*allowlistPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testskip: %v\n", err)
		os.Exit(2)
	}
	unexpected, err := unexpectedSkips(os.Stdin, os.Stdout, allowlist)
	if err != nil {
		fmt.Fprintf(os.Stderr, "testskip: %v\n", err)
		os.Exit(2)
	}
	if len(unexpected) != 0 {
		fmt.Fprintln(os.Stderr, "testskip: unallowlisted test skips:")
		for _, skipped := range unexpected {
			fmt.Fprintln(os.Stderr, skipped)
		}
		os.Exit(1)
	}
}

func readAllowlist(path string) ([]allowlistedSkip, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}

	var result []allowlistedSkip
	scanner := bufio.NewScanner(strings.NewReader(string(contents)))
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Split(line, "\t")
		if len(fields) < 2 || strings.TrimSpace(fields[0]) == "" || strings.TrimSpace(fields[1]) == "" {
			return nil, fmt.Errorf("allowlist line %d must contain package and test regexp separated by a tab", lineNumber)
		}
		pattern, err := regexp.Compile(strings.TrimSpace(fields[1]))
		if err != nil {
			return nil, fmt.Errorf("allowlist line %d test regexp: %w", lineNumber, err)
		}
		result = append(result, allowlistedSkip{
			packagePath: strings.TrimSpace(fields[0]),
			testPattern: pattern,
		})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read allowlist: %w", err)
	}
	return result, nil
}

func unexpectedSkips(input io.Reader, output io.Writer, allowlist []allowlistedSkip) ([]string, error) {
	decoder := json.NewDecoder(input)
	unexpected := make(map[string]struct{})
	testOutput := make(map[string]*strings.Builder)
	for {
		var event testEvent
		if err := decoder.Decode(&event); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, fmt.Errorf("decode go test JSON: %w", err)
		}
		key := event.Package + "\x00" + event.Test
		if event.Output != "" {
			if event.Test == "" {
				if _, err := io.WriteString(output, event.Output); err != nil {
					return nil, fmt.Errorf("write go test output: %w", err)
				}
			} else {
				buffer := testOutput[key]
				if buffer == nil {
					buffer = &strings.Builder{}
					testOutput[key] = buffer
				}
				buffer.WriteString(event.Output)
			}
		}
		if event.Test != "" && (event.Action == "fail" || event.Action == "skip") {
			if buffer := testOutput[key]; buffer != nil {
				if _, err := io.WriteString(output, buffer.String()); err != nil {
					return nil, fmt.Errorf("write go test output: %w", err)
				}
			}
			delete(testOutput, key)
		} else if event.Test != "" && event.Action == "pass" {
			delete(testOutput, key)
		}
		// Package-level Action=skip is how go test reports packages with no
		// test files. Only named test and subtest skips are policy events.
		if event.Action != "skip" || event.Test == "" {
			continue
		}
		allowed := false
		for _, entry := range allowlist {
			if entry.packagePath == event.Package && entry.testPattern.MatchString(event.Test) {
				allowed = true
				break
			}
		}
		if !allowed {
			unexpected[event.Package+"\t"+event.Test] = struct{}{}
		}
	}

	result := make([]string, 0, len(unexpected))
	for skipped := range unexpected {
		result = append(result, skipped)
	}
	sort.Strings(result)
	return result, nil
}
