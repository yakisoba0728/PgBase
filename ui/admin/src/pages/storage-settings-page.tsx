import { StorageMigrationNotFound, StorageMigrationRejected } from "../backend/storage-port";
import { useEffect, useRef, useState } from "react";
import { useBackend, useSession } from "../auth/session";
import type {
  FileStorageMigration,
  FileStorageSettings,
  FileStorageUpdate,
} from "../backend/storage-port";

const editable = (value: FileStorageSettings): FileStorageUpdate => ({ driver: value.driver, bucket: value.bucket, region: value.region, endpoint: value.endpoint, accessKey: value.accessKey, usePathStyle: value.usePathStyle, revision: value.revision });
export function StorageSettingsPage() {
  const { storage } = useBackend();
  const { state: session } = useSession();
  const [saved, setSaved] = useState<FileStorageSettings>();
  const [form, setForm] = useState<FileStorageUpdate>();
  const [secret, setSecret] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const [migration, setMigration] = useState<FileStorageMigration>();
  const [previewInput, setPreviewInput] = useState<string>();
  const generation = useRef(0);
  useEffect(() => {
    const issued = ++generation.current;
    const controller = new AbortController();
    setBusy(true);
    void storage
      .getFiles(controller.signal)
      .then((value) => {
        if (issued === generation.current) {
          setSaved(value);
          setForm(editable(value));
        }
      })
      .catch(() => {
        if (issued === generation.current)
          setMessage("Unable to load file storage settings.");
      })
      .finally(() => {
        if (issued === generation.current) setBusy(false);
      });
    return () => {
      generation.current++;
      controller.abort();
    };
  }, [storage]);
  const [recoveryPending, setRecoveryPending] = useState(true);
  const [startAttemptID, setStartAttemptID] = useState<string>();
  const [missingMigration, setMissingMigration] = useState(false);
  const [pollEnabled, setPollEnabled] = useState(true);
  const [pollID, setPollID] = useState<string>();
  const [pollRevision, setPollRevision] = useState(0);
  const recoveryKey = session.kind === "authenticated"
    ? `${window.location.origin}:pgbase-file-storage-migration:${session.administrator.id}` : "";
  const migrationID = migration?.status === "preview" ? undefined : migration?.id;
  useEffect(() => {
    if (!recoveryKey || !pollEnabled) return;
    let id = pollID;
    try { id ||= window.sessionStorage.getItem(recoveryKey) ?? ""; } catch { /* polling works without persistence */ }
    const controller = new AbortController();
    let timer: number | undefined;
    async function poll() {
      try {
        const value = id ? await storage.migration(id, controller.signal) : await storage.latestMigration(controller.signal);
        if (controller.signal.aborted) return;
        if (!value) { setRecoveryPending(false); return; }
        setMissingMigration(false);
        id = value.id;
        try { window.sessionStorage.setItem(recoveryKey, id); } catch { /* polling works without persistence */ }
        if (controller.signal.aborted) return;
        setMigration(value);
        setStartAttemptID(undefined);
        setRecoveryPending(value.status === "committed");
        if (["committed", "canceled"].includes(value.status)) {
          if (value.status === "committed") {
            const fresh = await storage.getFiles(controller.signal);
            if (controller.signal.aborted) return;
            setSaved(fresh); setForm(editable(fresh)); setSecret(""); setRecoveryPending(false);
          }
          try { window.sessionStorage.removeItem(recoveryKey); } catch { /* unavailable storage */ }
          setMessage(value.status === "committed" ? "Storage migration completed and active." : "Storage migration canceled.");
          return;
        }
        if (value.status === "failed") { setMessage("Storage migration failed. Resume or cancel this migration before starting another."); return; }
      } catch (error) {
        if (controller.signal.aborted) return;
        setMissingMigration(error instanceof StorageMigrationNotFound);
        setMessage(error instanceof StorageMigrationNotFound
          ? "The remembered migration is not present in this database. Status checks will continue."
          : "Migration status is temporarily unavailable; retrying.");
      }
      if (!controller.signal.aborted) timer = window.setTimeout(() => void poll(), 500);
    }
    void poll();
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [pollID, pollRevision, pollEnabled, recoveryKey, storage]);
  function findCurrentMigration() {
    try { window.sessionStorage.removeItem(recoveryKey); } catch { /* server discovery remains available */ }
    setMigration(undefined); setPreviewInput(undefined); setStartAttemptID(undefined);
    setMissingMigration(false); setMessage(undefined); setRecoveryPending(true); setPollID(undefined);
    setPollEnabled(true); setPollRevision(value => value + 1);
  }
  const update = <K extends keyof FileStorageUpdate>(
    key: K,
    value: FileStorageUpdate[K],
  ) => {
    setForm((current) => current && { ...current, [key]: value });
    if (migration && ["preview", "committed", "canceled"].includes(migration.status)) { setMigration(undefined); setPreviewInput(undefined); }
    setMessage(undefined);
  };
  async function save() {
    if (!form) return;
    const issued = ++generation.current;
    setBusy(true);
    setMessage(undefined);
    try {
      const { secretKey: _ignored, ...base } = form;
      const value = await storage.saveFiles(
        secret ? { ...base, secretKey: secret } : base,
      );
      if (issued === generation.current) {
        setSaved(value);
        setForm(editable(value));
        setSecret("");
        setMessage("File storage settings saved and active.");
      }
    } catch {
      if (issued === generation.current)
        setMessage(
          "Unable to save settings. Existing files may require the current storage destination.",
        );
    } finally {
      if (issued === generation.current) setBusy(false);
    }
  }
  async function test() {
    const issued = ++generation.current;
    setBusy(true);
    setMessage(undefined);
    try {
      await storage.testFiles();
      if (issued === generation.current)
        setMessage("S3 upload, read, and delete succeeded.");
    } catch {
      if (issued === generation.current)
        setMessage("S3 connectivity test failed.");
    } finally {
      if (issued === generation.current) setBusy(false);
    }
  }
  const migrationInput = () => {
    if (!form) throw new Error("missing form");
    const { revision: _revision, secretKey: _secretKey, ...base } = form;
    return secret ? { ...base, secretKey: secret } : base;
  };
  async function previewMigration() {
    setBusy(true); setMessage(undefined); setMigration(undefined); setPreviewInput(undefined);
    try { const input = migrationInput(); setMigration(await storage.previewMigration(input)); setPreviewInput(JSON.stringify(input)); }
    catch { setMessage("Unable to preview storage migration."); }
    finally { setBusy(false); }
  }
  async function startMigration() {
    if (!previewInput) return;
    const id = startAttemptID ?? crypto.randomUUID();
    setStartAttemptID(id);
    setBusy(true); setMessage(undefined);
    try { window.sessionStorage.setItem(recoveryKey, id); } catch { /* exact ID remains in memory */ }
    try {
      const value = await storage.startMigration(JSON.parse(previewInput), id);
      setMigration(value); setStartAttemptID(undefined); setPreviewInput(undefined);
      setPollID(id); setPollEnabled(true); setMessage("Storage migration started.");
    } catch (error) {
      if (error instanceof StorageMigrationRejected) {
        setPollEnabled(false); setStartAttemptID(undefined); setRecoveryPending(false);
        try { window.sessionStorage.removeItem(recoveryKey); } catch { /* unavailable storage */ }
        setMessage("Storage migration was rejected. Review the destination and preview again.");
      } else {
        setMessage("Start response unavailable. Checking the saved migration; retry uses the same operation ID.");
        setPollID(id); setPollEnabled(true); setPollRevision(value => value + 1);
      }
    } finally { setBusy(false); }
  }
  async function resumeMigration() {
    if (!migration?.id) return;
    setBusy(true); try { await storage.resumeMigration(migration.id); setPollID(migration.id); setPollRevision(value => value + 1); } catch { setMessage("Unable to resume storage migration. Retry to check its state."); setPollRevision(value => value + 1); } finally { setBusy(false); }
  }
  async function cancelMigration() {
    if (!migration?.id) return;
    setBusy(true); try { await storage.cancelMigration(migration.id); setPollID(migration.id); setPollRevision(value => value + 1); } catch { setMessage("Unable to cancel storage migration. Retry to check its state."); setPollRevision(value => value + 1); } finally { setBusy(false); }
  }
  if (!form)
    return (
      <section>
        <h2>File storage</h2>
        <p aria-live="polite">{message ?? "Loading file storage settings…"}</p>
      </section>
    );
  const dirty = JSON.stringify(form) !== JSON.stringify(saved && editable(saved)) || secret !== "";
  const migrationRequired = !!saved && (form.driver !== saved.driver || form.bucket !== saved.bucket || form.region !== saved.region || form.endpoint !== saved.endpoint || form.usePathStyle !== saved.usePathStyle);
  const migrationActive = !!migration?.id && !["preview", "committed", "canceled"].includes(migration.status);
  return (
    <section>
      <h2>File storage</h2>
      <p>
        Configure storage for uploaded record files. Backup archive storage is configured separately.
      </p>
      <label>
        Driver
        <select
          value={form.driver}
          disabled={busy || recoveryPending || migrationActive || !!startAttemptID}
          onChange={(e) => update("driver", e.target.value as "local" | "s3")}
        >
          <option value="local">Local filesystem</option>
          <option value="s3">S3 compatible</option>
        </select>
      </label>
      {form.driver === "s3" && (
        <fieldset disabled={busy || recoveryPending || migrationActive || !!startAttemptID}>
          <legend>S3 connection</legend>
          <label>
            Endpoint
            <input
              value={form.endpoint}
              onChange={(e) => update("endpoint", e.target.value)}
            />
          </label>
          <label>
            Bucket
            <input
              value={form.bucket}
              onChange={(e) => update("bucket", e.target.value)}
            />
          </label>
          <label>
            Region
            <input
              value={form.region}
              onChange={(e) => update("region", e.target.value)}
            />
          </label>
          <label>
            Access key
            <input
              autoComplete="off"
              value={form.accessKey}
              onChange={(e) => update("accessKey", e.target.value)}
            />
          </label>
          <label>
            Secret key
            <input
              type="password"
              autoComplete="new-password"
              value={secret}
              placeholder={saved?.secretConfigured ? "Configured" : ""}
              onChange={(e) => {
                setSecret(e.target.value);
                if (migration && ["preview", "committed", "canceled"].includes(migration.status)) { setMigration(undefined); setPreviewInput(undefined); }
                setMessage(undefined);
              }}
            />
          </label>
          <label>
            <input
              type="checkbox"
              checked={form.usePathStyle}
              onChange={(e) => update("usePathStyle", e.target.checked)}
            />
            Use path-style addressing
          </label>
        </fieldset>
      )}
      {migrationRequired && (
        <p role="note">
          Use the migration workflow below to copy and verify existing objects before switching destinations.
        </p>
      )}
      <button
        type="button"
        disabled={busy || recoveryPending || migrationActive || !dirty || migrationRequired}
        onClick={() => void save()}
      >
        Save storage settings
      </button>
      {saved?.driver === "s3" && (
        <button
          type="button"
          disabled={busy || recoveryPending || dirty}
          onClick={() => void test()}
        >
          Test saved S3 connection
        </button>
      )}
      {message && <p aria-live="polite">{message}</p>}
      {missingMigration && !startAttemptID && <button type="button" disabled={busy} onClick={findCurrentMigration}>Find current migration</button>}
      {(migrationRequired || !!migration?.id) && (
        <section aria-labelledby="storage-migration-title">
          <h3 id="storage-migration-title">Move existing uploaded files</h3>
          <p>The current destination stays active until every referenced object is copied and verified.</p>
          <button type="button" disabled={busy || recoveryPending || migrationActive || !!startAttemptID || !migrationRequired} onClick={() => void previewMigration()}>Preview migration</button>
          {migration?.status === "preview" && <button type="button" disabled={busy || recoveryPending || migrationActive} onClick={() => void startMigration()}>{startAttemptID ? "Retry migration start" : `Start migration of ${migration.totalObjects} objects`}</button>}
          {migrationID && migration && <p aria-live="polite">Migration {migration.status}: {migration.copiedObjects} of {migration.totalObjects} verified.</p>}
          {migration?.status === "failed" && <button type="button" disabled={busy} onClick={() => void resumeMigration()}>Resume migration</button>}
          {migration?.id && ["copying", "ready", "canceling", "failed"].includes(migration.status) && <button type="button" disabled={busy || migration.status === "canceling"} onClick={() => void cancelMigration()}>Cancel migration</button>}
        </section>
      )}
    </section>
  );
}
