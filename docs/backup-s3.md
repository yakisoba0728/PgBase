# S3 backup archives

PgBase can retain backup archives in an S3-compatible bucket while keeping job receipts, restore activation state, and temporary work in the private local backup directory.

Retention counts all archives in the selected repository, including manual, uploaded, and scheduled backups; it does not reserve manual archives. One value applies, in this order:

1. **Keep** in the Settings page backups section (`backups.keep`), when it is greater than `0`.
2. Otherwise **Backups to keep** on the Backup storage page. `PGBASE_BACKUPS_RETENTION` only seeds that value when the storage settings row is first created; later changes to the variable have no effect. `0` there keeps every archive.

`backups.keep = 0` therefore means "not set here", never "keep everything". Changing either value does not immediately delete archives: pruning runs after a successful backup creation or upload, and a scheduled run first reloads the stored settings so it never uses an older count than the one shown. Keep long-lived recovery copies outside the managed repository when they must be exempt from its retention.

Upgrading from a release in which `pgbase serve` did not register the scheduled backup job: on the first start that registers `pgbase.managed-backup`, pgBase clears a previously stored `backups.enabled` and `backups.keep` (they had no effect under `pgbase serve`) and logs a warning. Scheduled backups therefore stay off and pruning keeps following the Backup storage retention until an administrator sets them again. An embedding application whose own backup manager already registered the job keeps its stored values; there, `backups.keep = 0` now falls back to the Backup storage retention instead of keeping every archive.

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
