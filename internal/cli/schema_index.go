package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/webisnoteasy/PgBase/internal/database"
	"github.com/webisnoteasy/PgBase/internal/schema"
)

const schemaIndexUsage = "usage: pgbase schema-index <status [--json], activate --witness ID [--witness ID...], build, rollback>"

// witnessList collects the repeated --witness flag. Activation requires the
// operator to name the live schema writers, and the transition then requires the
// durable leases to be exactly that set.
type witnessList []uuid.UUID

func (w *witnessList) String() string { return "" }

func (w *witnessList) Set(value string) error {
	instanceID, err := uuid.Parse(value)
	if err != nil {
		return errors.New("witness must be an instance UUID")
	}
	*w = append(*w, instanceID)
	return nil
}

// schemaIndexReport is the operator-facing view of the companion rollout. It
// names no collection, field, or index, so it stays safe to paste into a ticket.
type schemaIndexReport struct {
	Installed  bool                `json:"installed"`
	State      string              `json:"state"`
	Generation int64               `json:"generation"`
	Expected   int                 `json:"expected"`
	Valid      int                 `json:"valid"`
	Invalid    int                 `json:"invalid"`
	Mismatched int                 `json:"mismatched"`
	Missing    int                 `json:"missing"`
	Writers    []schemaIndexWriter `json:"writers"`
}

type schemaIndexWriter struct {
	InstanceID      string    `json:"instanceId"`
	CompilerVersion int16     `json:"compilerVersion"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

// runSchemaIndex is the only surface that drives the companion index rollout.
// It is deliberately an operator command: the capability row is the single
// source of truth, and nothing on the HTTP surface, no environment variable and
// no public option may move it.
func runSchemaIndex(ctx context.Context, args []string, env environment, streams IO) error {
	if len(args) == 0 {
		return configurationError{err: errors.New("missing schema-index subcommand"), message: schemaIndexUsage}
	}
	subcommand, rest := args[0], args[1:]
	flags := flag.NewFlagSet("schema-index", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	jsonOutput := flags.Bool("json", false, "")
	var witnesses witnessList
	flags.Var(&witnesses, "witness", "")
	if err := flags.Parse(rest); err != nil || flags.NArg() != 0 {
		return configurationError{err: errors.New("invalid schema-index usage"), message: schemaIndexUsage}
	}
	switch subcommand {
	case "status", "activate", "build", "rollback":
	default:
		return configurationError{err: errors.New("unknown schema-index subcommand"), message: schemaIndexUsage}
	}
	if len(witnesses) != 0 && subcommand != "activate" {
		return configurationError{err: errors.New("witness is only valid for activate"), message: schemaIndexUsage}
	}

	stdout := streams.stdout()
	runtime, err := openDatabase(ctx, env, streams)
	if err != nil {
		return err
	}
	defer runtime.Close()
	pool := runtime.pool
	service := schema.NewService(pool, &schema.Cache{}, schema.Planner{}, nil)

	switch subcommand {
	case "status":
		return writeSchemaIndexStatus(ctx, service, pool, stdout, *jsonOutput)
	case "activate":
		receipt, err := service.ActivateCompanionIndexes(ctx, schema.SchemaWriterFleetProof{Instances: witnesses})
		if err != nil {
			return operationError(runtime.logger, "activate companion index rollout", err)
		}
		return writeSchemaIndexReceipt(stdout, receipt)
	case "build":
		receipt, err := service.MaintainCompanionIndexes(ctx)
		if err != nil {
			return operationError(runtime.logger, "build companion indexes", err)
		}
		return writeSchemaIndexReceipt(stdout, receipt)
	default:
		receipt, err := service.RollbackCompanionIndexes(ctx)
		if err != nil {
			return operationError(runtime.logger, "roll back companion index rollout", err)
		}
		return writeSchemaIndexReceipt(stdout, receipt)
	}
}

func writeSchemaIndexStatus(
	ctx context.Context, service *schema.Service, pool *database.Pool, stdout io.Writer, jsonOutput bool,
) error {
	var diagnostics schema.CompanionIndexDiagnostics
	if err := pool.InTx(ctx, pgx.TxOptions{
		IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly,
	}, func(tx pgx.Tx) error {
		var err error
		diagnostics, err = schema.ReadCompanionIndexDiagnostics(ctx, tx)
		return err
	}); err != nil {
		return err
	}
	leases, err := service.SchemaWriterFleet(ctx)
	if err != nil {
		return err
	}
	report := schemaIndexReport{
		Installed:  diagnostics.Status.Installed,
		State:      string(diagnostics.Status.State),
		Generation: diagnostics.Status.Generation,
		Expected:   diagnostics.Expected,
		Valid:      diagnostics.Valid,
		Invalid:    diagnostics.Invalid,
		Mismatched: diagnostics.Mismatched,
		Missing:    diagnostics.Missing,
		Writers:    make([]schemaIndexWriter, 0, len(leases)),
	}
	for _, lease := range leases {
		report.Writers = append(report.Writers, schemaIndexWriter{
			InstanceID:      lease.InstanceID.String(),
			CompilerVersion: lease.CompilerVersion,
			ExpiresAt:       lease.ExpiresAt.UTC(),
		})
	}
	if jsonOutput {
		return json.NewEncoder(stdout).Encode(report)
	}
	if _, err := fmt.Fprintf(stdout,
		"companion indexes: installed=%t state=%s generation=%d expected=%d valid=%d invalid=%d mismatched=%d missing=%d\n",
		report.Installed, report.State, report.Generation, report.Expected,
		report.Valid, report.Invalid, report.Mismatched, report.Missing); err != nil {
		return err
	}
	for _, writer := range report.Writers {
		if _, err := fmt.Fprintf(stdout, "schema writer: instance=%s compiler=%d\n",
			writer.InstanceID, writer.CompilerVersion); err != nil {
			return err
		}
	}
	return nil
}

func writeSchemaIndexReceipt(stdout io.Writer, receipt schema.CompanionIndexReceipt) error {
	_, err := fmt.Fprintf(stdout,
		"companion indexes: state=%s generation=%d catalogVersion=%d indexes=%d\n",
		receipt.State, receipt.Generation, receipt.CatalogVersion, receipt.IndexCount)
	return err
}
