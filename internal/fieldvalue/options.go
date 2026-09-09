// Package fieldvalue validates reusable scalar field constraints without
// depending on schema or query packages.
package fieldvalue

import (
	"fmt"
	"math/big"
	"net/mail"
	"net/url"
	"regexp"
	"sort"
	"strings"
)

type Options struct {
	MinLength, MaxLength                 *int
	MinSelect, MaxSelect                 *int
	Pattern, Format, AutogeneratePattern string
	AllowedDomains, DeniedDomains        []string
	AllowedValues                        []string
	Min, Max                             *string
}

func ValidateDefinition(kind string, o Options) error {
	text := kind == "text"
	jsonb := kind == "jsonb"
	isNumeric := kind == "integer" || kind == "bigint" || kind == "numeric"
	if (o.MinLength != nil || o.MaxLength != nil || o.Pattern != "" || o.Format != "" || len(o.AllowedDomains) > 0 || len(o.DeniedDomains) > 0 || o.AutogeneratePattern != "") && !text {
		return fmt.Errorf("text options require text type")
	}
	if len(o.AllowedValues) > 0 && !text && !jsonb {
		return fmt.Errorf("allowed values require text or jsonb")
	}
	if (o.MinSelect != nil || o.MaxSelect != nil) && !jsonb {
		return fmt.Errorf("selection bounds require jsonb")
	}
	if jsonb && len(o.AllowedValues) == 0 && (o.MinSelect != nil || o.MaxSelect != nil) {
		return fmt.Errorf("selection bounds require allowed values")
	}
	if o.MinSelect != nil && *o.MinSelect < 0 || o.MaxSelect != nil && *o.MaxSelect < 0 || o.MinSelect != nil && o.MaxSelect != nil && *o.MinSelect > *o.MaxSelect {
		return fmt.Errorf("invalid selection bounds")
	}
	if (o.Min != nil || o.Max != nil) && !isNumeric {
		return fmt.Errorf("bounds require numeric type")
	}
	if o.MinLength != nil && *o.MinLength < 0 || o.MaxLength != nil && *o.MaxLength < 0 || o.MinLength != nil && o.MaxLength != nil && *o.MinLength > *o.MaxLength {
		return fmt.Errorf("invalid length bounds")
	}
	if o.Pattern != "" {
		if len(o.Pattern) > 1024 {
			return fmt.Errorf("pattern too long")
		}
		if _, err := regexp.Compile(o.Pattern); err != nil {
			return fmt.Errorf("invalid pattern")
		}
	}
	if o.Format != "" && o.Format != "email" && o.Format != "url" {
		return fmt.Errorf("invalid format")
	}
	if (len(o.AllowedDomains) > 0 || len(o.DeniedDomains) > 0) && o.Format != "email" && o.Format != "url" {
		return fmt.Errorf("domains require format")
	}
	if _, err := NormalizeDomains(o.AllowedDomains); err != nil {
		return err
	}
	if _, err := NormalizeDomains(o.DeniedDomains); err != nil {
		return err
	}
	if len(o.AllowedValues) > 1000 {
		return fmt.Errorf("too many allowed values")
	}
	if duplicate(o.AllowedValues) {
		return fmt.Errorf("duplicate values")
	}
	if o.AutogeneratePattern != "" {
		if err := ValidateRandomPattern(o.AutogeneratePattern, 2048); err != nil {
			return fmt.Errorf("invalid autogenerate pattern: %w", err)
		}
	}
	if o.Min != nil {
		if _, err := numeric(*o.Min); err != nil {
			return err
		}
	}
	if o.Max != nil {
		if _, err := numeric(*o.Max); err != nil {
			return err
		}
	}
	if o.Min != nil && o.Max != nil {
		a, _ := numeric(*o.Min)
		b, _ := numeric(*o.Max)
		if a.Cmp(b) > 0 {
			return fmt.Errorf("invalid numeric bounds")
		}
	}
	return nil
}

func ValidateText(value string, o Options) string {
	if o.MinLength != nil && len([]rune(value)) < *o.MinLength {
		return "min_length"
	}
	if o.MaxLength != nil && len([]rune(value)) > *o.MaxLength {
		return "max_length"
	}
	if o.Pattern != "" {
		pattern, err := regexp.Compile(o.Pattern)
		if err != nil || !pattern.MatchString(value) {
			return "pattern"
		}
	}
	if len(o.AllowedValues) > 0 && !contains(o.AllowedValues, value) {
		return "allowed_value"
	}
	var host string
	switch o.Format {
	case "email":
		a, e := mail.ParseAddress(value)
		if e != nil || a.Address != value || strings.Count(value, "@") != 1 {
			return "email"
		}
		host = value[strings.LastIndexByte(value, '@')+1:]
	case "url":
		u, e := url.ParseRequestURI(value)
		if e != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
			return "url"
		}
		host = u.Hostname()
	}
	if len(o.DeniedDomains) > 0 && domainMatches(host, o.DeniedDomains) {
		return "domain"
	}
	if len(o.AllowedDomains) > 0 && !domainMatches(host, o.AllowedDomains) {
		return "domain"
	}
	return ""
}

func ValidateSelection(values []string, o Options) string {
	if o.MinSelect != nil && len(values) < *o.MinSelect {
		return "min_select"
	}
	if o.MaxSelect != nil && len(values) > *o.MaxSelect {
		return "max_select"
	}
	seen := map[string]bool{}
	for _, value := range values {
		if seen[value] {
			return "duplicate_value"
		}
		seen[value] = true
		if !contains(o.AllowedValues, value) {
			return "allowed_value"
		}
	}
	return ""
}

func ValidateNumeric(value string, o Options) string {
	v, err := numeric(value)
	if err != nil {
		return "invalid"
	}
	if o.Min != nil {
		min, err := numeric(*o.Min)
		if err != nil {
			return "invalid"
		}
		if v.Cmp(min) < 0 {
			return "min"
		}
	}
	if o.Max != nil {
		max, err := numeric(*o.Max)
		if err != nil {
			return "invalid"
		}
		if v.Cmp(max) > 0 {
			return "max"
		}
	}
	return ""
}

var decimal = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?$`)

func numeric(s string) (*big.Rat, error) {
	if len(s) > 1000 || !decimal.MatchString(s) {
		return nil, fmt.Errorf("invalid numeric bound")
	}
	n, ok := new(big.Rat).SetString(s)
	if !ok {
		return nil, fmt.Errorf("invalid numeric bound")
	}
	return n, nil
}
func contains(v []string, s string) bool {
	for _, x := range v {
		if x == s {
			return true
		}
	}
	return false
}
func duplicate(v []string) bool {
	m := map[string]bool{}
	for _, x := range v {
		if m[x] {
			return true
		}
		m[x] = true
	}
	return false
}
func domainMatches(host string, domains []string) bool {
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	for _, d := range domains {
		d = strings.ToLower(strings.Trim(strings.TrimSpace(d), "."))
		if host == d || strings.HasSuffix(host, "."+d) {
			return true
		}
	}
	return false
}

// NormalizeDomains returns the stable schema representation used by catalog,
// snapshot and export paths. Domain values are ASCII DNS names; callers may
// use their punycode form for internationalized names.
func NormalizeDomains(values []string) ([]string, error) {
	if values == nil {
		return nil, nil
	}
	if len(values) > 256 {
		return nil, fmt.Errorf("too many domains")
	}
	result := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
		if !validDomain(value) {
			return nil, fmt.Errorf("invalid domain")
		}
		if _, exists := seen[value]; exists {
			return nil, fmt.Errorf("duplicate domain")
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	sort.Strings(result)
	return result, nil
}

func validDomain(value string) bool {
	if len(value) == 0 || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for index := range len(label) {
			current := label[index]
			if current != '-' && !(current >= 'a' && current <= 'z') && !(current >= '0' && current <= '9') {
				return false
			}
		}
	}
	return true
}

// SplitFilterModifier separates the one adopted query modifier from a logical
// field path. Unknown or repeated modifiers are rejected by the parser.
func SplitFilterModifier(value string) (string, string, bool) {
	if !strings.Contains(value, ":") {
		return value, "", true
	}
	if strings.Count(value, ":") != 1 || !strings.HasSuffix(value, ":lower") {
		return "", "", false
	}
	name := strings.TrimSuffix(value, ":lower")
	return name, "lower", name != ""
}
