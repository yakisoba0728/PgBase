package migrate

import (
	"errors"
	"strings"
	"testing"
)

// TestVerifyExactTargetReadbackBlocksOnEveryDisagreement pins the release gate
// an exact-target rollout depends on. Each case is reachable only when the
// migration transaction has already committed successfully, so a mismatch here
// means the committed database is not the one the release was approved against
// and the job must stop rather than report success.
func TestVerifyExactTargetReadbackBlocksOnEveryDisagreement(t *testing.T) {
	const target int64 = 7
	want := [32]byte{1, 2, 3}
	other := [32]byte{9, 9, 9}
	manifestErr := errors.New("system migration manifest is invalid")
	issue := &HistoryIssue{Kind: HistoryIssueChecksumMismatch, Version: target}

	tests := []struct {
		name       string
		inspection historyInspection
		present    bool
		wantError  string
	}{
		{
			name:       "history table missing after commit",
			inspection: historyInspection{state: HistoryState{Version: target, Checksum: want}},
			wantError:  "committed history table is missing",
		},
		{
			name:       "manifest rejected on read-back",
			inspection: historyInspection{err: manifestErr},
			present:    true,
			wantError:  manifestErr.Error(),
		},
		{
			name:       "typed history issue on read-back",
			inspection: historyInspection{issue: issue, state: HistoryState{Version: target, Checksum: want, Dirty: true}},
			present:    true,
			wantError:  issue.Error(),
		},
		{
			name:       "version behind target",
			inspection: historyInspection{state: HistoryState{Version: target - 1, Checksum: want}},
			present:    true,
			wantError:  "committed history does not match target",
		},
		{
			name:       "checksum does not match the approved migration",
			inspection: historyInspection{state: HistoryState{Version: target, Checksum: other}},
			present:    true,
			wantError:  "committed history does not match target",
		},
		{
			name:       "committed history matches the approved target",
			inspection: historyInspection{state: HistoryState{Version: target, Checksum: want}},
			present:    true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := verifyExactTargetReadback(test.inspection, test.present, target, want)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("verifyExactTargetReadback() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("verifyExactTargetReadback() = %v, want an error containing %q", err, test.wantError)
			}
			if !strings.Contains(err.Error(), "verify system migration target 7") {
				t.Fatalf("verifyExactTargetReadback() = %v, want the target named in the error", err)
			}
		})
	}
}

// TestVerifyExactTargetReadbackKeepsTheTypedIssue lets a rollout job branch on
// the reason rather than on error text.
func TestVerifyExactTargetReadbackKeepsTheTypedIssue(t *testing.T) {
	issue := &HistoryIssue{Kind: HistoryIssueGap, Version: 5}
	err := verifyExactTargetReadback(historyInspection{issue: issue}, true, 7, [32]byte{})
	var got *HistoryIssue
	if !errors.As(err, &got) || got.Kind != HistoryIssueGap {
		t.Fatalf("verifyExactTargetReadback() = %v, want a wrapped %q issue", err, HistoryIssueGap)
	}
}
