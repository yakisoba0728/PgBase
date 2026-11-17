# Uploaded-file storage settings

PgBase can keep uploaded record files in its configured local directory or an S3-compatible bucket. Administrators can read, save, and test the active uploaded-file storage from the Storage page. These settings do not change where managed backup archives are stored.

The native protected-file HTTP routes evaluate regular-record view/update rules with the originating request's method, query, headers and authenticated-user metadata. Listing, upload, token minting, download and deletion retain their normal ownership and field checks. A download token does not bypass a later policy change or a denied request context. Embedded service calls without HTTP metadata do not acquire it from an earlier request. This transport binding does not synthesize multipart body fields or per-operation batch bodies.

Configured local and S3 backends retain single-byte-range downloads through the storage controller. A range response holds the same destination lease as a complete download until its body closes; an unsatisfiable range releases the lease and retains the object size for the HTTP 416 response.

S3 settings include bucket, region, endpoint, access key, secret key, and path-style addressing. PgBase encrypts the secret key with the project token secret and never returns it. A blank secret field preserves the configured secret. Saving uses a revision check, validates the candidate with a small upload/read/delete probe, and applies the committed generation to subsequent file operations without restarting the process. Every process checks the database generation before object access, so other PgBase processes converge on the saved credentials.

HTTPS endpoints are required by default. Local MinIO development can opt into an HTTP loopback endpoint with `PGBASE_FILES_S3_ALLOW_INSECURE_LOOPBACK=true`. This flag does not permit plaintext remote endpoints.

Changing credentials for the same bucket is supported. Directly changing between local and S3, or changing an S3 bucket, endpoint, region, or path-style namespace, remains rejected while uploaded, staged, variant, or cleanup state exists. Use the administrator storage migration workflow for a populated destination.

Storage migration records encrypted immutable source and destination snapshots plus a durable ledger for original, thumbnail, variant, and cleanup objects. The active destination continues serving reads and writes while referenced objects are copied and SHA-256 verified. Upload and variant publication share the final transition fence, and the worker performs a final reference scan before atomically activating the verified destination. A stopped or failed worker can resume from its ledger; cancel and copy failure leave the original destination active. After cutover, source deletion is delayed and retried. Cleanup refuses to delete from a namespace that has become active again or is used by another active migration.

Starting a migration requires a client-generated UUID in `PgBase-Operation-ID`. Retrying that UUID with the exact same destination returns the durable migration; reusing it with different input is rejected. The latest-migration endpoint (`GET /api/admin/storage/files/migrations/latest`) supports cross-tab discovery and returns HTTP 404 `not_found` when no migration has ever been started; clients treat that as "no migration", not as an error. Meanwhile the operation UUID provides exact recovery when a start response is lost.

The migration copies one object at a time through a bounded temporary file. The worker rejects objects larger than 8 GiB; the public uploaded-file configuration currently has a lower 100 MiB maximum. Operators should verify capacity for the temporary directory and retain a provider-level backup before a large move.

The connection test proves the permissions PgBase uses: put, get, and delete for a random bounded object. It does not require bucket listing. A successful local MinIO test does not validate production DNS, TLS trust, IAM policy, lifecycle, replication, or availability.

## Variant cleanup and uncertain writes (schema 69)

Before writing a generated image variant, PgBase commits a cleanup intent in
`pgbase_meta.file_object_deletions`. Its staging session and publishing
transaction use the same object locks as the existing cleanup worker. Publishing
the variant reference and removing its intent happen in one transaction. A lost
COMMIT response therefore does not require an unbounded synchronous cleanup
query: the transaction's actual outcome determines which durable state survives.

An interrupted storage request can finish remotely after its PostgreSQL
connections disappear. Such an intent has `retain_on_success=true`: a successful
physical deletion does **not** retire it. The worker keeps the intent and retries
hourly so a later write is still tracked. When the live writer can establish that
no Put started or that Put completed successfully, it can release this retention
using a bounded cleanup query. Failed queries leave the retained intent intact.
There is no time-based automatic pruning of uncertain intents.

`attempts`, `last_attempt_at`, `next_attempt_at`, and sanitized `last_error`
record cleanup state. Delete failures retain the row and use exponential retry
delays from one second to 256 seconds. Due-time ordering lets ordinary deletions
continue past entries waiting for a retry. Each Delete receives a ten-second
context; this depends on the storage implementation honoring cancellation and
is not a hard bound on a blocked filesystem syscall or a custom store.

Retained uncertain intents intentionally block storage migration activation:
switching namespaces could otherwise abandon a late write in the old backend.
Operators can inspect their count and retry state without modifying them:

```sql
SELECT object_key, retain_on_success, attempts, queued_at,
       last_attempt_at, next_attempt_at, last_error
FROM pgbase_meta.file_object_deletions
ORDER BY next_attempt_at, queued_at, object_key;
```

Do not remove an uncertain intent solely because one storage read or Delete
reports that the key is absent. An unresolved tombstone can persist indefinitely;
automated proof of remote-write quiescence and retirement is not implemented.
Monitor accumulation and failed attempts. Keep all cleanup workers on the
schema-69-capable binary: the database guard prevents an older worker from
discarding an uncertain intent, but that worker can repeatedly encounter an
error until upgraded. Restore to a separate local namespace explicitly detaches
the source's pending intents and credentials; it does not modify the source.
Managed restore performs this detachment before applying the restored
application's migrations. Both schema-68 backups without retention metadata and
schema-69 backups with retained intents are supported; the restored application
then applies schema 69 normally.

If retained intents block migration activation, keep the source namespace active
and use this recovery procedure:

1. Stop new variant writes and upgrade or stop every older cleanup worker.
2. Establish that all writers and remote Put requests for the reviewed keys have
   actually finished. A process exit, cancellation, successful Delete, or absent
   read alone is insufficient proof of remote completion.
3. If that proof is unavailable, retain the intents and defer migration activation.
   Copy progress does not justify abandoning the source namespace.
4. After independently confirming quiescence, an operator can clear
   `retain_on_success` for those exact reviewed keys in a maintenance transaction.
   Leave the rows for the current cleanup worker, which checks live references
   before deletion and records any retryable deletion error. Do not bulk-delete
   the queue or infer a safe retirement time from its age.
5. Confirm the relevant intents have drained, then resume migration activation.
   If writes resume before this process completes, re-establish quiescence before
   retiring any remaining retained intent.

Backup archive storage is independent of uploaded-file storage. P8 adds a separate backup repository and administrator configuration; its current integration and acceptance status is tracked in [P8 local contracts](phase4-local-contracts.md). `BackupOptions.Directory` remains the local workspace for jobs, staging, and activation receipts even when archives use S3.
