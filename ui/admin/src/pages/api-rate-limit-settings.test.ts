import { describe, expect, it } from "vitest";
import { ruleErrors } from "./api-rate-limit-settings";

describe("native API rate labels", () => {
  it("matches the server's UTF-8 byte limit", () => {
    expect(ruleErrors([{ label: "/" + "가".repeat(85), audience: "", duration: 60, maxRequests: 5 }], [])).toBeNull();
    expect(ruleErrors([{ label: "/" + "가".repeat(86), audience: "", duration: 60, maxRequests: 5 }], [])).not.toBeNull();
  });
  it.each(["posts:list", "*:auth", " /api/", "/api/ ", "GET  /api/", "get /api/", "/api/?key=x", "/api/#fragment", "/api/\n", "/api/\u0000", "/api/\u00a0"])("rejects an ineffective label %j", (label) => {
    expect(ruleErrors([{ label, audience: "", duration: 60, maxRequests: 5 }], [])).not.toBeNull();
  });

  it.each(["/", "/health/live", "/api/", "GET /api/", "PATCH /api/v1/collections/{collection}/records/{id}"])("accepts the native route label %j", (label) => {
    expect(ruleErrors([{ label, audience: "@auth", duration: 60, maxRequests: 5 }], [])).toBeNull();
  });
});
