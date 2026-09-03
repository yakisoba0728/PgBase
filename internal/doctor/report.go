// Package doctor performs read-only pgBase consistency diagnostics.
package doctor

// Finding identifies one detected consistency problem without exposing
// connection details or SQL diagnostics.
type Finding struct {
	Severity string `json:"severity"`
	Code     string `json:"code"`
	Message  string `json:"message"`
}

type Report struct {
	OK       bool      `json:"ok"`
	Findings []Finding `json:"findings"`
}
