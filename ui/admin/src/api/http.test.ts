import { afterEach, describe, expect, it, vi } from "vitest";
import { LosslessNumber } from "lossless-json";
import { ApiError, clearCsrfToken, parseJson, request, setCsrfToken, stringifyJson } from "./http";

describe("parseJson / stringifyJson", () => {
  it("keeps safe numbers as numbers and unsafe ones exact", () => {
    const text = '{"small":42,"float":0.30000000000000004,"big":9007199254740993,"long":1.23456789012345678901}';
    const value = parseJson(text) as Record<string, unknown>;
    expect(value.small).toBe(42);
    expect(value.float).toBe(0.30000000000000004);
    expect(value.big).toBeInstanceOf(LosslessNumber);
    expect(String(value.big)).toBe("9007199254740993");
    expect(stringifyJson(value)).toBe(text);
  });
});

describe("request", () => {
  afterEach(() => {
    vi.unstubAllGlobals();
    clearCsrfToken();
  });

  it("maps the server error envelope to ApiError", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response(
      '{"error":{"code":"csrf_rejected","message":"CSRF verification failed","details":{"x":["y"]},"requestId":"r"}}',
      { status: 403 },
    )));
    const error = await request("POST", "/api/admin/settings/reload").catch((e: unknown) => e);
    expect(error).toBeInstanceOf(ApiError);
    expect(error).toMatchObject({ status: 403, code: "csrf_rejected", message: "CSRF verification failed", details: { x: ["y"] } });
  });

  it("maps a non-envelope body to invalid_response and a fetch failure to network_error", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => new Response("<html>bad gateway</html>", { status: 502 })));
    await expect(request("GET", "/x")).rejects.toMatchObject({ status: 502, code: "invalid_response" });
    vi.stubGlobal("fetch", vi.fn(async () => { throw new TypeError("Failed to fetch"); }));
    await expect(request("GET", "/x")).rejects.toMatchObject({ status: 0, code: "network_error" });
  });

  it("lets an abort propagate untouched", async () => {
    const controller = new AbortController();
    controller.abort();
    const abort = new DOMException("aborted", "AbortError");
    vi.stubGlobal("fetch", vi.fn(async () => { throw abort; }));
    await expect(request("GET", "/x", { signal: controller.signal })).rejects.toBe(abort);
  });

  it("sends CSRF on mutations only, encodes JSON and returns undefined for empty bodies", async () => {
    const fetchMock = vi.fn(async (_url: string, _init: RequestInit) => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetchMock);
    setCsrfToken("tok");
    await expect(request("PATCH", "/a", { body: { n: 1 }, query: { q: "v", skip: undefined } })).resolves.toBeUndefined();
    await request("GET", "/b");
    const [url, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init.headers);
    expect(url).toBe("/a?q=v");
    expect(headers.get("X-PgBase-CSRF")).toBe("tok");
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(init.body).toBe('{"n":1}');
    expect(init.credentials).toBe("include");
    expect(new Headers(fetchMock.mock.calls[1][1].headers).has("X-PgBase-CSRF")).toBe(false);
  });
});
