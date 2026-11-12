import { describe, expect, it } from "vitest";
import type { RuntimeSettings } from "../api/system";
import { isAddrOrPrefix, isMaskedPrefix } from "./net";
import { isDirty, toDraft, toPatch, validate } from "./settingsForm";

const settings: RuntimeSettings = {
  batch: { enabled: true, maxRequests: 50, maxBodyBytes: 155193344, timeoutMs: 30000 },
  logs: { retentionDays: 7, level: "info", includeIP: false, includeAuth: false },
  proxy: { trustedProxyCIDRs: null },
  backups: { schedule: "@every 24h", timezone: "UTC", enabled: false, keep: 0 },
  application: { theme: "system", accent: "", branding: "" },
  rateLimits: {
    admin: { accountMaxFailures: 5, networkMaxFailures: 20, windowSeconds: 900, blockSeconds: 900 },
    user: { accountMaxFailures: 5, networkMaxFailures: 20, registrationNetworkMaxFailures: 5, windowSeconds: 900, blockSeconds: 900 },
    enabled: false,
    rules: null,
    excludedIPs: null,
  },
  revision: 3,
};

describe("prefix checks", () => {
  it("accepts masked, non-zero CIDRs only", () => {
    expect(isMaskedPrefix("10.0.0.0/8")).toBe(true);
    expect(isMaskedPrefix("127.0.0.1/32")).toBe(true);
    expect(isMaskedPrefix("fd00::/8")).toBe(true);
    expect(isMaskedPrefix("::ffff:10.0.0.0/104")).toBe(true);
    expect(isMaskedPrefix("10.0.0.1/8")).toBe(false);
    expect(isMaskedPrefix("0.0.0.0/0")).toBe(false);
    expect(isMaskedPrefix("127.0.0.1")).toBe(false);
    expect(isMaskedPrefix("10.0.0.0/33")).toBe(false);
    expect(isMaskedPrefix("010.0.0.0/8")).toBe(false);
    expect(isMaskedPrefix("1::2::/64")).toBe(false);
  });

  it("allows bare addresses for exclusions", () => {
    expect(isAddrOrPrefix("192.168.1.10")).toBe(true);
    expect(isAddrOrPrefix("::1")).toBe(true);
    expect(isAddrOrPrefix("10.0.0.1/8")).toBe(true);
    expect(isAddrOrPrefix("example.com")).toBe(false);
    expect(isAddrOrPrefix("1:2:3:4:5:6:7:8:9")).toBe(false);
  });
});

describe("settings draft", () => {
  it("round-trips null lists without reporting a change", () => {
    const draft = toDraft(settings);
    expect(draft.values["proxy.trustedProxyCIDRs"]).toBe("");
    expect(isDirty(draft, settings)).toBe(false);
    draft.values["proxy.trustedProxyCIDRs"] = "  \n10.0.0.0/8\n";
    expect(isDirty(draft, settings)).toBe(true);
    const patch = toPatch(draft, settings.revision);
    expect(patch.proxy.trustedProxyCIDRs).toEqual(["10.0.0.0/8"]);
    expect(patch.revision).toBe(3);
    expect(patch.rateLimits.admin.windowSeconds).toBe(900);
  });

  it("maps invalid values to their fields", () => {
    const draft = toDraft(settings);
    draft.values["batch.maxRequests"] = "101";
    draft.values["logs.retentionDays"] = "1.5";
    draft.values["backups.schedule"] = " ";
    draft.values["backups.keep"] = "";
    draft.values["rateLimits.excludedIPs"] = "10.0.0.1\nnope";
    draft.rules = [
      { label: "GET /api/v1/x", audience: "", duration: "60", maxRequests: "10" },
      { label: "GET /api/v1/x", audience: "", duration: "0", maxRequests: "10" },
      { label: "get /x", audience: "@auth", duration: "60", maxRequests: "100001" },
    ];
    const errors = validate(draft);
    expect(errors["batch.maxRequests"]).toEqual({ key: "system.invalid.range", vars: { min: 1, max: 100 } });
    expect(errors["logs.retentionDays"]?.key).toBe("system.invalid.int");
    expect(errors["backups.schedule"]?.key).toBe("system.invalid.required");
    expect(errors["backups.keep"]?.key).toBe("system.invalid.int");
    expect(errors["rateLimits.excludedIPs"]).toEqual({ key: "system.invalid.ip", vars: { value: "nope" } });
    expect(errors["rules.0.label"]).toBeUndefined();
    expect(errors["rules.1.label"]?.key).toBe("system.invalid.duplicateRule");
    expect(errors["rules.1.duration"]?.key).toBe("system.invalid.range");
    expect(errors["rules.2.label"]?.key).toBe("system.invalid.rateLabel");
    expect(errors["rules.2.maxRequests"]?.key).toBe("system.invalid.range");
    expect(Object.keys(validate(toDraft(settings)))).toEqual([]);
  });
});
