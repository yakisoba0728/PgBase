import { useState } from "react";
import type { SchemaPort } from "../backend/ports";

/** A non-persistent SQL view check. The server caps this to ten rows. */
export function ViewSamplePreview({ query, disabled, schema }: { query: string; disabled: boolean; schema?: SchemaPort }) {
  const [sample, setSample] = useState<{ fields: Array<{ name: string }>; items: Array<Record<string, unknown>>; dependencies: string[] } | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<string | null>(null);
  async function preview() {
    if (!schema?.sampleView || pending || query.trim() === "") return;
    setPending(true); setError(null);
    try { setSample(await schema.sampleView(query)); }
    catch { setSample(null); setError("The view query could not be sampled. Correct the query and try again."); }
    finally { setPending(false); }
  }
  if (!schema?.sampleView) return null;
  return <section aria-label="View sample rows">
    <button disabled={disabled || pending || query.trim() === ""} onClick={() => void preview()} type="button">{pending ? "Sampling view…" : "Sample view rows"}</button>
    <p>Runs the SQL without saving the view and shows at most 10 rows.</p>
    {error && <p role="alert">{error}</p>}
    {sample && <><p role="status">Dependencies: {sample.dependencies.length ? sample.dependencies.join(", ") : "none"}.</p>
      <div className="table-scroll"><table><thead><tr>{sample.fields.map((field) => <th key={field.name}>{field.name}</th>)}</tr></thead><tbody>{sample.items.map((item, index) => <tr key={index}>{sample.fields.map((field) => <td key={field.name}>{typeof item[field.name] === "string" ? item[field.name] as string : JSON.stringify(item[field.name])}</td>)}</tr>)}</tbody></table></div>
      {sample.items.length === 0 && <p>The query is valid and returned no sample rows.</p>}</>}
  </section>;
}
