import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient } from "@tanstack/react-query";
import type { AuthCollection, AuthRecord } from "../api/authCollections";
import { ApiError } from "../api/http";
import type { ManagedUser } from "../api/users";
import { userQuery, userRecordQuery, usersQuery } from "./queries";
import { isStale, rejectedFields } from "./useDraft";

const listUsers = vi.hoisted(() => vi.fn());
const getUser = vi.hoisted(() => vi.fn());
const listAuthRecords = vi.hoisted(() => vi.fn());
vi.mock("../api/users", () => ({ listUsers, getUser, listUserOAuthIdentities: vi.fn() }));
vi.mock("../api/authCollections", () => ({ listAuthRecords, getAuthAdministration: vi.fn(), listAuthCollections: vi.fn(), listOAuthProviderCatalog: vi.fn(), listTokenPolicies: vi.fn() }));

const user: ManagedUser = { id: "0190a1b2-0000-7000-8000-000000000010", authCollectionId: "c2", email: "ann@example.test", verified: true, active: true, createdAt: "2026-01-01T00:00:00Z", updatedAt: "" };
const collection = (id: string) => ({ id, name: id }) as AuthCollection;
const record: AuthRecord = { id: "ann-text-id", authCollectionId: "c2", email: user.email, emailVisibility: false, verified: true, active: true, fields: {}, createdAt: user.createdAt, updatedAt: "" };

beforeEach(() => {
  listUsers.mockReset();
  getUser.mockReset();
  listAuthRecords.mockReset();
});

describe("user queries", () => {
  it("search and the collection filter go to the server; only the first page asks for the total", async () => {
    listUsers.mockResolvedValue({ items: [], nextAfter: "x" });
    const client = new QueryClient();
    await client.fetchInfiniteQuery(usersQuery({ q: "ann@example.test", collection: "c2" }));
    expect(listUsers).toHaveBeenLastCalledWith(expect.objectContaining({ after: undefined, q: "ann@example.test", authCollectionId: "c2", total: true }));
    await client.fetchInfiniteQuery({ ...usersQuery({ q: "", collection: "" }), pages: 2 });
    expect(listUsers).toHaveBeenLastCalledWith(expect.objectContaining({ after: "x", q: undefined, authCollectionId: undefined, total: false }));
  });

  it("reads one user directly and maps a missing one to null", async () => {
    getUser.mockResolvedValueOnce(user).mockRejectedValueOnce(new ApiError(404, "not_found", "Not found"));
    const client = new QueryClient();
    expect(await client.fetchQuery(userQuery(user.id))).toEqual(user);
    expect(await client.fetchQuery(userQuery("0190a1b2-0000-7000-8000-000000000011"))).toBeNull();
  });

  it("finds the auth record in the user's own collection with one keyed page", async () => {
    listAuthRecords.mockResolvedValue({ items: [record] });
    const found = await new QueryClient().fetchQuery(userRecordQuery(user, [collection("c1"), collection("c2")]));
    expect(found).toEqual({ collection: collection("c2"), record });
    expect(listAuthRecords).toHaveBeenCalledTimes(1);
    expect(listAuthRecords).toHaveBeenCalledWith("c2", expect.objectContaining({ after: "0190a1b2-0000-7000-8000-00000000000f", limit: 1 }));
  });
});

describe("save failures", () => {
  it("treats 412 as a stale save and names the fields a rejection reports", () => {
    expect(isStale(new ApiError(412, "precondition_failed", "changed"))).toBe(true);
    expect(isStale(new ApiError(409, "conflict", "conflict"))).toBe(false);
    expect(rejectedFields(new ApiError(400, "invalid_request", "bad", { ttlSeconds: ["invalid"] }))).toEqual(["ttlSeconds"]);
    expect(rejectedFields(new ApiError(400, "invalid_request", "bad", {}))).toEqual([]);
  });
});
