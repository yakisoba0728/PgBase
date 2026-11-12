import { describe, expect, it } from "vitest";
import { ApiError } from "../api/http";
import { decideGuard } from "./guard";

const idle = { data: undefined, error: null, isPending: true };
const unauthorized = { data: undefined, error: new ApiError(401, "unauthorized", "no session"), isPending: false };

describe("decideGuard", () => {
  it("lets a session through", () => {
    expect(decideGuard({ data: { id: "a" }, error: null, isPending: false }, idle)).toEqual({ kind: "ready" });
  });

  it("waits while the session loads", () => {
    expect(decideGuard(idle, idle)).toEqual({ kind: "loading" });
  });

  it("sends a missing session to /setup while setup is pending", () => {
    expect(decideGuard(unauthorized, { data: { setupRequired: true }, error: null, isPending: false })).toEqual({ kind: "redirect", to: "/setup" });
  });

  it("sends a missing session to /login once set up", () => {
    expect(decideGuard(unauthorized, { data: { setupRequired: false }, error: null, isPending: false })).toEqual({ kind: "redirect", to: "/login" });
  });

  it("falls back to /login when the status check fails", () => {
    expect(decideGuard(unauthorized, { data: undefined, error: new Error("down"), isPending: false })).toEqual({ kind: "redirect", to: "/login" });
  });

  it("waits for the status check before redirecting", () => {
    expect(decideGuard(unauthorized, idle)).toEqual({ kind: "loading" });
  });

  it("treats other failures as errors, not redirects", () => {
    const down = { data: undefined, error: new ApiError(0, "network_error", "offline"), isPending: false };
    expect(decideGuard(down, idle)).toEqual({ kind: "error" });
  });
});
