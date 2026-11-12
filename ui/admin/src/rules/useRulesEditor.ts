import { useRef, useState } from "react";
import { useBlocker } from "react-router";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import {
  getCollectionRules,
  getCollectionUserAccess,
  previewCollectionRule,
  setCollectionRules,
  setCollectionUserAccess,
  setPocketBaseRules,
  type Collection,
} from "../api/collections";
import { ApiError } from "../api/http";
import { listRecords } from "../api/records";
import { errorText } from "../app/queries";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import {
  draftsFromRules,
  emptyExpressions,
  expressionChanged,
  insertAt,
  planSave,
  previewable,
  sameDraft,
  typedRule,
  visibleActions,
  type Action,
  type RuleDraft,
  type RuleDrafts,
} from "./model";

const uuidPattern = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i;

export const rulesQueryKey = (name: string) => ["rules", name] as const;

export function useRulesEditor(collection: Collection, basePath: string) {
  const t = useT();
  const name = collection.name;
  const visible = visibleActions(collection.type ?? "base");
  const queryClient = useQueryClient();
  const rules = useQuery({ queryKey: rulesQueryKey(name), queryFn: ({ signal }) => getCollectionRules(name, signal) });
  const access = useQuery({ queryKey: [...rulesQueryKey(name), "access"], queryFn: ({ signal }) => getCollectionUserAccess(name, signal) });

  // Edits sit on top of the loaded policy; a save clears them, so nothing needs syncing.
  const [edits, setEdits] = useState<Partial<RuleDrafts>>({});
  const [accessEdit, setAccessEdit] = useState<boolean>();
  const [errors, setErrors] = useState<Partial<Record<Action, string>>>({});
  const [results, setResults] = useState<Partial<Record<Action, string>>>({});
  const [testAs, setTestAs] = useState("");
  const focused = useRef<{ action: Action; element: HTMLTextAreaElement } | null>(null);

  const loaded = rules.data ? draftsFromRules(rules.data.rules) : undefined;
  const drafts = loaded && ({ ...loaded, ...edits } as RuleDrafts);
  const accessEnabled = accessEdit ?? access.data?.enabled ?? false;
  const changedRules = loaded && drafts ? visible.filter((action) => !sameDraft(drafts[action], loaded[action])) : [];
  const accessChanged = accessEdit !== undefined && accessEdit !== access.data?.enabled;
  const dirty = changedRules.length > 0 || accessChanged;

  const update = (action: Action, patch: Partial<RuleDraft>) => {
    if (!drafts) return;
    setEdits((current) => ({ ...current, [action]: { ...drafts[action], ...patch } }));
    setErrors(({ [action]: _, ...rest }) => rest);
    setResults(({ [action]: _, ...rest }) => rest);
  };

  const discard = () => {
    setEdits({});
    setAccessEdit(undefined);
    setErrors({});
  };

  const save = useMutation({
    mutationFn: async () => {
      if (!drafts) return;
      const empty = emptyExpressions(drafts, visible);
      if (empty.length) throw Object.assign(new Error("empty"), { empty });
      if (changedRules.length) {
        const plan = planSave(drafts, visible);
        if (plan.kind === "typed") await setCollectionRules(name, plan.rules);
        else await setPocketBaseRules(name, plan.rules);
        if (plan.kind === "split") {
          // shortcut: owner/typed rows have no expression form, so they are written back in a
          // second request; between the two the rows read as admins only (fail-closed).
          // Upgrade when the server can parse an expression without saving.
          const fresh = await getCollectionRules(name);
          await setCollectionRules(name, { ...fresh.rules, ...plan.keep });
        }
      }
      if (accessChanged) await setCollectionUserAccess(name, accessEnabled);
    },
    onSuccess: async () => {
      discard();
      setResults({});
      await queryClient.invalidateQueries({ queryKey: rulesQueryKey(name) });
      toast(t("rules.saved"));
    },
    onError: (error) => {
      const empty = (error as { empty?: Action[] }).empty;
      if (empty) {
        setErrors(Object.fromEntries(empty.map((action) => [action, t("rules.emptyExpression")])));
        return;
      }
      if (error instanceof ApiError && error.code === "invalid_rule" && drafts) {
        // The server names no rule, so every expression edited since the last save is suspect.
        const suspects = visible.filter((action) => expressionChanged(drafts[action]));
        if (suspects.length) {
          const message = t(suspects.length === 1 ? "rules.rejected" : "rules.rejectedAny");
          setErrors(Object.fromEntries(suspects.map((action) => [action, message])));
          return;
        }
      }
      toast(errorText(t, error), "danger");
    },
  });

  const blocker = useBlocker(({ nextLocation }) => dirty && !save.isPending && nextLocation.pathname !== `${basePath}/rules`);

  const preview = useMutation({
    mutationFn: async (action: Action) => {
      const rule = drafts && typedRule(drafts[action]);
      if (!rule || !previewable(action)) return "";
      const userId = testAs.trim();
      if (userId && !uuidPattern.test(userId)) return t("rules.testAsInvalid");
      const page = await listRecords(name, { limit: 50 });
      const recordIds = page.items.map((record) => String(record.id)).filter((id) => uuidPattern.test(id));
      if (!recordIds.length) return t("rules.testNoRecords");
      const decisions = await previewCollectionRule(name, { rule, action, recordIds, ...(userId ? { userId } : {}) });
      const allowed = decisions.filter((decision) => decision.allowed).length;
      return t(userId ? "rules.testResultUser" : "rules.testResultGuest", { allowed, total: decisions.length });
    },
    onSuccess: (text, action) => setResults((current) => ({ ...current, [action]: text })),
    onError: (error, action) => setResults((current) => ({ ...current, [action]: errorText(t, error) })),
  });

  /** Inserts a reference token into the expression that last had focus. */
  const insert = (text: string) => {
    if (!focused.current || !drafts) return;
    const { action, element } = focused.current;
    if (drafts[action].mode !== "expression" || !element.isConnected) return;
    const next = insertAt(drafts[action].expression, element.selectionStart, element.selectionEnd, text);
    update(action, { expression: next.value });
    requestAnimationFrame(() => {
      element.focus();
      element.setSelectionRange(next.caret, next.caret);
    });
  };

  return {
    collection,
    visible,
    loading: rules.isPending || access.isPending,
    loadError: rules.error ?? access.error,
    drafts,
    accessEnabled,
    setAccess: setAccessEdit,
    dirty,
    errors,
    results,
    testAs,
    setTestAs,
    update,
    discard,
    save,
    preview,
    insert,
    focused,
    blocker,
  };
}

export type RulesEditor = ReturnType<typeof useRulesEditor>;
