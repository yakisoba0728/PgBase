import { QueryObserver } from "@tanstack/react-query";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "../api/http";
import type { AdminSession } from "../api/session";
import { operations, trackOperation } from "./operations";
import { endSignedOutSession, queryClient, sessionQuery, signingOutKey } from "./queries";
import { adoptSession, finishSignOut, followSignOut, recoverSession } from "./signout";

const getSession = vi.hoisted(() => vi.fn());
const logout = vi.hoisted(() => vi.fn());
vi.mock("../api/session", () => ({ getSession, logout }));

const session = (id: string, csrfToken = "t"): AdminSession => ({
  administrator: { id, email: `${id}@example.test`, active: true, createdAt: "", updatedAt: "" },
  csrfToken,
  expiresAt: "",
  sqlConsoleEnabled: false,
});

beforeEach(() => {
  queryClient.clear();
  localStorage.clear();
  getSession.mockReset();
  logout.mockReset();
});

describe("adoptSession", () => {
  it("drops cached data always and device content only for another administrator", () => {
    adoptSession(session("a"));
    trackOperation({ kind: "schema", id: "op" });
    queryClient.setQueryData(["profile"], { email: "a@example.test" });
    adoptSession(session("a"));
    expect(queryClient.getQueryData(["profile"])).toBeUndefined();
    expect(operations()).toHaveLength(1);
    adoptSession(session("b"));
    expect(operations()).toHaveLength(0);
    expect(queryClient.getQueryData(sessionQuery.queryKey)?.administrator.id).toBe("b");
  });
});

describe("recoverSession", () => {
  it("does nothing without a signed-in session", async () => {
    await expect(recoverSession()).resolves.toBeNull();
    expect(getSession).not.toHaveBeenCalled();
  });

  it("retries for the same administrator and keeps the cache", async () => {
    queryClient.setQueryData(sessionQuery.queryKey, session("a", "old"));
    queryClient.setQueryData(["profile"], { email: "a@example.test" });
    getSession.mockResolvedValue(session("a", "new"));
    await expect(recoverSession()).resolves.toBe("a");
    expect(queryClient.getQueryData(sessionQuery.queryKey)?.csrfToken).toBe("new");
    expect(queryClient.getQueryData(["profile"])).toBeDefined();
  });

  it("replaces another administrator's cached data instead of retrying", async () => {
    queryClient.setQueryData(sessionQuery.queryKey, session("a"));
    queryClient.setQueryData(["profile"], { email: "a@example.test" });
    getSession.mockResolvedValue(session("b"));
    await expect(recoverSession()).resolves.toBe("b");
    expect(queryClient.getQueryData(["profile"])).toBeUndefined();
    expect(queryClient.getQueryData(sessionQuery.queryKey)?.administrator.id).toBe("b");
  });

  it("ends the session when the server has none", async () => {
    queryClient.setQueryData(sessionQuery.queryKey, session("a"));
    getSession.mockRejectedValue(new ApiError(401, "unauthorized", "no session"));
    await expect(recoverSession()).resolves.toBeNull();
  });
});

describe("sign-out", () => {
  it("ends the session as soon as the navigation commits, with a request that outlives the page", async () => {
    let answer!: () => void;
    logout.mockReturnValue(new Promise<void>((resolve) => (answer = resolve)));
    queryClient.setQueryData(sessionQuery.queryKey, session("a"));
    followSignOut({ key: "stay", state: null });
    expect(logout).not.toHaveBeenCalled();
    followSignOut({ key: "out", state: { signedOut: true, end: true } });
    followSignOut({ key: "out", state: { signedOut: true, end: true } });
    expect(logout).toHaveBeenCalledTimes(1);
    expect(logout).toHaveBeenCalledWith(true);
    // Nothing cached is left for the guard to render from while the request is out.
    expect(queryClient.getQueryData(sessionQuery.queryKey)).toBeUndefined();
    expect(localStorage.getItem(signingOutKey)).toBe("1");
    answer();
    await vi.waitFor(() => expect(localStorage.getItem(signingOutKey)).toBeNull());
  });

  it("finishes a sign-out the page left before the server answered", async () => {
    localStorage.setItem(signingOutKey, "1");
    getSession.mockResolvedValue(session("a"));
    logout.mockResolvedValue(undefined);
    await endSignedOutSession();
    expect(logout).toHaveBeenCalledTimes(1);
    expect(localStorage.getItem(signingOutKey)).toBeNull();
    localStorage.setItem(signingOutKey, "1");
    getSession.mockRejectedValue(new ApiError(0, "network_error", "offline"));
    await expect(endSignedOutSession()).rejects.toMatchObject({ code: "network_error" });
    expect(localStorage.getItem(signingOutKey)).toBe("1");
  });
});

it.each(["logout", "login"])("ignores an old recovery after %s", async (action) => {
  adoptSession(session("a"));
  let resolve!: (value: AdminSession) => void;
  getSession.mockReturnValue(new Promise<AdminSession>((answer) => { resolve = answer; }));
  const recovery = recoverSession();
  if (action === "logout") finishSignOut(false); else adoptSession(session("b"));
  resolve(session("a", "stale"));
  await expect(recovery).resolves.toBeNull();
  expect(queryClient.getQueryData(sessionQuery.queryKey)?.administrator.id).toBe(action === "login" ? "b" : undefined);
});

it("clears the signed-out owner's other-tab cache but ignores another owner's signal", () => {
  adoptSession(session("a"));
  queryClient.setQueryData(["private"], "private content");
  window.dispatchEvent(new StorageEvent("storage", { key: "pgbase.signedOut", newValue: "b:1" }));
  expect(queryClient.getQueryData(["private"])).toBe("private content");
  window.dispatchEvent(new StorageEvent("storage", { key: "pgbase.signedOut", newValue: "a:1" }));
  expect(queryClient.getQueryData(["private"])).toBeUndefined();
  expect(queryClient.getQueryData(sessionQuery.queryKey)).toBeUndefined();
});

it("notifies an active session observer when another tab signs out", () => {
  adoptSession(session("a"));
  const observer = new QueryObserver(queryClient, sessionQuery);
  const unsubscribe = observer.subscribe(() => {});
  expect(observer.getCurrentResult().data?.administrator.id).toBe("a");
  window.dispatchEvent(new StorageEvent("storage", { key: "pgbase.signedOut", newValue: "a:2" }));
  expect(observer.getCurrentResult().data).toBeUndefined();
  expect(observer.getCurrentResult().error).toMatchObject({ status: 401 });
  unsubscribe(); observer.destroy();
});
