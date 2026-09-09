# Uploaded-file storage settings

PgBase can keep uploaded record files in its configured local directory or an S3-compatible bucket. Administrators can read, save, and test the active uploaded-file storage from the Storage page. These settings do not change where managed backup archives are stored.

S3 settings include bucket, region, endpoint, access key, secret key, and path-style addressing. PgBase encrypts the secret key with the project token secret and never returns it. A blank secret field preserves the configured secret. Saving uses a revision check, validates the candidate with a small upload/read/delete probe, and applies the committed generation to subsequent file operations without restarting the process. Every process checks the database generation before object access, so other PgBase processes converge on the saved credentials.

HTTPS endpoints are required by default. Local MinIO development can opt into an HTTP loopback endpoint with `PGBASE_FILES_S3_ALLOW_INSECURE_LOOPBACK=true`. This flag does not permit plaintext remote endpoints.

Changing credentials for the same bucket is supported. Directly changing between local and S3, or changing an S3 bucket, endpoint, region, or path-style namespace, remains rejected while uploaded, staged, variant, or cleanup state exists. Use the administrator storage migration workflow for a populated destination.

Storage migration records encrypted immutable source and destination snapshots plus a durable ledger for original, thumbnail, variant, and cleanup objects. The active destination continues serving reads and writes while referenced objects are copied and SHA-256 verified. Upload and variant publication share the final transition fence, and the worker performs a final reference scan before atomically activating the verified destination. A stopped or failed worker can resume from its ledger; cancel and copy failure leave the original destination active. After cutover, source deletion is delayed and retried. Cleanup refuses to delete from a namespace that has become active again or is used by another active migration.

Starting a migration requires a client-generated UUID in `PgBase-Operation-ID`. Retrying that UUID with the exact same destination returns the durable migration; reusing it with different input is rejected. The latest-migration endpoint supports cross-tab discovery, while the operation UUID provides exact recovery when a start response is lost.

The migration copies one object at a time through a bounded temporary file. The worker rejects objects larger than 8 GiB; the public uploaded-file configuration currently has a lower 100 MiB maximum. Operators should verify capacity for the temporary directory and retain a provider-level backup before a large move.

The connection test proves the permissions PgBase uses: put, get, and delete for a random bounded object. It does not require bucket listing. A successful local MinIO test does not validate production DNS, TLS trust, IAM policy, lifecycle, replication, or availability.

Backup archive storage is independent of uploaded-file storage. P8 adds a separate backup repository and administrator configuration; its current integration and acceptance status is tracked in [P8 local contracts](phase4-local-contracts.md). `BackupOptions.Directory` remains the local workspace for jobs, staging, and activation receipts even when archives use S3.
