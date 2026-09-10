package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/yakisoba0728/PgBase/internal/membership"
)

const membershipUsage = "usage: pgbase membership <status [--json], cutover --witness ID:VERSION [--witness ID:VERSION...], backfill, promote-ready, enable-v5 --witness ID:VERSION..., disable-v5, retire-v4 --witness ID:VERSION...>"

// parseMembershipWitness reads one repeated --witness argument. An irreversible
// rollout transition requires the operator to name the live record writers and
// the release each one runs, and the transition then requires the durable
// leases to be exactly that set. The release is named rather than looked up: a
// proof this command filled in from the same rows the transition is about to
// re-read would prove nothing about what the operator confirmed.
func parseMembershipWitness(value string) (membership.FleetMember, error) {
	rawID, rawVersion, separated := strings.Cut(value, ":")
	if !separated {
		return membership.FleetMember{}, errors.New("witness must be INSTANCE_ID:WRITER_VERSION")
	}
	instanceID, err := uuid.Parse(rawID)
	if err != nil {
		return membership.FleetMember{}, errors.New("witness instance must be a UUID")
	}
	version, err := strconv.ParseInt(rawVersion, 10, 16)
	if err != nil {
		return membership.FleetMember{}, errors.New("witness writer version must be an integer")
	}
	return membership.FleetMember{
		InstanceID: instanceID, Version: membership.WriterVersion(version),
	}, nil
}

// membershipProof orders the named members the way the transition's own
// comparison requires. Ordering is a wire detail an operator should not have to
// satisfy by hand, and sorting hides nothing: the transition still rejects a
// repeated instance, and still compares the whole proof element for element
// against the lease set it reads under its own locks.
func membershipProof(members []membership.FleetMember) membership.FleetProof {
	ordered := slices.Clone(members)
	slices.SortFunc(ordered, func(first, second membership.FleetMember) int {
		return bytes.Compare(first.InstanceID[:], second.InstanceID[:])
	})
	return membership.FleetProof{Members: ordered}
}

// membershipReport is the operator-facing view of the record-membership
// rollout. It names no collection and no record, so it stays safe to paste into
// a change ticket, and it reports every witness in the exact --witness spelling
// the next transition expects back.
type membershipReport struct {
	State                string     `json:"state"`
	Generation           int64      `json:"generation"`
	MinimumWriterVersion int        `json:"minimumWriterVersion"`
	CursorIssueVersion   int        `json:"cursorIssueVersion"`
	CutoverAt            *time.Time `json:"cutoverAt"`
	ReadyAt              *time.Time `json:"readyAt"`
	V5EnabledAt          *time.Time `json:"v5EnabledAt"`
	DecodeV4Until        *time.Time `json:"decodeV4Until"`
	V4RetiredAt          *time.Time `json:"v4RetiredAt"`
	Witnesses            []string   `json:"witnesses"`
}

// runMembership is the only surface that drives the record-membership rollout.
// It is deliberately an operator command: the capability row is the single
// source of truth, and nothing on the HTTP surface, no environment variable and
// no public option may move it. Every guard — the fleet proof, the drain hold
// points, the backfill's validated counts — stays in the transition; this
// command supplies arguments and reports receipts.
func runMembership(ctx context.Context, args []string, env environment, streams IO) error {
	if len(args) == 0 {
		return configurationError{err: errors.New("missing membership subcommand"), message: membershipUsage}
	}
	subcommand, rest := args[0], args[1:]
	flags := flag.NewFlagSet("membership", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonOutput := flags.Bool("json", false, "")
	var witnesses []membership.FleetMember
	flags.Func("witness", "", func(value string) error {
		member, err := parseMembershipWitness(value)
		if err != nil {
			return err
		}
		witnesses = append(witnesses, member)
		return nil
	})
	if err := flags.Parse(rest); err != nil || flags.NArg() != 0 {
		return configurationError{err: errors.New("invalid membership usage"), message: membershipUsage}
	}
	var proven bool
	switch subcommand {
	case "cutover", "enable-v5", "retire-v4":
		proven = true
	case "status", "backfill", "promote-ready", "disable-v5":
	default:
		return configurationError{err: errors.New("unknown membership subcommand"), message: membershipUsage}
	}
	// A proof is required exactly where the transition takes one, so a witness
	// can neither be forgotten on a step that needs it nor be passed to a step
	// that would silently ignore it.
	if (len(witnesses) != 0) != proven {
		return configurationError{err: errors.New("witness set does not match the subcommand"), message: membershipUsage}
	}
	if *jsonOutput && subcommand != "status" {
		return configurationError{err: errors.New("json is only valid for status"), message: membershipUsage}
	}

	stdout := streams.stdout()
	runtime, err := openDatabase(ctx, env, streams)
	if err != nil {
		return err
	}
	defer runtime.Close()
	service := membership.New(runtime.pool)

	switch subcommand {
	case "status":
		return writeMembershipStatus(ctx, service, runtime, stdout, *jsonOutput)
	case "cutover":
		receipt, err := service.Cutover(ctx, membershipProof(witnesses))
		return membershipReceipt(runtime, stdout, "start record membership cutover", err,
			"cutover generation=%d collections=%d records=%d",
			receipt.Generation, receipt.Collections, receipt.Records)
	case "backfill":
		return runMembershipBackfill(ctx, service, runtime, stdout)
	case "promote-ready":
		receipt, err := service.PromoteReady(ctx)
		return membershipReceipt(runtime, stdout, "promote record membership ready", err,
			"ready generation=%d collections=%d", receipt.Generation, receipt.Collections)
	case "enable-v5":
		receipt, err := service.EnableV5(ctx, membershipProof(witnesses))
		return membershipReceipt(runtime, stdout, "enable record cursor v5", err,
			"v5 generation=%d writerFloor=%d cursorIssue=%d decodeV4Until=%s",
			receipt.Generation, int(receipt.MinimumWriterVersion), receipt.CursorIssueVersion,
			receipt.DecodeV4Until.UTC().Format(time.RFC3339))
	case "disable-v5":
		receipt, err := service.DisableV5(ctx)
		return membershipReceipt(runtime, stdout, "roll record cursor issuance back to v4", err,
			"v4 generation=%d writerFloor=%d cursorIssue=%d",
			receipt.Generation, int(receipt.MinimumWriterVersion), receipt.CursorIssueVersion)
	default:
		receipt, err := service.RetireV4(ctx, membershipProof(witnesses))
		return membershipReceipt(runtime, stdout, "retire record cursor v4", err,
			"retired generation=%d retiredAt=%s",
			receipt.Generation, receipt.RetiredAt.UTC().Format(time.RFC3339))
	}
}

// membershipReceipt reports one transition. The operation name is what the
// redacting logger records on failure; stdout carries only counts, versions and
// stamps, never a cause, so a receipt an operator files discloses nothing the
// status report would not.
func membershipReceipt(
	runtime *databaseRuntime, stdout io.Writer, operation string, err error, format string, arguments ...any,
) error {
	if err != nil {
		return operationError(runtime.logger, operation, err)
	}
	_, err = fmt.Fprintf(stdout, "record membership: "+format+"\n", arguments...)
	return err
}

// runMembershipBackfill drives the resumable backfill to completion. Each call
// commits its own checkpoint, so a killed run resumes where it stopped and a
// rerun after completion does nothing. A pass that neither scanned a record nor
// completed a collection cannot make progress by being repeated, so the command
// stops and fails rather than spinning: the state that produces it — a
// collection whose backfill row is at another generation, or one another job
// holds — is for the operator to resolve, and reporting completion there would
// let promotion be attempted against an incomplete rollout.
func runMembershipBackfill(
	ctx context.Context, service *membership.Service, runtime *databaseRuntime, stdout io.Writer,
) error {
	for {
		progress, err := service.Backfill(ctx)
		if err != nil {
			return operationError(runtime.logger, "backfill record membership", err)
		}
		if _, err := fmt.Fprintf(stdout,
			"record membership: backfill generation=%d scanned=%d inserted=%d collectionReady=%t done=%t\n",
			progress.Generation, progress.Scanned, progress.Inserted, progress.Ready, progress.Done); err != nil {
			return err
		}
		if progress.Done {
			return nil
		}
		if progress.Scanned == 0 && !progress.Ready {
			return operationError(runtime.logger, "backfill record membership",
				errors.New("no collection could be advanced"))
		}
	}
}

// writeMembershipStatus reports what the next transition will require. The
// fleet read needs no installation check of its own: the status read above it
// already takes the schema pin and rejects an incomplete installation, so a
// database this reaches has the lease table the rollout proofs are read from.
func writeMembershipStatus(
	ctx context.Context, service *membership.Service, runtime *databaseRuntime, stdout io.Writer, jsonOutput bool,
) error {
	status, err := service.Status(ctx)
	if err != nil {
		return operationError(runtime.logger, "read record membership status", err)
	}
	fleet, err := service.ActiveWriterFleet(ctx)
	if err != nil {
		return operationError(runtime.logger, "read active record writer fleet", err)
	}
	report := membershipReport{
		State:                string(status.State),
		Generation:           status.Generation,
		MinimumWriterVersion: int(status.MinimumWriterVersion),
		CursorIssueVersion:   int(status.CursorIssueVersion),
		CutoverAt:            status.CutoverAt,
		ReadyAt:              status.ReadyAt,
		V5EnabledAt:          status.V5EnabledAt,
		DecodeV4Until:        status.DecodeV4Until,
		V4RetiredAt:          status.V4RetiredAt,
		Witnesses:            make([]string, 0, len(fleet)),
	}
	for _, member := range fleet {
		report.Witnesses = append(report.Witnesses,
			member.InstanceID.String()+":"+strconv.Itoa(int(member.Version)))
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(report)
	}
	if _, err := fmt.Fprintf(stdout,
		"record membership: state=%s generation=%d writerFloor=%d cursorIssue=%d retired=%t writers=%d\n",
		report.State, report.Generation, report.MinimumWriterVersion, report.CursorIssueVersion,
		status.V4RetiredAt != nil, len(report.Witnesses)); err != nil {
		return err
	}
	for _, witness := range report.Witnesses {
		if _, err := fmt.Fprintf(stdout, "record writer: --witness %s\n", witness); err != nil {
			return err
		}
	}
	return nil
}
