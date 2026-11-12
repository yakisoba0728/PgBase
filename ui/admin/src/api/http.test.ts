import { afterEach, describe, expect, it, vi } from "vitest";
import { LosslessNumber } from "lossless-json";
import { ApiError, clearCsrfToken, onCsrfRejected, parseJson, request, setCsrfToken, stringifyJson, uploadForm } from "./http";

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

describe("CSRF recovery", () => {
  const rejected = () => new Response('{"error":{"code":"csrf_rejected","message":"CSRF verification failed"}}', { status: 403 });
  let signedIn: string | null = "a";
  const issuer = () => signedIn;
  afterEach(() => {
    vi.unstubAllGlobals();
    clearCsrfToken();
    signedIn = "a";
    onCsrfRejected(async () => null, () => null);
  });

  it("renews once for concurrent rejections and resends each request with the new token", async () => {
    setCsrfToken("old");
    const fetchMock = vi.fn(async (_url: string, init: RequestInit) => (new Headers(init.headers).get("X-PgBase-CSRF") === "new" ? new Response('{"ok":true}') : rejected()));
    vi.stubGlobal("fetch", fetchMock);
    const renew = vi.fn(async () => {
      setCsrfToken("new");
      return "a";
    });
    onCsrfRejected(renew, issuer);
    await expect(Promise.all([request("POST", "/a"), request("DELETE", "/b")])).resolves.toEqual([{ ok: true }, { ok: true }]);
    expect(renew).toHaveBeenCalledTimes(1);
    expect(fetchMock).toHaveBeenCalledTimes(4);
  });

  it("does not resend for another administrator and retries at most once", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => rejected()));
    onCsrfRejected(async () => "b", issuer);
    await expect(request("POST", "/a")).rejects.toMatchObject({ status: 403, code: "session_changed" });
    const fetchMock = vi.fn(async () => rejected());
    vi.stubGlobal("fetch", fetchMock);
    onCsrfRejected(async () => "a", issuer);
    await expect(request("POST", "/a")).rejects.toMatchObject({ code: "csrf_rejected" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it("never resends a request issued under A once B is signed in, even when the cache already shows B", async () => {
    // A's write is in flight; another tab signs B in and this tab follows; then A's rejection arrives.
    let answer!: () => void;
    const fetchMock = vi.fn(() => new Promise<Response>((resolve) => (answer = () => resolve(rejected()))));
    vi.stubGlobal("fetch", fetchMock);
    onCsrfRejected(async () => signedIn, issuer);
    const pending = request("PUT", "/api/admin/profile/ip-allowlist", { body: { prefixes: [] } });
    signedIn = "b";
    answer();
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("applies the same rule to form uploads", async () => {
    const sent: { onload: (() => void) | null }[] = [];
    class FakeXhr {
      status = 403;
      responseText = '{"error":{"code":"csrf_rejected","message":"no"}}';
      upload = {};
      onload: (() => void) | null = null;
      withCredentials = false;
      open() {}
      setRequestHeader() {}
      send() {
        sent.push(this);
      }
    }
    vi.stubGlobal("XMLHttpRequest", FakeXhr);
    onCsrfRejected(async () => signedIn, issuer);
    const pending = uploadForm("/api/files", new FormData());
    signedIn = "b";
    sent[0].onload?.();
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
    expect(sent).toHaveLength(1);
  });
});
