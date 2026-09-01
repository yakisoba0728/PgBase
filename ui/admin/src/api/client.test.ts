import { describe, expect, it, vi } from "vitest";
import { APIClient, APIClientError } from "./client";
import { stringifyJSON } from "./json";

function textResponse(status: number, body: string, headers?: HeadersInit): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ "content-type": "application/json", ...headers }),
    text: vi.fn().mockResolvedValue(body),
  } as unknown as Response;
}

describe("APIClient JSON and session boundaries", () => {
  it("round-trips JSON numbers that are unsafe for JavaScript Number", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(textResponse(200, `{"metadata":{"sequence":9007199254740993,"ratio":1.234567890123456789}}`)));
    const value = await new APIClient().request<unknown>("/api/value");
    expect(stringifyJSON(value)).toBe(`{"metadata":{"sequence":9007199254740993,"ratio":1.234567890123456789}}`);
    vi.unstubAllGlobals();
  });

  it("retains response status and headers without changing the existing data-only API", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(textResponse(200, `{"id":"one"}`, { ETag: '"pgb1.one"' }))
      .mockResolvedValueOnce(textResponse(200, `{"id":"two"}`, { ETag: '"pgb1.two"' }));
    vi.stubGlobal("fetch", fetchMock);
    const client = new APIClient();

    const metadata = await client.requestWithResponse<{ id: string }>("/api/one");
    expect(metadata.data).toEqual({ id: "one" });
    expect(metadata.status).toBe(200);
    expect(metadata.headers.get("ETag")).toBe('"pgb1.one"');
    await expect(client.request<{ id: string }>("/api/two")).resolves.toEqual({ id: "two" });
    vi.unstubAllGlobals();
  });

  it("retains error response headers for failed preconditions", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(textResponse(412, `{"error":{"code":"precondition_failed","message":"stale","details":{}}}`, { ETag: '"pgb1.current"' })));

    await expect(new APIClient().request("/api/record")).rejects.toEqual(
      expect.objectContaining<Partial<APIClientError>>({ status: 412, code: "precondition_failed" }),
    );
    try {
      await new APIClient().request("/api/record");
    } catch (caught) {
      expect(caught).toBeInstanceOf(APIClientError);
      expect((caught as APIClientError).headers.get("ETag")).toBe('"pgb1.current"');
    }
    vi.unstubAllGlobals();
  });

  it("rejects a 2xx whose body cannot be parsed instead of resolving without data", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(textResponse(200, "<html><body>502 Bad Gateway</body></html>")));
    await expect(new APIClient().request("/api/v1/collections/posts/records")).rejects.toEqual(
      expect.objectContaining<Partial<APIClientError>>({ status: 200, code: "invalid_response_body" }),
    );
    vi.unstubAllGlobals();
  });

  it("still resolves an empty successful body as undefined", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(textResponse(200, "")));
    await expect(new APIClient().request("/api/admin/auth/logout")).resolves.toBeUndefined();
    vi.unstubAllGlobals();
  });

  it("reports the transport status when a failure body is unreadable", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(textResponse(412, "<html><body>proxy</body></html>")));
    await expect(new APIClient().request("/api/record")).rejects.toEqual(
      expect.objectContaining<Partial<APIClientError>>({ status: 412, code: "request_failed" }),
    );
    vi.unstubAllGlobals();
  });

  it("reads every response through text() so the shipped parser is never bypassed", async () => {
    const json = vi.fn().mockResolvedValue({ id: "one" });
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
      ok: true,
      status: 200,
      headers: new Headers({ "content-type": "application/json" }),
      json,
    } as unknown as Response));

    await expect(new APIClient().request("/api/value")).rejects.toEqual(
      expect.objectContaining<Partial<APIClientError>>({ status: 200, code: "invalid_response_body" }),
    );
    expect(json).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });

  it("notifies the session owner for every protected-request 401", async () => {
    const unauthorized = vi.fn();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(textResponse(401, `{"error":{"code":"authentication_required","message":"Authentication required","details":{}}}`)));
    await expect(new APIClient(unauthorized).request("/api/admin/collections")).rejects.toEqual(expect.objectContaining<Partial<APIClientError>>({ status: 401 }));
    expect(unauthorized).toHaveBeenCalledOnce();
    vi.unstubAllGlobals();
  });

  it("does not notify the session owner for public authentication-flow 401 responses", async () => {
    const unauthorized = vi.fn();
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(textResponse(401, `{"error":{"code":"invalid_credentials","message":"Invalid credentials","details":{}}}`)));
    const client = new APIClient(unauthorized);

    await expect(client.getAdminStatus()).rejects.toEqual(expect.objectContaining<Partial<APIClientError>>({ status: 401 }));
    await expect(client.getSession()).rejects.toEqual(expect.objectContaining<Partial<APIClientError>>({ status: 401 }));
    await expect(client.setup({ setupToken: "bad-token", email: "admin@example.com", password: "password" })).rejects.toEqual(expect.objectContaining<Partial<APIClientError>>({ status: 401 }));
    await expect(client.login({ email: "admin@example.com", password: "password" })).rejects.toEqual(expect.objectContaining<Partial<APIClientError>>({ status: 401 }));

    expect(unauthorized).not.toHaveBeenCalled();
    vi.unstubAllGlobals();
  });
});
