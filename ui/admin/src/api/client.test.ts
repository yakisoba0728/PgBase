import { describe, expect, it, vi } from "vitest";
import { APIClient, APIClientError } from "./client";
import { stringifyJSON } from "./json";

function textResponse(status: number, body: string): Response {
  return {
    ok: status >= 200 && status < 300,
    status,
    headers: new Headers({ "content-type": "application/json" }),
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
