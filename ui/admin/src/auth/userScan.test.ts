import { describe, expect, it } from "vitest";
import type { ManagedUser } from "../api/users";
import { isUserId, userMatcher } from "./userScan";

const user = (email: string, active: boolean, verified: boolean): ManagedUser => ({ id: "0190a1b2-0000-4000-8000-00000000abcd", email, active, verified, createdAt: "", updatedAt: "" });

describe("user search", () => {
  it("matches email or id case-insensitively, within the status filter", () => {
    const ann = user("Ann@Example.test", true, false);
    expect(userMatcher("ann@ex", "all")(ann)).toBe(true);
    expect(userMatcher("abcd", "all")(ann)).toBe(true);
    expect(userMatcher("bob", "all")(ann)).toBe(false);
    expect(userMatcher("", "unverified")(ann)).toBe(true);
    expect(userMatcher("", "disabled")(ann)).toBe(false);
    expect(userMatcher("ann", "active")(ann)).toBe(true);
  });

  it("treats only a complete UUID as an id to open", () => {
    expect(isUserId(" 0190A1B2-0000-4000-8000-00000000abcd ")).toBe(true);
    expect(isUserId("0190a1b2-0000")).toBe(false);
  });
});
