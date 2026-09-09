import { useState } from "react";
import type { CollectionDefinition, FieldDefinition } from "../backend/ports";

function sample(field: FieldDefinition): unknown {
  if (field.options?.allowedValues?.length) return field.type === "jsonb" ? field.options.allowedValues.slice(0, field.options.minSelect || 1) : field.options.allowedValues[0];
  if (field.options?.format === "email") return `person@${field.options.allowedDomains?.[0] ?? "example.test"}`;
  if (field.options?.format === "url") return `https://${field.options.allowedDomains?.[0] ?? "example.test"}/`;
  if (field.type === "boolean") return true;
  if (["integer", "bigint", "numeric"].includes(field.type)) return field.options?.min ?? "1";
  if (field.type === "uuid") return "00000000-0000-4000-8000-000000000001";
  if (field.type === "timestamptz") return "2026-01-01T00:00:00Z";
  if (field.type === "editor") return "<p>Example content</p>";
  if (field.type === "geoPoint") return { lon: 127.5, lat: 37.5 };
  if (field.type === "password") return "REPLACE_WITH_PASSWORD";
  if (field.type === "jsonb") return {};
  if (field.type === "relation") return field.relation?.cardinality === "multiple" ? ["RELATED_RECORD_ID"] : "RELATED_RECORD_ID";
  return "example";
}

export function collectionExample(collection: CollectionDefinition, origin: string, audience: "user" | "anonymous" | "service"): string {
  const collectionName = JSON.stringify(collection.name);
  const prefix = audience === "anonymous" ? "public" : audience;
  const base = `${origin}/api/v1/${prefix}/collections/${encodeURIComponent(collection.name)}/records`;
  const input = Object.fromEntries(collection.fields.filter(field => !field.hidden && field.type !== "file" && field.type !== "autodate" && (field.default === undefined || ["editor", "geoPoint", "password"].includes(field.type))).map(field => [field.name, sample(field)]));
  const scalar = collection.fields.find(field => field.type === "text");
  const filter = scalar ? `${scalar.name} = 'example'` : "";
  const relation = collection.fields.find(field => field.type === "relation");
  const setup = audience === "user" ? `import { PgBaseClient } from "@pgbase/sdk";\n\nconst client = new PgBaseClient(${JSON.stringify(origin)});\nawait client.login("person@example.test", "YOUR_PASSWORD");\nconst records = client.records(${collectionName});\n\nconst query = { limit: 20, sort: "id"${filter ? `, filter: ${JSON.stringify(filter)}` : ""}${relation ? `, expand: ${JSON.stringify(relation.name)}` : ""} };\nconst firstPage = await records.list(query);\nif (firstPage.nextCursor) {\n  const nextPage = await records.list({ ...query, cursor: firstPage.nextCursor });\n  console.log(nextPage.items);\n}` : `const endpoint = ${JSON.stringify(base)};\nconst headers = ${audience === "service" ? '{ Authorization: "Bearer YOUR_SERVICE_TOKEN" }' : '{}'};\nconst response = await fetch(endpoint + "?limit=20&sort=id", { headers });\nif (!response.ok) throw new Error("Request failed: " + response.status);\nconst page = await response.json();\nif (page.nextCursor) {\n  const next = new URL(endpoint);\n  next.searchParams.set("cursor", page.nextCursor);\n  next.searchParams.set("limit", "20");\n  next.searchParams.set("sort", "id");\n  console.log(await (await fetch(next, { headers })).json());\n}`;
  if (collection.view || audience === "anonymous") return setup;
  const requiredFiles = collection.fields.filter(field => field.type === "file" && field.required);
  if (requiredFiles.length) {
    const body = { ...input, ...Object.fromEntries(requiredFiles.map((field, index) => [field.name, { $file: `attachment${index}` }])) };
    const pickers = requiredFiles.map((field, index) => `// Add <input type="file" name="${field.name}"> to your form.
const picker${index} = document.querySelector(${JSON.stringify(`input[type="file"][name="${field.name}"]`)});
if (!(picker${index} instanceof HTMLInputElement) || !picker${index}.files?.[0]) throw new Error(${JSON.stringify(`Select a file for ${field.name}`)});
const file${index} = picker${index}.files[0];`).join("\n");
    const request = { method: "POST", url: `/api/v1/collections/${collection.name}/records`, body };
    const requestCode = `{ ...${JSON.stringify(request, null, 2)}, headers: { "Idempotency-Key": crypto.randomUUID() } }`;
    if (audience === "user") return setup + `

// Required attachments and the record are committed together.
${pickers}
const created = await client.batchMultipart([${requestCode}], { ${requiredFiles.map((_, index) => `attachment${index}: { blob: file${index}, filename: file${index}.name }`).join(", ")} });
console.log(created);`;
    return setup + `

// Required attachments and the record are committed together.
${pickers}
const form = new FormData();
form.append("requests", JSON.stringify({ requests: [${requestCode}] }));
${requiredFiles.map((_, index) => `form.append("attachment${index}", file${index}, file${index}.name);`).join("\n")}
const created = await fetch(${JSON.stringify(`${origin}/api/v1/service/collections/batch`)}, { method: "POST", headers, body: form });
if (!created.ok) throw new Error("Create failed: " + created.status);
console.log(await created.json());`;
  }
  if (audience === "service") return setup + `\n\n// Supply the token with create/update/delete scopes.\nconst created = await fetch(endpoint, { method: "POST", headers: { ...headers, "Content-Type": "application/json", "Idempotency-Key": crypto.randomUUID() }, body: JSON.stringify(${JSON.stringify(input, null, 2)}) });\nif (!created.ok) throw new Error("Create failed: " + created.status);\nconst record = await created.json();\nconst etag = created.headers.get("ETag");\nif (!etag) throw new Error("Missing ETag");\n// Use this ETag as If-Match for PATCH or DELETE of this record.\nconsole.log(record, etag);`;
  return setup + `\n\n// Review sample values against this collection’s constraints.\n// Reuse this idempotency key only when retrying this exact create body.\nconst idempotencyKey = crypto.randomUUID();\nconst created = await records.createWithETag(${JSON.stringify(input, null, 2)}, idempotencyKey);\nconst current = await records.getWithETag(String(created.record.id));\nif (!current.etag) throw new Error("Missing ETag");\nconst updated = await records.updateWithETag(String(current.record.id), ${JSON.stringify(input, null, 2)}, current.etag);\nif (!updated.etag) throw new Error("Missing ETag");\n// Uncomment only when you intend to delete the example record.\n// await records.remove(String(updated.record.id), updated.etag);`;
}

export function CollectionAPIExamples({ collection }: { collection: CollectionDefinition }) {
  const [audience, setAudience] = useState<"user" | "anonymous" | "service">("user");
  const [copied, setCopied] = useState("");
  const code = collectionExample(collection, window.location.origin, audience);
  return <details><summary>API examples for {collection.name}</summary>
    <label>API identity<select value={audience} onChange={event => { setAudience(event.target.value as typeof audience); setCopied(""); }}><option value="user">Signed-in user · TypeScript SDK</option><option value="anonymous">Anonymous · Fetch</option><option value="service">Service token · Fetch</option></select></label>
    <p>Access must be enabled by this collection’s rules. These examples do not grant access. The SDK is available as a local package; install its npm pack archive first.</p>
    {collection.view && <p>This collection is a read-only view; mutation examples are omitted.</p>}
    <pre><code>{code}</code></pre>
    <button type="button" onClick={() => void navigator.clipboard.writeText(code).then(() => setCopied("Example copied.")).catch(() => setCopied("Copy unavailable. Select the example text to copy it."))}>Copy API example</button>
    {copied && <p role="status">{copied}</p>}
    <p>Keep filter, sort, limit and expand unchanged while following nextCursor. A 412 response means the record changed: read it again before deciding whether to retry. Numeric field values may use decimal strings to preserve precision.</p>
    {collection.fields.some(field => field.type === "relation") && <p>Relation values are record UUIDs (arrays for multiple relations). Replace RELATED_RECORD_ID with an accessible existing record. Use expand with the relation field name to retrieve related data.</p>}
    {collection.fields.some(field => field.type === "file") && <p>Required files are uploaded atomically with record creation through multipart Batch. Optional files can also use client.files(collection, recordId).upload(field, file, file.name) after creation. Download tokens are scoped to one file and variant.</p>}
    <table><thead><tr><th>Field</th><th>Type</th><th>Validation</th></tr></thead><tbody>{collection.fields.map(field => <tr key={field.id ?? field.name}><td>{field.name}</td><td>{field.type}</td><td>{field.required ? "SQL NULL rejected. " : "Optional. "}{JSON.stringify(field.options ?? {})}</td></tr>)}</tbody></table>
  </details>;
}
