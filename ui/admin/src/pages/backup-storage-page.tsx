import { useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { BackupStorageSettings, BackupStorageUpdate } from "../backend/backup-storage-port";

const editable = (settings: BackupStorageSettings): BackupStorageUpdate => ({
  driver: settings.driver,
  bucket: settings.bucket,
  prefix: settings.prefix,
  region: settings.region,
  endpoint: settings.endpoint,
  accessKey: settings.accessKey,
  usePathStyle: settings.usePathStyle,
  retention: settings.retention,
  revision: settings.revision,
});

export function BackupStoragePage() {
  const { backupStorage } = useBackend();
  const [saved, setSaved] = useState<BackupStorageSettings>();
  const [form, setForm] = useState<BackupStorageUpdate>();
  const [secret, setSecret] = useState("");
  const [clearSecret, setClearSecret] = useState(false);
  const [retentionInput, setRetentionInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string>();
  const generation = useRef(0);
  const request = useRef<AbortController | undefined>(undefined);

  const beginRequest = () => {
    request.current?.abort();
    const controller = new AbortController();
    request.current = controller;
    return { controller, issued: ++generation.current };
  };

  useEffect(() => {
    const { controller, issued } = beginRequest();
    setBusy(true);
    void backupStorage.get(controller.signal).then((value) => {
      if (issued !== generation.current) return;
      setSaved(value);
      setForm(editable(value));
      setRetentionInput(String(value.retention));
    }).catch(() => {
      if (issued === generation.current && !controller.signal.aborted) setMessage("Unable to load backup storage settings.");
    }).finally(() => {
      if (issued === generation.current) setBusy(false);
    });
    return () => {
      generation.current++;
      request.current?.abort();
    };
  }, [backupStorage]);

  if (!form) {
    return <section><h2>Backup archive storage</h2><p aria-live="polite">{message ?? "Loading backup storage settings…"}</p></section>;
  }

  const retention = Number(retentionInput);
  const retentionValid = /^\d+$/.test(retentionInput) && Number.isInteger(retention) && retention <= 2_147_483_647;
  const dirty = JSON.stringify(form) !== JSON.stringify(saved && editable(saved)) || secret !== "" || clearSecret || (retentionValid && retention !== form.retention);
  const set = <K extends keyof BackupStorageUpdate>(key: K, value: BackupStorageUpdate[K]) => {
    setForm((current) => current && { ...current, [key]: value });
    setMessage(undefined);
  };

  async function save() {
    if (!retentionValid) return;
    const { controller, issued } = beginRequest();
    setBusy(true);
    setMessage(undefined);
    const input: BackupStorageUpdate = { ...form!, retention };
    if (secret) input.secretKey = secret;
    if (clearSecret) input.clearSecret = true;
    try {
      const value = await backupStorage.save(input, controller.signal);
      if (issued !== generation.current) return;
      setSaved(value);
      setForm(editable(value));
      setRetentionInput(String(value.retention));
      setSecret("");
      setClearSecret(false);
      setMessage("Backup storage settings saved.");
    } catch {
      if (issued === generation.current && !controller.signal.aborted) setMessage("Unable to save backup storage settings. Existing archives may require the current destination.");
    } finally {
      if (issued === generation.current) setBusy(false);
    }
  }

  async function test() {
    const { controller, issued } = beginRequest();
    setBusy(true);
    setMessage(undefined);
    try {
      await backupStorage.test(controller.signal);
      if (issued === generation.current) setMessage("Saved backup storage upload, read, and delete succeeded.");
    } catch {
      if (issued === generation.current && !controller.signal.aborted) setMessage("Backup storage connectivity test failed.");
    } finally {
      if (issued === generation.current) setBusy(false);
    }
  }

  return <section>
    <h2>Backup archive storage</h2>
    <p>Configure backup archives separately from uploaded record files.</p>
    <label>Driver<select value={form.driver} disabled={busy} onChange={(event) => set("driver", event.target.value as "local" | "s3")}><option value="local">Local filesystem</option><option value="s3">S3 compatible</option></select></label>
    {form.driver === "s3" && <fieldset disabled={busy}>
      <legend>S3 connection</legend>
      <label>Endpoint<input value={form.endpoint} onChange={(event) => set("endpoint", event.target.value)} /></label>
      <label>Bucket<input value={form.bucket} onChange={(event) => set("bucket", event.target.value)} /></label>
      <label>Prefix<input value={form.prefix} onChange={(event) => set("prefix", event.target.value)} /></label>
      <label>Region<input value={form.region} onChange={(event) => set("region", event.target.value)} /></label>
      <label>Access key<input autoComplete="off" value={form.accessKey} onChange={(event) => set("accessKey", event.target.value)} /></label>
      <label>Secret key<input type="password" autoComplete="new-password" value={secret} disabled={clearSecret} placeholder={saved?.secretConfigured ? "Configured" : ""} onChange={(event) => { setSecret(event.target.value); setClearSecret(false); setMessage(undefined); }} /></label>
      {saved?.secretConfigured && <label><input type="checkbox" checked={clearSecret} onChange={(event) => { const checked = event.target.checked; setClearSecret(checked); if (checked) setSecret(""); set("accessKey", checked ? "" : saved.accessKey); }} />Clear saved access and secret keys and use the default credential provider</label>}
      <label><input type="checkbox" checked={form.usePathStyle} onChange={(event) => set("usePathStyle", event.target.checked)} />Use path-style addressing</label>
    </fieldset>}
    <label>Backups to retain<input type="number" min={0} step={1} disabled={busy} value={retentionInput} aria-invalid={!retentionValid} aria-describedby="backup-retention-help" onChange={(event) => { setRetentionInput(event.target.value); setMessage(undefined); }} /></label>
    <p id="backup-retention-help">This is the repository fallback. Managed apps use Settings &gt; Backups to keep instead, even when automatic backups are disabled. Both count manual, uploaded, and scheduled archives; 0 keeps every archive.</p>
    {!retentionValid && <p role="alert">Retention must be a whole number from 0 through 2147483647.</p>}
    <button type="button" disabled={busy || !dirty || !retentionValid} onClick={() => void save()}>Save backup storage</button>
    <button type="button" disabled={busy || dirty || !retentionValid} onClick={() => void test()}>Test backup storage</button>
    <p aria-live="polite">{message}</p>
  </section>;
}
