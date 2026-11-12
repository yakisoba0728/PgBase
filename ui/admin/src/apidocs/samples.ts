import { stringifyJson } from "../api/http";
import type { Collection } from "../api/collections";
import { recordIdExample, requestBody, type Audience, type Endpoint } from "./endpoints";

// Code samples follow the real call shapes of sdk/typescript/src/index.ts and
// sdk/dart/lib/src (records.dart, client.dart, auth_protocol.dart, files.dart).

export const languages = ["curl", "ts", "dart"] as const;
export type Language = (typeof languages)[number];

/** POSIX shell quoting: single quotes unless the text contains one. */
export function shellQuote(text: string): string {
  if (!text.includes("'")) return `'${text}'`;
  return `"${text.replace(/[\\"$`]/g, (char) => `\\${char}`)}"`;
}

function curl(endpoint: Endpoint, collection: Collection, origin: string, audience: Audience): string {
  const path = endpoint.path.replace("{id}", recordIdExample(collection));
  const lines = [`curl${endpoint.id === "realtime" ? " -N" : ""}${endpoint.query ? " --get" : ""}${endpoint.method === "GET" || (endpoint.method === "POST" && endpoint.body) ? "" : ` -X ${endpoint.method}`} ${shellQuote(origin + path)}`];
  for (const [key, value] of Object.entries(endpoint.query ?? {})) lines.push(`--data-urlencode ${shellQuote(`${key}=${value}`)}`);
  if (endpoint.userOnly || (audience === "user" && endpoint.rule)) lines.push(`-H "Authorization: Bearer $PGBASE_TOKEN"`);
  for (const [key, value] of Object.entries(endpoint.headers ?? {})) lines.push(`-H ${shellQuote(`${key}: ${value}`)}`);
  if (endpoint.id.startsWith("files-")) lines.push(`-F 'file=@photo.jpg'`);
  if (endpoint.body) lines.push(`-H 'Content-Type: application/json'`, `--data ${shellQuote(stringifyJson(endpoint.body))}`);
  return lines.join(" \\\n  ");
}

const json = (value: unknown) => JSON.stringify(value);

function typescript(endpoint: Endpoint, collection: Collection, origin: string): string {
  const name = JSON.stringify(collection.name);
  const id = JSON.stringify(recordIdExample(collection));
  const body = json(requestBody(collection));
  const head = `import { PgBaseClient } from "@pgbase/sdk";\n\nconst client = new PgBaseClient(${JSON.stringify(origin)});\n`;
  const records = `const records = client.records(${name});\n`;
  const realm = `const realm = client.realm(${name});\n`;
  const code: Record<string, string> = {
    list: `${records}const page = await records.list(${json({ ...endpoint.query, limit: 20 })});\n// page.items, page.nextCursor`,
    view: `${records}const record = await records.get(${id});`,
    create: `${records}const record = await records.create(${body}, "create-0000000001");`,
    update: `${records}const { etag } = await records.getWithETag(${id});\nconst record = await records.update(${id}, ${body}, etag!);`,
    delete: `${records}const { etag } = await records.getWithETag(${id});\nawait records.remove(${id}, etag!);`,
    batch: `const { results } = await client.batch([\n  { operation: "create", collection: ${name}, input: ${body}, idempotencyKey: "batch-create-000001" },\n]);`,
    realtime: `const unsubscribe = await client.realtime.subscribe(${JSON.stringify(`${collection.name}/*`)}, (event) => {\n  console.log(event);\n});`,
    authMethods: `${realm}const methods = await realm.methods();`,
    authRegister: `${realm}const session = await realm.register("person@example.com", "a-long-passphrase");`,
    authLogin: `${realm}const session = await realm.login("person@example.com", "a-long-passphrase");`,
    authRefresh: `${realm}const session = await realm.refresh();`,
    authProfile: `${realm}const profile = await realm.profile();`,
    authReset: `${realm}await realm.requestPasswordReset("person@example.com");`,
  };
  const field = endpoint.id.startsWith("files-") ? endpoint.id.slice("files-".length) : "";
  const snippet = field ? `const info = await client.files(${name}, ${id}).upload(${JSON.stringify(field)}, file, "photo.jpg");` : code[endpoint.id];
  return `${head}\n${snippet}`;
}

/** Dart has exact JSON value classes instead of plain maps. */
export function dartValue(value: unknown): string {
  if (value === null || value === undefined) return "const JsonNull()";
  if (typeof value === "string") return `const JsonString(${dartString(value)})`;
  if (typeof value === "number") return `JsonNumber('${value}')`;
  if (typeof value === "boolean") return `const JsonBool(${value})`;
  if (Array.isArray(value)) return `JsonArray([${value.map(dartValue).join(", ")}])`;
  const entries = Object.entries(value as Record<string, unknown>).map(([key, item]) => `${dartString(key)}: ${dartValue(item)}`);
  return `JsonObject({${entries.join(", ")}})`;
}

const dartString = (text: string) => `'${text.replace(/[\\'$]/g, (char) => `\\${char}`)}'`;

function dart(endpoint: Endpoint, collection: Collection, origin: string): string {
  const name = dartString(collection.name);
  const id = dartString(recordIdExample(collection));
  const body = dartValue(requestBody(collection));
  const head = `import 'package:pgbase/pgbase.dart';\n\nfinal client = PgBaseClient(Uri.parse(${dartString(origin)}));\n`;
  const records = `final records = client.records(${name});\n`;
  const realm = `final realm = client.realm(${name});\n`;
  const query = Object.entries(endpoint.query ?? {}).map(([key, value]) => (key === "limit" ? `limit: ${value}` : `${key}: ${dartString(value)}`));
  const code: Record<string, string> = {
    list: `${records}final page = await records.list(Query(${query.join(", ")}));\n// page.items, page.nextCursor`,
    view: `${records}final record = await records.get(${id});`,
    create: `${records}final record = await records.create(${body}, 'create-0000000001');`,
    update: `${records}final current = await records.getWithEtag(${id});\nfinal record = await records.update(${id}, ${body}, current.etag!);`,
    delete: `${records}final current = await records.getWithEtag(${id});\nawait records.remove(${id}, current.etag!);`,
    batch: `final result = await client.batch([\n  BatchOperation.create(collection: ${name}, input: ${body}, idempotencyKey: 'batch-create-000001'),\n]);`,
    realtime: `final unsubscribe = await client.realtime.subscribe(${dartString(`${collection.name}/*`)}, (RealtimeEvent event) {\n  print(event.record);\n});`,
    authMethods: `${realm}final methods = await realm.methods();`,
    authRegister: `${realm}final session = await realm.register('person@example.com', 'a-long-passphrase');`,
    authLogin: `${realm}final session = await realm.login('person@example.com', 'a-long-passphrase');`,
    authRefresh: `${realm}final session = await realm.refresh();`,
    authProfile: `${realm}final profile = await realm.profile();`,
    authReset: `${realm}await realm.requestPasswordReset('person@example.com');`,
  };
  const field = endpoint.id.startsWith("files-") ? endpoint.id.slice("files-".length) : "";
  const snippet = field
    ? `// import 'dart:io';\nfinal file = File('photo.jpg');\nfinal info = await client.files(${name}, ${id}).upload(${dartString(field)}, UploadSource(open: file.openRead, length: await file.length(), filename: 'photo.jpg'));`
    : code[endpoint.id];
  return `${head}\n${snippet}`;
}

export function codeSample(language: Language, endpoint: Endpoint, collection: Collection, origin: string, audience: Audience): string {
  if (language === "curl") return curl(endpoint, collection, origin, audience);
  return language === "ts" ? typescript(endpoint, collection, origin) : dart(endpoint, collection, origin);
}
