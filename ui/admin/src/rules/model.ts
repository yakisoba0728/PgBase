import type { AccessRule, CollectionRules, CollectionType, PocketBaseRules } from "../api/collections";

// The server stores typed rules; only the ?compat=pocketbase PUT parses expression text.
// Owner and other typed-only rules have no expression form, so a draft remembers the
// loaded rule and the save path is chosen from what changed.

const actions = ["list", "view", "create", "update", "delete"] as const;
export type Action = (typeof actions)[number];

export const modes = ["admins", "everyone", "signedIn", "owner", "expression"] as const;
type Mode = (typeof modes)[number] | "custom";

export interface RuleDraft {
  mode: Mode;
  expression: string;
  /** The rule as loaded; sent back unchanged while its row is untouched. */
  loaded: AccessRule;
}

export type RuleDrafts = Record<Action, RuleDraft>;

const signedInExpression = "@request.auth.id != ''";

/** The server's `invalid_rule` details: rules.<action> holds the reason and rules.<action>.position
 * the 1-based character where a syntax error was found. */
export function rejectedRules(details: unknown): Partial<Record<Action, { reason: string; position?: string }>> {
  const map = (details ?? {}) as Record<string, string[] | undefined>;
  return Object.fromEntries(
    actions.filter((action) => map[`rules.${action}`]?.length).map((action) => [action, { reason: map[`rules.${action}`]![0], position: map[`rules.${action}.position`]?.[0] }]),
  );
}

/** Rows shown for a collection type: views only read. */
export const visibleActions = (type: CollectionType): readonly Action[] => (type === "view" ? ["list", "view"] : actions);

/** Preview evaluates existing rows, so create has no preview. */
export const previewable = (action: Action): action is "list" | "view" | "update" | "delete" => action !== "create";

export function draftFromRule(rule: AccessRule): RuleDraft {
  const draft = (mode: Mode, expression = "") => ({ mode, expression, loaded: rule });
  if (rule.op === "deny") return draft("admins");
  if (rule.op === "allow") return draft("everyone");
  if (rule.op === "owner") return draft("owner");
  if (rule.op === "authenticated" && (rule.pocketbase === undefined || rule.pocketbase === signedInExpression)) return draft("signedIn");
  if (rule.pocketbase !== undefined) return draft("expression", rule.pocketbase);
  return draft("custom");
}

export const draftsFromRules = (rules: CollectionRules): RuleDrafts =>
  Object.fromEntries(actions.map((action) => [action, draftFromRule(rules[action])])) as RuleDrafts;

/** An expression row whose text is not what the server last parsed. */
export const expressionChanged = (draft: RuleDraft) => draft.mode === "expression" && draft.expression !== draft.loaded.pocketbase;

export const sameDraft = (a: RuleDraft, b: RuleDraft) => a.mode === b.mode && (a.mode !== "expression" || a.expression === b.expression);

/** The typed rule, or null when the row holds expression text the server has not parsed yet. */
export function typedRule(draft: RuleDraft): AccessRule | null {
  if (draft.mode !== "expression" && draftFromRule(draft.loaded).mode === draft.mode) return draft.loaded;
  switch (draft.mode) {
    case "admins":
      return { op: "deny" };
    case "everyone":
      return { op: "allow" };
    case "signedIn":
      return { op: "authenticated" };
    case "owner":
      return { op: "owner" };
    case "custom":
      return draft.loaded;
    case "expression":
      return expressionChanged(draft) ? null : draft.loaded;
  }
}

/** The compat string; undefined when the mode has no expression form (owner, custom). */
function compatRule(draft: RuleDraft): string | null | undefined {
  switch (draft.mode) {
    case "admins":
      return null;
    case "everyone":
      return "";
    case "signedIn":
      return signedInExpression;
    case "expression":
      return draft.expression;
    default:
      return undefined;
  }
}

type SavePlan =
  | { kind: "typed"; rules: CollectionRules }
  | { kind: "compat"; rules: PocketBaseRules }
  /** compat first (keep slots as admins only), then the kept typed rules are put back. */
  | { kind: "split"; rules: PocketBaseRules; keep: Partial<CollectionRules> };

/** Empty expression rows; an empty compat string would mean "everyone". */
export const emptyExpressions = (drafts: RuleDrafts, visible: readonly Action[]) =>
  visible.filter((action) => drafts[action].mode === "expression" && !drafts[action].expression.trim());

export function planSave(drafts: RuleDrafts, visible: readonly Action[]): SavePlan {
  const typed = Object.fromEntries(actions.map((action) => [action, typedRule(drafts[action])]));
  if (actions.every((action) => typed[action] !== null)) return { kind: "typed", rules: typed as unknown as CollectionRules };
  const compat = {} as Record<Action, string | null>;
  const keep: Partial<CollectionRules> = {};
  for (const action of actions) {
    const draft = drafts[action];
    // Hidden rows (writes on a view) are never used, so they fall back to admins only.
    const value = visible.includes(action) ? compatRule(draft) : null;
    compat[action] = value ?? null;
    if (value === undefined) keep[action] = typed[action]!;
  }
  const rules: PocketBaseRules = { listRule: compat.list, viewRule: compat.view, createRule: compat.create, updateRule: compat.update, deleteRule: compat.delete };
  return Object.keys(keep).length ? { kind: "split", rules, keep } : { kind: "compat", rules };
}

/** Insert text at a textarea selection; returns the new value and caret. */
export function insertAt(value: string, start: number, end: number, text: string): { value: string; caret: number } {
  const before = value.slice(0, start);
  const pad = before && !/\s$/.test(before) ? " " : "";
  return { value: `${before}${pad}${text}${value.slice(end)}`, caret: start + pad.length + text.length };
}
