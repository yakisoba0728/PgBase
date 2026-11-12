import { describe, expect, it } from "vitest";
import type { CollectionRules } from "../api/collections";
import { draftFromRule, draftsFromRules, emptyExpressions, insertAt, planSave, visibleActions, type RuleDrafts } from "./model";

const owner = { op: "owner" } as const;
const parsed = { op: "eq", field: "published", value: true, pocketbase: "published = true" } as const;
const loaded: CollectionRules = { list: parsed, view: { op: "allow" }, create: { op: "authenticated", pocketbase: "@request.auth.id != ''" }, update: { op: "deny" }, delete: owner };

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
    expect(plan).toEqual({ kind: "typed", rules: { ...loaded, update: { op: "allow" } } });
  });

  it("saves through compat when an expression changed and every row has an expression form", () => {
    const drafts = draftsFromRules({ ...loaded, delete: { op: "deny" } });
    const plan = planSave(edit(drafts, { list: { ...drafts.list, expression: "count > 1" } }), visibleActions("base"));
    expect(plan).toEqual({ kind: "compat", rules: { listRule: "count > 1", viewRule: "", createRule: "@request.auth.id != ''", updateRule: null, deleteRule: null } });
  });

  it("splits when owner rows sit next to a changed expression", () => {
    const drafts = draftsFromRules(loaded);
    const plan = planSave(edit(drafts, { list: { ...drafts.list, expression: "count > 1" } }), visibleActions("base"));
    expect(plan).toMatchObject({ kind: "split", rules: { listRule: "count > 1", deleteRule: null }, keep: { delete: owner } });
  });

  it("closes hidden write rows of a view instead of splitting", () => {
    const drafts = draftsFromRules({ list: owner, view: owner, create: owner, update: owner, delete: owner });
    const plan = planSave(edit(drafts, { list: { ...drafts.list, mode: "expression", expression: "count > 1" } }), visibleActions("view"));
    expect(plan).toEqual({ kind: "split", rules: { listRule: "count > 1", viewRule: null, createRule: null, updateRule: null, deleteRule: null }, keep: { view: owner } });
  });

  it("flags empty expressions, which compat would read as everyone", () => {
    const drafts = draftsFromRules(loaded);
    expect(emptyExpressions(edit(drafts, { view: { ...drafts.view, mode: "expression", expression: " " } }), visibleActions("base"))).toEqual(["view"]);
  });

  it("inserts tokens with a separating space", () => {
    expect(insertAt("title =", 7, 7, "@now")).toEqual({ value: "title = @now", caret: 12 });
    expect(insertAt("", 0, 0, "title")).toEqual({ value: "title", caret: 5 });
  });
});
