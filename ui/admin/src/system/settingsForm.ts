import type { RuntimeSettings } from "../api/system";
import type { MessageKey } from "../i18n";
import { isAddrOrPrefix, isMaskedPrefix, lines } from "./net";

// Mirrors internal/runtimesettings validateStored + validAPIRateLabel; the server
// answers only `invalid_settings`, so this is what points at the offending field.

/** 148 MiB + 4 KiB, runtimesettings.MaximumBatchBodyBytes. */
const maxBatchBody = 148 * 1024 * 1024 + 4 * 1024;
const failures = { kind: "int", min: 1, max: 10000 } as const;
const windowRange = { kind: "int", min: 1, max: 86400 } as const;
const block = { kind: "int", min: 1, max: 604800 } as const;

export type FieldSpec = { path: string } & (
  | { kind: "int"; min: number; max: number }
  | { kind: "bool" }
  | { kind: "text"; required?: boolean }
  | { kind: "select"; options: string[] }
  | { kind: "lines"; rule: "masked" | "addr"; max: number }
);

/** Display order; ids double as i18n keys (system.section.<id>). */
export const sections: { id: string; fields: FieldSpec[] }[] = [
  {
    id: "application",
    fields: [
      { path: "application.theme", kind: "select", options: ["system", "light", "dark"] },
      { path: "application.accent", kind: "text" },
      { path: "application.branding", kind: "text" },
    ],
  },
  {
    id: "logs",
    fields: [
      { path: "logs.level", kind: "select", options: ["debug", "info", "warn", "error"] },
      { path: "logs.retentionDays", kind: "int", min: 1, max: 3650 },
      { path: "logs.includeIP", kind: "bool" },
      { path: "logs.includeAuth", kind: "bool" },
    ],
  },
  {
    id: "batch",
    fields: [
      { path: "batch.enabled", kind: "bool" },
      { path: "batch.maxRequests", kind: "int", min: 1, max: 100 },
      { path: "batch.maxBodyBytes", kind: "int", min: 1024, max: maxBatchBody },
      { path: "batch.timeoutMs", kind: "int", min: 100, max: 300000 },
    ],
  },
  {
    id: "backups",
    fields: [
      { path: "backups.enabled", kind: "bool" },
      { path: "backups.schedule", kind: "text", required: true },
      { path: "backups.timezone", kind: "text", required: true },
      { path: "backups.keep", kind: "int", min: 0, max: 10000 },
    ],
  },
  {
    id: "proxy",
    fields: [{ path: "proxy.trustedProxyCIDRs", kind: "lines", rule: "masked", max: Infinity }],
  },
  {
    id: "rateLimits",
    fields: [
      { path: "rateLimits.enabled", kind: "bool" },
      { path: "rateLimits.excludedIPs", kind: "lines", rule: "addr", max: 100 },
    ],
  },
  {
    id: "adminThrottle",
    fields: [
      { path: "rateLimits.admin.accountMaxFailures", ...failures },
      { path: "rateLimits.admin.networkMaxFailures", ...failures },
      { path: "rateLimits.admin.windowSeconds", ...windowRange },
      { path: "rateLimits.admin.blockSeconds", ...block },
    ],
  },
  {
    id: "userThrottle",
    fields: [
      { path: "rateLimits.user.accountMaxFailures", ...failures },
      { path: "rateLimits.user.networkMaxFailures", ...failures },
      { path: "rateLimits.user.registrationNetworkMaxFailures", ...failures },
      { path: "rateLimits.user.windowSeconds", ...windowRange },
      { path: "rateLimits.user.blockSeconds", ...block },
    ],
  },
];

const specs = sections.flatMap((section) => section.fields);

export interface RuleDraft {
  label: string;
  audience: string;
  duration: string;
  maxRequests: string;
}

export interface SettingsDraft {
  values: Record<string, string | boolean>;
  rules: RuleDraft[];
}

export type FieldError = { key: MessageKey; vars?: Record<string, string | number> };

type Tree = Record<string, unknown>;

function read(tree: unknown, path: string): unknown {
  return path.split(".").reduce<unknown>((node, key) => (node as Tree | null)?.[key], tree);
}

function write(tree: Tree, path: string, value: unknown): void {
  const keys = path.split(".");
  const last = keys.pop() as string;
  let node = tree;
  for (const key of keys) node = (node[key] ??= {}) as Tree;
  node[last] = value;
}

export function toDraft(settings: RuntimeSettings): SettingsDraft {
  const values: SettingsDraft["values"] = {};
  for (const spec of specs) {
    const value = read(settings, spec.path);
    // List fields arrive as null when unset.
    values[spec.path] = spec.kind === "bool" ? Boolean(value) : spec.kind === "lines" ? ((value as string[] | null) ?? []).join("\n") : String(value ?? "");
  }
  const rules = (settings.rateLimits.rules ?? []).map((rule) => ({
    label: rule.label,
    audience: rule.audience,
    duration: String(rule.duration),
    maxRequests: String(rule.maxRequests),
  }));
  return { values, rules };
}

/** The entered value in the API's shape; unparsable integers become NaN and are caught by validate. */
const int = (text: string) => (/^-?\d+$/.test(text.trim()) ? Number(text.trim()) : NaN);

/** The full document: PATCH merges one level deep, so every section is sent whole. */
export function toPatch(draft: SettingsDraft, revision: number) {
  const tree: Tree = { revision };
  for (const spec of specs) {
    const value = draft.values[spec.path];
    write(tree, spec.path, spec.kind === "int" ? int(value as string) : spec.kind === "lines" ? lines(value as string) : value);
  }
  write(
    tree,
    "rateLimits.rules",
    draft.rules.map((rule) => ({ label: rule.label.trim(), audience: rule.audience, duration: int(rule.duration), maxRequests: int(rule.maxRequests) })),
  );
  return tree as unknown as RuntimeSettings;
}

/** Same document, compared as text so line spacing in list fields is not a change. */
export function isDirty(draft: SettingsDraft, settings: RuntimeSettings): boolean {
  return JSON.stringify(toPatch(draft, 0)) !== JSON.stringify(toPatch(toDraft(settings), 0));
}

function validRateLabel(label: string): boolean {
  if (label.length === 0 || label.length > 256) return false;
  let route = label;
  if (!label.startsWith("/")) {
    const space = label.indexOf(" ");
    if (space < 1 || !/^[A-Z]+$/.test(label.slice(0, space))) return false;
    route = label.slice(space + 1);
  }
  // eslint-disable-next-line no-control-regex
  return route.startsWith("/") && !/[?#\s\u0000-\u001f\u007f]/.test(route);
}

function checkInt(text: string, min: number, max: number): FieldError | undefined {
  const value = int(text);
  if (Number.isNaN(value)) return { key: "system.invalid.int" };
  if (value < min || value > max) return { key: "system.invalid.range", vars: { min, max } };
}

export function validate(draft: SettingsDraft): Record<string, FieldError> {
  const errors: Record<string, FieldError> = {};
  for (const spec of specs) {
    const value = draft.values[spec.path];
    let error: FieldError | undefined;
    if (spec.kind === "int") error = checkInt(value as string, spec.min, spec.max);
    if (spec.kind === "text" && spec.required && !(value as string).trim()) error = { key: "system.invalid.required" };
    if (spec.kind === "lines") {
      const entries = lines(value as string);
      const bad = entries.find((entry) => !(spec.rule === "masked" ? isMaskedPrefix(entry) : isAddrOrPrefix(entry)));
      if (bad !== undefined) error = { key: spec.rule === "masked" ? "system.invalid.cidr" : "system.invalid.ip", vars: { value: bad } };
      else if (entries.length > spec.max) error = { key: "system.invalid.tooMany", vars: { max: spec.max } };
    }
    if (error) errors[spec.path] = error;
  }
  if (draft.rules.length > 100) errors["rules"] = { key: "system.invalid.tooMany", vars: { max: 100 } };
  const seen = new Set<string>();
  draft.rules.forEach((rule, index) => {
    const key = `${rule.label.trim()}\u0000${rule.audience}`;
    if (!validRateLabel(rule.label.trim())) errors[`rules.${index}.label`] = { key: "system.invalid.rateLabel" };
    else if (seen.has(key)) errors[`rules.${index}.label`] = { key: "system.invalid.duplicateRule" };
    seen.add(key);
    const duration = checkInt(rule.duration, 1, 86400);
    if (duration) errors[`rules.${index}.duration`] = duration;
    const max = checkInt(rule.maxRequests, 1, 100000);
    if (max) errors[`rules.${index}.maxRequests`] = max;
  });
  return errors;
}
