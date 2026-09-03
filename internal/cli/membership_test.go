package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/webisnoteasy/PgBase/internal/membership"
)

// The rollout switch must be reachable only through this operator command, so
// its argument handling refuses anything it does not recognize rather than
// guessing a transition. A proof is required exactly where the transition takes
// one: an irreversible step must not run without the operator naming the fleet,
// and a step that would ignore a witness must not silently accept one.
func TestMembershipRejectsInvalidUsage(t *testing.T) {
	const witness = "0199c400-0000-7000-8000-000000000001:3"
	for name, args := range map[string][]string{
		"missing subcommand":         {"membership"},
		"unknown subcommand":         {"membership", "retire"},
		"unknown flag":               {"membership", "status", "--force"},
		"positional argument":        {"membership", "status", "extra"},
		"witness without version":    {"membership", "cutover", "--witness", "0199c400-0000-7000-8000-000000000001"},
		"malformed witness instance": {"membership", "cutover", "--witness", "not-a-uuid:3"},
		"malformed witness version":  {"membership", "cutover", "--witness", "0199c400-0000-7000-8000-000000000001:B"},
		"cutover without witness":    {"membership", "cutover"},
		"enable without witness":     {"membership", "enable-v5"},
		"retire without witness":     {"membership", "retire-v4"},
		"witness outside a proof":    {"membership", "backfill", "--witness", witness},
		"json outside status":        {"membership", "backfill", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			var stderr bytes.Buffer
			code := Execute(context.Background(), args, emptyEnv, IO{Stdout: io.Discard, Stderr: &stderr})
			if code != 2 {
				t.Fatalf("exit=%d stderr=%q", code, stderr.String())
			}
			if stderr.String() != "pgbase: "+membershipUsage+"\n" {
				t.Fatalf("stderr=%q", stderr.String())
			}
		})
	}
}

func TestMembershipIsDispatchedAsItsOwnCommand(t *testing.T) {
	if _, dispatched := productionCommands()["membership"]; !dispatched {
		t.Fatal("membership has no production handler")
	}
	var stderr bytes.Buffer
	code := Execute(context.Background(), []string{"membership", "status"}, emptyEnv, IO{Stdout: io.Discard, Stderr: &stderr})
	// An empty environment fails configuration, which proves dispatch reached the
	// handler rather than falling through to the unknown-command path.
	if code != 2 || strings.Contains(stderr.String(), "unknown command") {
		t.Fatalf("exit=%d stderr=%q", code, stderr.String())
	}
}

// The transition compares the proof element for element against the lease set
// it reads under its own locks, and requires strictly ascending instance ids.
// An operator lists witnesses in whatever order the status report or their
// notes produced them, so the command has to sort — while still handing a
// repeated instance to the transition, which is what rejects it.
func TestMembershipWitnessProofIsOrderedWithoutCollapsingDuplicates(t *testing.T) {
	var witnesses []membership.FleetMember
	for _, raw := range []string{
		"0199c400-0000-7000-8000-00000000000c:3",
		"0199c400-0000-7000-8000-00000000000a:2",
		"0199c400-0000-7000-8000-00000000000c:3",
	} {
		member, err := parseMembershipWitness(raw)
		if err != nil {
			t.Fatalf("parseMembershipWitness(%q) = %v", raw, err)
		}
		witnesses = append(witnesses, member)
	}
	proof := membershipProof(witnesses)
	want := []membership.FleetMember{
		{InstanceID: uuid.MustParse("0199c400-0000-7000-8000-00000000000a"), Version: membership.WriterB},
		{InstanceID: uuid.MustParse("0199c400-0000-7000-8000-00000000000c"), Version: membership.WriterC},
		{InstanceID: uuid.MustParse("0199c400-0000-7000-8000-00000000000c"), Version: membership.WriterC},
	}
	if len(proof.Members) != len(want) {
		t.Fatalf("proof = %#v", proof.Members)
	}
	for index, member := range proof.Members {
		if member != want[index] {
			t.Fatalf("proof[%d] = %#v, want %#v", index, member, want[index])
		}
	}
}
