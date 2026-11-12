import type { AdminSettings } from "../backend/ports";
import { FormSection } from "../components/ui";
export function runtimePolicyError(value: AdminSettings): string | null {
  const ranges: Array<[string, number, number, number]> = [["Batch request count", value.batch.maxRequests, 1, 100], ["Batch body bytes", value.batch.maxBodyBytes, 1024, (148 * 1024 * 1024) + 4096], ["Batch timeout", value.batch.timeoutMs, 100, 300000], ["User account failures", value.rateLimits.user.accountMaxFailures, 1, 10000], ["User network failures", value.rateLimits.user.networkMaxFailures, 1, 10000], ["Registration failures", value.rateLimits.user.registrationNetworkMaxFailures, 1, 10000], ["User failure window", value.rateLimits.user.windowSeconds, 1, 86400], ["User block duration", value.rateLimits.user.blockSeconds, 1, 604800]];
  const invalid = ranges.find(([, v, min, max]) => !Number.isInteger(v) || v < min || v > max);
  return invalid ? `${invalid[0]} must be a whole number from ${invalid[2]} through ${invalid[3]}.` : null;
}
export function RuntimePolicySettings({ value, disabled, change }: { value: AdminSettings; disabled: boolean; change(value: AdminSettings): void }) {
  return <>
    <FormSection disabled={disabled} title="User sign-in and registration limits">
      <p className="settings-help">Limit repeated app-user sign-in failures and registration attempts by account and network.</p>
      <div className="settings-field-grid">{([['accountMaxFailures','User account failures',10000],['networkMaxFailures','User network failures',10000],['registrationNetworkMaxFailures','Registration failures per network',10000],['windowSeconds','User failure window in seconds',86400],['blockSeconds','User block duration in seconds',604800]] as const).map(([key,label,max]) => <label key={key}>{label}<input type="number" min={1} max={max} step={1} value={value.rateLimits.user[key]} onChange={e => change({ ...value, rateLimits: { ...value.rateLimits, user: { ...value.rateLimits.user, [key]: Number(e.target.value) } } })} /></label>)}</div>
    </FormSection>
    <FormSection id="settings-batch" disabled={disabled} title="Batch requests">
      <p className="settings-help">Applies to multi-record requests and atomic record saves with file attachments.</p>
      <label><input type="checkbox" checked={value.batch.enabled} onChange={e => change({ ...value, batch: { ...value.batch, enabled: e.target.checked } })} />Enable batch requests</label>
      <div className="settings-field-grid">{([['maxRequests','Maximum operations per batch',1,100],['maxBodyBytes','Maximum batch body in bytes',1024,(148 * 1024 * 1024)+4096],['timeoutMs','Batch timeout in milliseconds',100,300000]] as const).map(([key,label,min,max]) => <label key={key}>{label}<input type="number" min={min} max={max} step={1} value={value.batch[key]} onChange={e => change({ ...value, batch: { ...value.batch, [key]: Number(e.target.value) } })} /></label>)}</div>
    </FormSection>
  </>;
}
