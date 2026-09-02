import { LosslessNumber } from "lossless-json";
import { afterEach, describe, expect, it, vi } from "vitest";
import { httpBackend } from "./http-backend";
import { CreateAttempt, RecordError } from "./ports";

const recordID = "aaaaaaaa-bbbb-4ccc-8ddd-eeeeeeeeeeee";
const firstTag = "11111111-1111-4111-8111-111111111111";
const secondTag = "22222222-2222-4222-8222-222222222222";

function respond(status: number, body = "", headers?: HeadersInit): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ "content-type": "application/json", ...headers }),
    text: vi.fn().mockResolvedValue(body),
  } as unknown as Response;
}

function envelope(code: string, details: Record<string, string[]> = {}): string {
  return JSON.stringify({ error: { code, message: "message", details } });
}

function stub(...responses: Response[]): ReturnType<typeof vi.fn> {
  const mock = vi.fn();
  for (const response of responses) mock.mockResolvedValueOnce(response);
  vi.stubGlobal("fetch", mock);
  return mock;
}

function headerOf(mock: ReturnType<typeof vi.fn>, call: number, name: string): string | null {
  return new Headers((mock.mock.calls[call][1] as RequestInit).headers).get(name);
}

async function authenticated(mock: ReturnType<typeof vi.fn>) {
  const backend = httpBackend();
  await backend.session.login({ email: "admin@example.com", password: "x".repeat(12) });
  void mock;
  return backend;
}

const session = () => respond(200, JSON.stringify({
  administrator: { id: "admin", email: "admin@example.com", createdAt: "2026-08-28T00:00:00Z" },
  csrfToken: "csrf-token",
  expiresAt: "2026-09-02T00:00:00Z",
}));

afterEach(() => vi.unstubAllGlobals());

describe("HTTP adapter wire contract", () => {
  it("reads and writes bodies losslessly", async () => {
    const mock = stub(session(), respond(200, `{"items":[{"sequence":9007199254740993,"ratio":1.234567890123456789}]}`));
    const backend = await authenticated(mock);

    const page = await backend.records.list("posts", {});
    expect(page.items[0].sequence).toBeInstanceOf(LosslessNumber);
    expect(String(page.items[0].ratio)).toBe("1.234567890123456789");
  });

  it("rejects a body that repeats a JSON key instead of keeping the last one", async () => {
    const mock = stub(session(), respond(200, `{"items":[],"items":[{"id":"one"}]}`));
    const backend = await authenticated(mock);

    await expect(backend.records.list("posts", {})).rejects.toBeInstanceOf(RecordError);
  });

  it("treats an unreadable success body as a failure rather than an empty result", async () => {
    const mock = stub(session(), respond(200, "<html><body>502 Bad Gateway</body></html>"));
    const backend = await authenticated(mock);

    await expect(backend.records.list("posts", {})).rejects.toMatchObject({ reason: "failed" });
  });

  it("accepts an empty successful body", async () => {
    const mock = stub(session(), respond(204));
    const backend = await authenticated(mock);

    await expect(backend.session.logout()).resolves.toBeUndefined();
  });

  it("reports a failure whose body cannot be read", async () => {
    const mock = stub(session(), respond(412, "<html><body>proxy</body></html>"));
    const backend = await authenticated(mock);

    await expect(backend.records.get("posts", recordID)).rejects.toMatchObject({ reason: "stale" });
  });

  it("never calls response.json() so the shipped parser is not bypassed", async () => {
    const json = vi.fn().mockResolvedValue({ items: [] });
    const mock = stub(session(), { ok: true, status: 200, headers: new Headers(), json } as unknown as Response);
    const backend = await authenticated(mock);

    await expect(backend.records.list("posts", {})).rejects.toBeDefined();
    expect(json).not.toHaveBeenCalled();
  });

  it("announces the session expiry for a protected 401 and stays silent for the authentication flow", async () => {
    const mock = stub(
      respond(401, envelope("authentication_required")),
      respond(200, JSON.stringify({ setupRequired: false })),
      respond(401, envelope("invalid_credentials")),
      respond(401, envelope("authentication_required")),
    );
    const backend = httpBackend();
    const expired = vi.fn();
    backend.session.onExpired(expired);

    await expect(backend.session.resume()).resolves.toEqual({ kind: "anonymous" });
    await expect(backend.session.login({ email: "a@b.test", password: "x" })).rejects.toBeDefined();
    expect(expired).not.toHaveBeenCalled();

    await expect(backend.schema.catalog()).rejects.toBeDefined();
    expect(expired).toHaveBeenCalledOnce();
    void mock;
  });

  it("sends one credential-included JSON POST per setup attempt", async () => {
    const mock = stub(respond(400, envelope("validation_failed", { email: ["invalid"] })));

    await expect(httpBackend().session.setup({ setupToken: "first-run", email: "admin@example.com", password: "a secure password" }))
      .rejects.toMatchObject({ reason: "invalid-input" });

    expect(mock).toHaveBeenCalledTimes(1);
    const [path, options] = mock.mock.calls[0] as [string, RequestInit];
    expect(path).toBe("/api/admin/setup");
    expect(options.method).toBe("POST");
    expect(options.credentials).toBe("include");
    expect(headerOf(mock, 0, "Content-Type")).toBe("application/json");
    expect(headerOf(mock, 0, "Accept")).toBe("application/json");
  });

  it("sends both CSRF headers on logout using the token the session issued", async () => {
    const mock = stub(session(), respond(204));
    const backend = await authenticated(mock);

    await backend.session.logout();

    expect(mock.mock.calls[1][0]).toBe("/api/admin/auth/logout");
    expect(headerOf(mock, 1, "X-CSRF-Token")).toBe("csrf-token");
    expect(headerOf(mock, 1, "X-PgBase-CSRF")).toBe("csrf-token");
  });

  it("forwards the abort signal to the session probe", async () => {
    const mock = stub(session());
    const signal = new AbortController().signal;

    await httpBackend().session.resume(signal);

    expect(mock).toHaveBeenCalledWith("/api/admin/session", expect.objectContaining({ signal }));
  });

  it("asks for one bounded page and carries cursor, filter and sort in the query", async () => {
    const mock = stub(session(), respond(200, `{"items":[]}`));
    const backend = await authenticated(mock);

    await backend.records.list("po sts", { cursor: "c1", filter: "title = 'x'", sort: "-created_at" });

    expect(mock.mock.calls[1][0]).toBe("/api/v1/collections/po%20sts/records?limit=50&cursor=c1&filter=title+%3D+%27x%27&sort=-created_at");
  });

  it("sends idempotency, precondition and CSRF headers with the exact request body", async () => {
    const mock = stub(
      session(),
      respond(201, `{"id":"one"}`, { ETag: '"pgb1.created"' }),
      respond(200, `{"id":"one","title":"new"}`, { ETag: '"pgb1.updated"' }),
      respond(204),
    );
    const backend = await authenticated(mock);

    const created = await backend.records.create(new CreateAttempt("posts", () => "11111111-1111-4111-8111-111111111111"), { sequence: new LosslessNumber("9007199254740993") });
    const updated = await backend.records.update(created, { title: "new" });
    await backend.records.remove(updated);

    expect(headerOf(mock, 1, "Idempotency-Key")).toBe("11111111-1111-4111-8111-111111111111");
    expect(headerOf(mock, 1, "X-PgBase-CSRF")).toBe("csrf-token");
    expect((mock.mock.calls[1][1] as RequestInit).body).toBe(`{"sequence":9007199254740993}`);
    expect(headerOf(mock, 2, "If-Match")).toBe('"pgb1.created"');
    expect(headerOf(mock, 3, "If-Match")).toBe('"pgb1.updated"');
    expect((mock.mock.calls[3][1] as RequestInit).method).toBe("DELETE");
  });

  it("reads a strong ETag into the record version", async () => {
    const mock = stub(session(), respond(200, `{"id":"one","sequence":9007199254740993}`, { ETag: '"pgb0.legacy"' }), respond(200, `{"id":"one"}`, { ETag: '"pgb0.next"' }));
    const backend = await authenticated(mock);

    const record = await backend.records.get("posts", "one");
    expect(record.data.sequence).toBeInstanceOf(LosslessNumber);
    await backend.records.update(record, {});
    expect(headerOf(mock, 2, "If-Match")).toBe('"pgb0.legacy"');
  });

  it.each([undefined, "*", 'W/"pgb1.weak"', '"two", "tags"'])("fails closed for an absent or non-strong ETag: %s", async (etag) => {
    const mock = stub(session(), respond(200, `{"id":"one"}`, etag === undefined ? undefined : { ETag: etag }));
    const backend = await authenticated(mock);

    await expect(backend.records.get("posts", "one")).rejects.toMatchObject({ reason: "failed" });
  });

  it("keeps relation values byte-identical across a round trip", async () => {
    const mock = stub(
      session(),
      respond(200, `{"id":"one","owner":null,"tags":["${secondTag}","${firstTag}"]}`, { ETag: '"pgb1.read"' }),
      respond(200, `{"id":"one","owner":"${firstTag}","tags":["${secondTag}","${firstTag}"]}`, { ETag: '"pgb1.updated"' }),
    );
    const backend = await authenticated(mock);

    const record = await backend.records.get("posts", "one");
    expect(record.data).toMatchObject({ owner: null, tags: [secondTag, firstTag] });
    await backend.records.update(record, { owner: firstTag, tags: [secondTag, firstTag] });

    expect((mock.mock.calls[2][1] as RequestInit).body).toBe(`{"owner":"${firstTag}","tags":["${secondTag}","${firstTag}"]}`);
  });

  it("posts the approved checksum and base version with the CSRF header", async () => {
    const mock = stub(
      session(),
      respond(200, `{"baseVersion":7,"checksum":"approved","definition":{"fields":[]},"sql":["CREATE TABLE posts"]}`),
      respond(200, "{}"),
    );
    const backend = await authenticated(mock);

    const plan = await backend.schema.plan({ operation: "create", name: "posts", fields: [] });
    await backend.schema.apply(plan);

    expect(mock.mock.calls[1][0]).toBe("/api/admin/collections/plan");
    expect(headerOf(mock, 1, "X-PgBase-CSRF")).toBe("csrf-token");
    expect(mock.mock.calls[2][0]).toBe("/api/admin/collections/apply");
    expect(JSON.parse(String((mock.mock.calls[2][1] as RequestInit).body))).toEqual({
      operation: "create", name: "posts", fields: [], checksum: "approved", baseVersion: 7,
    });
  });

  it("re-reads the catalog only to separate a same-version conflict from a moved catalog", async () => {
    const mock = stub(
      session(),
      respond(200, `{"baseVersion":7,"checksum":"approved","definition":{"fields":[]},"sql":[]}`),
      respond(409, envelope("schema_conflict")),
      respond(200, `{"version":9,"collections":{}}`),
    );
    const backend = await authenticated(mock);

    const plan = await backend.schema.plan({ operation: "create", name: "posts", fields: [] });
    await expect(backend.schema.apply(plan)).rejects.toMatchObject({ reason: "catalog-moved" });
    expect(mock.mock.calls[3][0]).toBe("/api/admin/collections");
  });

  it("never re-reads the catalog for a refused deletion", async () => {
    const mock = stub(
      session(),
      respond(200, `{"baseVersion":7,"checksum":"drop","definition":{"fields":[]},"sql":["DROP TABLE posts"]}`),
      respond(409, envelope("collection_referenced", { name: ["collection_referenced"] })),
    );
    const backend = await authenticated(mock);

    const plan = await backend.schema.plan({ operation: "delete", name: "posts" });
    await expect(backend.schema.apply(plan)).rejects.toMatchObject({ reason: "referenced" });
    expect(mock).toHaveBeenCalledTimes(3);
  });
});
