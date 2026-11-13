import { describe, expect, it } from "vitest";
import type { CollectionRules } from "../api/collections";
import { draftFromRule, draftsFromRules, emptyExpressions, insertAt, planSave, rejectedRules, visibleActions, type RuleDrafts } from "./model";

const owner = { op: "owner" } as const;
const parsed = { op: "eq", field: "published", value: true, expression: "published = true" } as const;
const loaded: CollectionRules = { list: parsed, view: { op: "allow" }, create: { op: "authenticated", expression: "@request.auth.id != ''" }, update: { op: "deny" }, delete: owner };

const edit = (drafts: RuleDrafts, patch: Partial<RuleDrafts>): RuleDrafts => ({ ...drafts, ...patch });

describe("rule drafts", () => {
  it("classifies loaded rules without losing typed-only ones", () => {
    const drafts = draftsFromRules(loaded);
    expect([drafts.list.mode, drafts.view.mode, drafts.create.mode, drafts.update.mode, drafts.delete.mode]).toEqual(["expression", "everyone", "signedIn", "admins", "owner"]);
    expect(drafts.list.expression).toBe("published = true");
    expect(draftFromRule({ op: "unauthenticated" }).mode).toBe("custom");
  });

  it("saves typed when no expression text changed, keeping parsed expressions verbatim", () => {
    const drafts = draftsFromRules(loaded);
    const plan = planSave(edit(drafts, { update: { ...drafts.update, mode: "everyone" } }), visibleActions("base"));
    expect(plan).toEqual({ ...loaded, update: { op: "allow" } });
  });

  it("saves expressions through the native policy API", () => {
    const drafts = draftsFromRules({ ...loaded, delete: { op: "deny" } });
    const plan = planSave(edit(drafts, { list: { ...drafts.list, expression: "count > 1" } }), visibleActions("base"));
    expect(plan).toEqual({ ...loaded, list: { op: "expression", expression: "count > 1" }, delete: { op: "deny" } });
  });

  it("keeps owner rules alongside expressions in one policy", () => {
    const drafts = draftsFromRules(loaded);
    const plan = planSave(edit(drafts, { list: { ...drafts.list, expression: "count > 1" } }), visibleActions("base"));
    expect(plan).toEqual({ ...loaded, list: { op: "expression", expression: "count > 1" } });
  });

  it("preserves hidden view actions", () => {
    const drafts = draftsFromRules({ list: owner, view: owner, create: owner, update: owner, delete: owner });
    const plan = planSave(edit(drafts, { list: { ...drafts.list, mode: "expression", expression: "count > 1" } }), visibleActions("view"));
    expect(plan).toEqual({ list: { op: "expression", expression: "count > 1" }, view: owner, create: owner, update: owner, delete: owner });
  });

  it("flags empty expressions", () => {
    const drafts = draftsFromRules(loaded);
    expect(emptyExpressions(edit(drafts, { view: { ...drafts.view, mode: "expression", expression: " " } }), visibleActions("base"))).toEqual(["view"]);
  });

  it("inserts tokens with a separating space", () => {
    expect(insertAt("title =", 7, 7, "@now")).toEqual({ value: "title = @now", caret: 12 });
    expect(insertAt("", 0, 0, "title")).toEqual({ value: "title", caret: 5 });
  });

  it("reads which rule the server rejected and where", () => {
    expect(rejectedRules({ "rules.update": ["invalid_syntax"], "rules.update.position": ["15"], "rules.list": ["invalid_rule"] })).toEqual({
      list: { reason: "invalid_rule", position: undefined },
      update: { reason: "invalid_syntax", position: "15" },
    });
    expect(rejectedRules(undefined)).toEqual({});
  });
});
