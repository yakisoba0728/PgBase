import { LosslessNumber } from "lossless-json";
import { afterEach, describe, expect, it, vi } from "vitest";
import { APIClient, APIClientError } from "./client";
import { CreateAttempt, RecordsAPI } from "./records";

function response(status: number, body?: string, headers?: HeadersInit): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ "content-type": "application/json", ...headers }),
    text: vi.fn().mockResolvedValue(body ?? ""),
  } as unknown as Response;
}

afterEach(() => vi.unstubAllGlobals());

describe("RecordsAPI mutation metadata", () => {
  it("keeps a create key for identical retries and rotates it after input changes or success", () => {
    const generate = vi.fn()
      .mockReturnValueOnce("11111111-1111-4111-8111-111111111111")
      .mockReturnValueOnce("22222222-2222-4222-8222-222222222222")
      .mockReturnValueOnce("33333333-3333-4333-8333-333333333333");
    const attempt = new CreateAttempt(generate);
    const initial = { sequence: new LosslessNumber("9007199254740993") };

    expect(attempt.keyFor(initial)).toBe("11111111-1111-4111-8111-111111111111");
    expect(attempt.keyFor(initial)).toBe("11111111-1111-4111-8111-111111111111");
    expect(attempt.keyFor({ sequence: new LosslessNumber("9007199254740994") })).toBe("22222222-2222-4222-8222-222222222222");
    attempt.reset();
    expect(attempt.keyFor({ sequence: new LosslessNumber("9007199254740994") })).toBe("33333333-3333-4333-8333-333333333333");
    expect(generate).toHaveBeenCalledTimes(3);
  });

  it("returns GET ETags while preserving lossless record numbers", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(200, `{"id":"one","sequence":9007199254740993}`, { ETag: '"pgb0.legacy"' })));

    const result = await new RecordsAPI(new APIClient(), "csrf").get("posts", "one");

    expect(result.etag).toBe('"pgb0.legacy"');
    expect(result.data.sequence).toBeInstanceOf(LosslessNumber);
  });

  it("sends idempotency and precondition headers on safe mutations", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response(201, `{"id":"one"}`, { ETag: '"pgb1.created"', "Idempotency-Replayed": "false" }))
      .mockResolvedValueOnce(response(200, `{"id":"one","title":"new"}`, { ETag: '"pgb1.updated"' }))
      .mockResolvedValueOnce(response(204));
    vi.stubGlobal("fetch", fetchMock);
    const records = new RecordsAPI(new APIClient(), "csrf-token");

    await records.create("posts", { sequence: new LosslessNumber("9007199254740993") }, "11111111-1111-4111-8111-111111111111");
    await records.update("posts", "one", { title: "new" }, '"pgb1.created"');
    await records.delete("posts", "one", '"pgb1.updated"');

    const createHeaders = new Headers((fetchMock.mock.calls[0][1] as RequestInit).headers);
    expect(createHeaders.get("Idempotency-Key")).toBe("11111111-1111-4111-8111-111111111111");
    expect(createHeaders.get("X-PgBase-CSRF")).toBe("csrf-token");
    expect((fetchMock.mock.calls[0][1] as RequestInit).body).toBe(`{"sequence":9007199254740993}`);
    expect(new Headers((fetchMock.mock.calls[1][1] as RequestInit).headers).get("If-Match")).toBe('"pgb1.created"');
    expect(new Headers((fetchMock.mock.calls[2][1] as RequestInit).headers).get("If-Match")).toBe('"pgb1.updated"');
  });

  it("preserves canonical single, null, and ordered multiple relation values across API boundaries", async () => {
    const first = "11111111-1111-4111-8111-111111111111";
    const second = "22222222-2222-4222-8222-222222222222";
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(response(200, `{"id":"one","owner":null,"tags":["${second}","${first}"]}`, { ETag: '"pgb1.read"' }))
      .mockResolvedValueOnce(response(200, `{"id":"one","owner":"${first}","tags":["${second}","${first}"]}`, { ETag: '"pgb1.updated"' }));
    vi.stubGlobal("fetch", fetchMock);
    const records = new RecordsAPI(new APIClient(), "csrf-token");

    const read = await records.get("posts", "one");
    expect(read.data).toMatchObject({ owner: null, tags: [second, first] });
    await records.update("posts", "one", { owner: first, tags: [second, first] }, read.etag);

    const update = fetchMock.mock.calls[1][1] as RequestInit;
    expect(update.body).toBe(`{"owner":"${first}","tags":["${second}","${first}"]}`);
    expect(new Headers(update.headers).get("If-Match")).toBe('"pgb1.read"');
  });

  it.each([undefined, "*", 'W/"pgb1.weak"', '"two", "tags"'])("fails closed for an absent or non-strong ETag: %s", async (etag) => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(response(200, `{"id":"one"}`, etag === undefined ? undefined : { ETag: etag })));

    await expect(new RecordsAPI(new APIClient(), "csrf").get("posts", "one")).rejects.toEqual(
      expect.objectContaining<Partial<APIClientError>>({ status: 502, code: "invalid_response" }),
    );
  });
});
