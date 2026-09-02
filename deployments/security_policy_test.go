package deployments_test

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// allowlistEntry is one dated, owned vulnerability exception. Trivy's own
// schema has no owner or expiry semantics beyond `expired_at`, so the
// accountability fields live in `statement` and are enforced here.
type allowlistEntry struct {
	id         string
	statement  string
	expiredAt  string
	lineNumber int
}

var (
	advisoryIdentifier = regexp.MustCompile(`^(CVE|GHSA|DLA|DSA|ELSA|RHSA|ALAS[0-9]*|AVD|TRIVY)-[A-Za-z0-9.\-]+$`)
	isoDate            = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func TestTrivyAllowlistEntriesAreOwnedAndDated(t *testing.T) {
	entries := parseTrivyAllowlist(t, readFile(t, "security/trivy-allowlist.yaml"))
	seen := make(map[string]int, len(entries))
	for _, entry := range entries {
		if !advisoryIdentifier.MatchString(entry.id) {
			t.Errorf("line %d: %q is not an advisory or CVE identifier", entry.lineNumber, entry.id)
		}
		if previous, duplicate := seen[entry.id]; duplicate {
			t.Errorf("line %d: %s is already allowlisted on line %d", entry.lineNumber, entry.id, previous)
		}
		seen[entry.id] = entry.lineNumber
		// An exception without an owner cannot be reviewed, without a rationale
		// cannot be judged, and without a re-evaluation condition never ends.
		for _, field := range []string{"owner=", "rationale=", "reevaluate="} {
			if !strings.Contains(entry.statement, field) {
				t.Errorf("line %d: %s has no %s in its statement", entry.lineNumber, entry.id, strings.TrimSuffix(field, "="))
			}
		}
		if !isoDate.MatchString(entry.expiredAt) {
			t.Errorf("line %d: %s expiry %q is not a YYYY-MM-DD date", entry.lineNumber, entry.id, entry.expiredAt)
			continue
		}
		expiry, err := time.Parse(time.DateOnly, entry.expiredAt)
		if err != nil {
			t.Errorf("line %d: %s expiry %q: %v", entry.lineNumber, entry.id, entry.expiredAt, err)
			continue
		}
		// An entry that is already expired is dead weight the scanner ignores;
		// removing it is the review the expiry date was supposed to force.
		if !expiry.After(time.Now()) {
			t.Errorf("line %d: %s expired on %s and must be removed or renewed", entry.lineNumber, entry.id, entry.expiredAt)
		}
	}
}

func TestSecurityWorkflowScansWithoutABlanketUnfixedExemption(t *testing.T) {
	workflow := readFile(t, "../.github/workflows/security.yml")
	if strings.Contains(workflow, "ignore-unfixed") {
		t.Error("security.yml suppresses every unfixed vulnerability instead of allowlisting named exceptions")
	}
	for _, contract := range []string{
		"deployments/security/trivy-allowlist.yaml",
		"format: sarif",
		"severity: HIGH,CRITICAL",
	} {
		if !strings.Contains(workflow, contract) {
			t.Errorf("security.yml is missing %q", contract)
		}
	}
}

// parseTrivyAllowlist reads the narrow list shape the allowlist documents. A
// YAML dependency would be production surface for one file, so anything the
// reader cannot classify fails the test rather than being skipped.
func parseTrivyAllowlist(t *testing.T, source string) []allowlistEntry {
	t.Helper()
	var entries []allowlistEntry
	inVulnerabilities := false
	folding := ""
	for index, line := range strings.Split(source, "\n") {
		lineNumber := index + 1
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "#") {
			continue
		}
		if !strings.HasPrefix(line, " ") && !strings.HasPrefix(line, "-") {
			key, value, _ := strings.Cut(line, ":")
			inVulnerabilities = key == "vulnerabilities"
			if inVulnerabilities && strings.TrimSpace(value) == "[]" {
				return nil
			}
			folding = ""
			continue
		}
		if !inVulnerabilities {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if item, found := strings.CutPrefix(trimmed, "- "); found {
			entries = append(entries, allowlistEntry{lineNumber: lineNumber})
			trimmed = item
			folding = ""
		}
		if len(entries) == 0 {
			t.Fatalf("line %d: allowlist content appears before its first list item", lineNumber)
		}
		entry := &entries[len(entries)-1]
		if key, value, hasKey := strings.Cut(trimmed, ":"); hasKey && isAllowlistKey(strings.TrimSpace(key)) {
			key, value = strings.TrimSpace(key), strings.TrimSpace(value)
			// A folded or literal block opens the value on the following,
			// more-indented lines instead of on this one.
			if value == ">" || value == ">-" || value == "|" || value == "|-" {
				folding = key
				continue
			}
			folding = ""
			setAllowlistField(entry, key, value)
			continue
		}
		if folding == "" {
			t.Fatalf("line %d: %q is neither an allowlist key nor a folded continuation", lineNumber, trimmed)
		}
		setAllowlistField(entry, folding, joinFolded(allowlistField(entry, folding), trimmed))
	}
	return entries
}

func isAllowlistKey(key string) bool {
	switch key {
	case "id", "statement", "expired_at":
		return true
	}
	return false
}

func allowlistField(entry *allowlistEntry, key string) string {
	switch key {
	case "id":
		return entry.id
	case "statement":
		return entry.statement
	case "expired_at":
		return entry.expiredAt
	}
	return ""
}

func setAllowlistField(entry *allowlistEntry, key, value string) {
	switch key {
	case "id":
		entry.id = value
	case "statement":
		entry.statement = value
	case "expired_at":
		entry.expiredAt = value
	}
}

func joinFolded(existing, addition string) string {
	if existing == "" {
		return addition
	}
	return existing + " " + addition
}
