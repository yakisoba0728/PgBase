package pgbase

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/yakisoba0728/PgBase/internal/backup"
	"github.com/yakisoba0728/PgBase/internal/database"
	"github.com/yakisoba0728/PgBase/internal/mailer"
)

const backupAlertMinimumInterval = 15 * time.Minute

// backupFailureAlerter keeps failed scheduled backups visible without turning
// a temporary SMTP outage or repeated scheduler retries into a mail storm.
// It uses all active PgBase administrators because PgBase has no separate
// superuser role: administrators are the installation's operational owners.
type backupFailureAlerter struct {
	pool   *database.Pool
	sender mailer.Sender
	logger *slog.Logger
	now    func() time.Time
	mu     sync.Mutex
	last   time.Time
}

func newBackupFailureAlerter(pool *database.Pool, options Options) *backupFailureAlerter {
	if pool == nil || options.SMTP.Address == "" {
		return nil
	}
	sender, err := mailer.NewSMTP(options.SMTP.config())
	if err != nil {
		return nil
	}
	return &backupFailureAlerter{pool: pool, sender: sender, logger: options.Logger, now: time.Now}
}

func (a *backupFailureAlerter) Notify(ctx context.Context, job backup.Job) {
	if a == nil || a.sender == nil || a.pool == nil {
		return
	}
	now := a.now().UTC()
	a.mu.Lock()
	if !a.last.IsZero() && now.Sub(a.last) < backupAlertMinimumInterval {
		a.mu.Unlock()
		return
	}
	a.mu.Unlock()
	rows, err := a.pool.Query(ctx, `select email from pgbase_meta.admins where active order by email`)
	if err != nil {
		a.log(ctx, "read backup alert recipients", err)
		return
	}
	defer rows.Close()
	var recipients []string
	for rows.Next() {
		var email string
		if err := rows.Scan(&email); err != nil {
			a.log(ctx, "read backup alert recipient", err)
			return
		}
		recipients = append(recipients, email)
	}
	if err := rows.Err(); err != nil || len(recipients) == 0 {
		if err != nil {
			a.log(ctx, "read backup alert recipients", err)
		}
		return
	}
	message := mailer.Message{Purpose: "managed_backup_failure", Subject: "PgBase managed backup failed", Text: "A scheduled PgBase backup failed at " + job.UpdatedAt.UTC().Format(time.RFC3339) + ". Check the server diagnostic log and run a verified backup before relying on recovery."}
	for _, recipient := range recipients {
		message.To = recipient
		if err := a.sendWithRetry(ctx, message); err != nil {
			a.log(ctx, "send managed backup failure alert", err)
			return
		}
	}
	a.mu.Lock()
	a.last = now
	a.mu.Unlock()
}

func (a *backupFailureAlerter) sendWithRetry(ctx context.Context, message mailer.Message) error {
	var err error
	for attempt := 0; attempt < 2; attempt++ {
		if err = a.sender.Send(ctx, message); err == nil {
			return nil
		}
		if attempt == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(250 * time.Millisecond):
			}
		}
	}
	return err
}

func (a *backupFailureAlerter) log(ctx context.Context, operation string, err error) {
	if a.logger != nil {
		a.logger.ErrorContext(ctx, operation, "error", err)
	}
}
