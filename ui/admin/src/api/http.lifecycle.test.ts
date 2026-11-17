import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { advanceSession, clearCsrfToken, onCsrfRejected, request, requestBlob, setCsrfToken, upload } from "./http";

function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((done) => { resolve = done; });
  return { promise, resolve };
}
const csrfRejected = () => new Response('{"error":{"code":"csrf_rejected"}}', { status: 403 });
let administrator: string | null = "a";
beforeEach(() => {
  administrator = "a";
  onCsrfRejected(async () => administrator, () => administrator);
});
afterEach(() => {
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  clearCsrfToken();
  onCsrfRejected(async () => null, () => null);
});

describe("request session lifetime", () => {
  it("discards a successful response from an earlier session", async () => {
    const answer = deferred<Response>();
    vi.stubGlobal("fetch", vi.fn(() => answer.promise));
    const pending = request("GET", "/records");
    advanceSession();
    answer.resolve(new Response('{"secret":"old-session"}'));
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
  });

  it("does not let an old 401 invalidate a newer login", async () => {
    const answer = deferred<Response>();
    vi.stubGlobal("fetch", vi.fn(() => answer.promise));
    const pending = request("GET", "/records");
    advanceSession();
    answer.resolve(new Response('{"error":{"code":"unauthorized"}}', { status: 401 }));
    await expect(pending).rejects.toMatchObject({ status: 403, code: "session_changed" });
  });

  it("never renews or replays an old mutation after a same-administrator login", async () => {
    const answer = deferred<Response>();
    const fetchMock = vi.fn(() => answer.promise);
    const renew = vi.fn(async () => "a");
    vi.stubGlobal("fetch", fetchMock);
    onCsrfRejected(renew, () => administrator);
    const pending = request("POST", "/write");
    advanceSession();
    answer.resolve(csrfRejected());
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
    expect(renew).not.toHaveBeenCalled();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("discards a response when the administrator changes without a local login", async () => {
    const answer = deferred<Response>();
    vi.stubGlobal("fetch", vi.fn(() => answer.promise));
    const pending = request("GET", "/records");
    administrator = "b";
    answer.resolve(new Response("{}"));
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
  });

  it("allows an anonymous bootstrap request to discover its initial identity", async () => {
    administrator = null;
    const answer = deferred<Response>();
    vi.stubGlobal("fetch", vi.fn(() => answer.promise));
    const pending = request("GET", "/status");
    administrator = "a";
    answer.resolve(new Response('{"ok":true}'));
    await expect(pending).resolves.toEqual({ ok: true });
  });

  it("covers a delayed JSON body after response headers have arrived", async () => {
    const body = deferred<string>();
    const entered = deferred<void>();
    const response = new Response();
    vi.spyOn(response, "text").mockImplementation(() => { entered.resolve(); return body.promise; });
    vi.stubGlobal("fetch", vi.fn(async () => response));
    const pending = request("GET", "/records");
    await entered.promise;
    advanceSession();
    body.resolve("{}");
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
  });

  it("covers a delayed download body", async () => {
    const body = deferred<Blob>();
    const entered = deferred<void>();
    const response = new Response();
    vi.spyOn(response, "blob").mockImplementation(() => { entered.resolve(); return body.promise; });
    vi.stubGlobal("fetch", vi.fn(async () => response));
    const pending = requestBlob("/download");
    await entered.promise;
    advanceSession();
    body.resolve(new Blob(["old"]));
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
  });

  it("does not share a pending recovery across session generations", async () => {
    const oldRecovery = deferred<string | null>();
    const entered = deferred<void>();
    const renew = vi.fn().mockImplementationOnce(() => { entered.resolve(); return oldRecovery.promise; }).mockResolvedValueOnce("a");
    onCsrfRejected(renew, () => administrator);
    const fetchMock = vi.fn().mockResolvedValueOnce(csrfRejected()).mockResolvedValueOnce(csrfRejected()).mockResolvedValueOnce(new Response('{"ok":true}'));
    vi.stubGlobal("fetch", fetchMock);
    const old = request("POST", "/old").catch((error: unknown) => error);
    await entered.promise;
    advanceSession();
    const fresh = request("POST", "/new");
    // Release the old flight even on the baseline, so a failing test cannot hang.
    await vi.waitFor(() => expect(fetchMock.mock.calls.length).toBeGreaterThanOrEqual(2));
    oldRecovery.resolve("a");
    await expect(fresh).resolves.toEqual({ ok: true });
    expect(renew).toHaveBeenCalledTimes(2);
    await expect(old).resolves.toMatchObject({ code: "session_changed" });
    expect(fetchMock).toHaveBeenCalledTimes(3);
  });

  it("does not publish a retry that completes after a session change", async () => {
    const answer = deferred<Response>();
    const retryStarted = deferred<void>();
    const fetchMock = vi.fn().mockResolvedValueOnce(csrfRejected()).mockImplementationOnce(() => { retryStarted.resolve(); return answer.promise; });
    vi.stubGlobal("fetch", fetchMock);
    const pending = request("POST", "/write");
    await retryStarted.promise;
    advanceSession();
    answer.resolve(new Response("{}"));
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });
});

class FakeXhr {
  static last: FakeXhr;
  status = 200;
  responseText = "{}";
  headers: [string, string][] = [];
  upload: { onprogress?: (event: { lengthComputable: boolean; loaded: number; total: number }) => void } = {};
  onload: (() => void) | null = null;
  onerror: (() => void) | null = null;
  ontimeout: (() => void) | null = null;
  onabort: (() => void) | null = null;
  withCredentials = false;
  constructor() { FakeXhr.last = this; }
  open() {}
  setRequestHeader(key: string, value: string) { this.headers.push([key, value]); }
  send() {}
  abort() { this.onabort?.(); }
}

describe("upload lifetime", () => {
  beforeEach(() => vi.stubGlobal("XMLHttpRequest", FakeXhr));

  it("rejects a successful upload response from an earlier session", async () => {
    const pending = upload("/files", new Blob(["data"]));
    advanceSession();
    FakeXhr.last.onload?.();
    await expect(pending).rejects.toMatchObject({ code: "session_changed" });
  });

  it("removes the abort listener after success and does not abort completed work", async () => {
    const controller = new AbortController();
    const remove = vi.spyOn(controller.signal, "removeEventListener");
    const pending = upload("/files", new Blob(), { signal: controller.signal });
    const xhr = FakeXhr.last;
    const abort = vi.spyOn(xhr, "abort");
    xhr.onload?.();
    await pending;
    expect(remove).toHaveBeenCalledWith("abort", expect.any(Function));
    controller.abort();
    expect(abort).not.toHaveBeenCalled();
  });

  it.each(["onerror", "ontimeout"] as const)("settles and removes the listener on %s", async (event) => {
    const controller = new AbortController();
    const remove = vi.spyOn(controller.signal, "removeEventListener");
    const pending = upload("/files", new Blob(), { signal: controller.signal });
    FakeXhr.last[event]?.();
    // The baseline has no timeout handler: explicitly finish it to avoid an unbounded test.
    if (!FakeXhr.last[event]) FakeXhr.last.onerror?.();
    await expect(pending).rejects.toMatchObject({ code: "network_error" });
    expect(FakeXhr.last[event]).toBeTypeOf("function");
    expect(remove).toHaveBeenCalledWith("abort", expect.any(Function));
  });

  it("cleans up when send throws synchronously", async () => {
    const controller = new AbortController();
    const remove = vi.spyOn(controller.signal, "removeEventListener");
    const failure = new Error("send failed");
    vi.spyOn(FakeXhr.prototype, "send").mockImplementation(() => { throw failure; });
    await expect(upload("/files", new Blob(), { signal: controller.signal })).rejects.toBe(failure);
    expect(remove).toHaveBeenCalledWith("abort", expect.any(Function));
  });

  it("preserves the caller's abort reason and cleans up", async () => {
    const controller = new AbortController();
    const remove = vi.spyOn(controller.signal, "removeEventListener");
    const pending = upload("/files", new Blob(), { signal: controller.signal });
    const reason = new Error("navigation cancelled upload");
    controller.abort(reason);
    await expect(pending).rejects.toBe(reason);
    expect(remove).toHaveBeenCalledWith("abort", expect.any(Function));
  });

  it("never reports NaN or progress outside zero to one", async () => {
    const progress = vi.fn();
    const pending = upload("/files", new Blob(), { onProgress: progress });
    FakeXhr.last.upload.onprogress?.({ lengthComputable: true, loaded: 0, total: 0 });
    expect(progress).not.toHaveBeenCalled();
    FakeXhr.last.upload.onprogress?.({ lengthComputable: true, loaded: 6, total: 4 });
    expect(progress).toHaveBeenLastCalledWith(1);
    FakeXhr.last.onload?.();
    await pending;
  });

  it("does not append a second CSRF value to an explicit header", async () => {
    setCsrfToken("implicit");
    const pending = upload("/files", new Blob(), { headers: { "x-pgbase-csrf": "explicit" } });
    expect(FakeXhr.last.headers.filter(([key]) => key.toLowerCase() === "x-pgbase-csrf")).toEqual([["x-pgbase-csrf", "explicit"]]);
    FakeXhr.last.onload?.();
    await pending;
  });
});
