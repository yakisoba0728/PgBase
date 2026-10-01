# S3 backup archives

PgBase can retain backup archives in an S3-compatible bucket while keeping job receipts, restore activation state, and temporary work in the private local backup directory.

The administrator Settings page exposes **Backups to keep**. This count covers all archives in the selected repository, including manual, uploaded, and scheduled backups; it does not reserve manual archives. A value of `0` keeps every archive. In a managed application, this runtime setting overrides the repository fallback on the Backup storage page, even when scheduled backups are disabled. Changing the count does not immediately delete archives: pruning runs after a successful backup creation or upload. Keep long-lived recovery copies outside the managed repository when they must be exempt from its retention.

```sh
PGBASE_BACKUPS_DIRECTORY=/var/lib/pgbase/backups
PGBASE_BACKUPS_S3_BUCKET=pgbase-backups
PGBASE_BACKUPS_S3_PREFIX=production
PGBASE_BACKUPS_S3_REGION=us-east-1
PGBASE_BACKUPS_RETENTION=14
```

For an S3-compatible service, set `PGBASE_BACKUPS_S3_ENDPOINT`, `PGBASE_BACKUPS_S3_ACCESS_KEY`, `PGBASE_BACKUPS_S3_SECRET_KEY`, and optionally `PGBASE_BACKUPS_S3_PATH_STYLE=true`. These settings are independent from uploaded-file storage.

PgBase uploads an archive before publishing its metadata, so incomplete uploads are not listed or restorable. Downloads and restores verify the recorded archive size and SHA-256 digest while streaming. Managed applications persist backup settings in the project database; environment values seed new settings and do not overwrite existing saved settings. Use the Backup storage administrator page to change them. A destination change is rejected while the current repository still lists archives or has pending publications, so preserve or remove those archives and resolve interrupted work before switching. Job receipts and restore activation state continue to use the private local backup directory.

An interrupted publication keeps a durable database fence and therefore blocks
repository setting changes. Startup waits for a publisher that still owns its
backup ID and promotes the receipt only when both metadata and archive are
readable. If the publisher lost its database session while object storage work
was still running, startup retains the incomplete fence without deleting either
key. To discard that failed backup ID, first stop or quiesce every PgBase process
that could still be publishing it, then delete the exact backup ID through the
administrator API. Quiescence is required because a stale remote upload cannot
be canceled reliably after its database session has already disappeared.
Archive objects carry an immutable PgBase ownership marker. Explicit recovery
refuses to remove an existing object without the matching format and backup ID,
so a UUID collision with an object owned by another application is preserved.
