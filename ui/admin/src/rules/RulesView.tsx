import type { KeyboardEvent } from "react";
import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { RiPlayLine } from "@remixicon/react";
import { Facts } from "../components/Facts";
import { UnsavedGuard } from "../components/UnsavedGuard";
import { authCollectionsQuery } from "../auth/queries";
import { stringifyJson } from "../api/http";
import type { Collection } from "../api/collections";
import { Button } from "../components/Button";
import { QueryView } from "../components/QueryView";
import { Checkbox, Field } from "../components/Field";
import { SaveBar } from "../components/SaveBar";
import { useT, type MessageKey } from "../i18n";
import { expressionChanged, modes, previewable, typedRule, type Action } from "./model";
import { useRulesEditor, type RulesEditor } from "./useRulesEditor";
import "../styles/rules.css";

const variables = [
  "@request.auth.id",
  "@request.auth.verified",
  "@request.auth.collectionId",
  "@request.method",
  "@request.body.",
  "@request.query.",
  "@request.headers.",
  "@collection.",
];
const modifiers = [":isset", ":changed", ":each", ":lower", ":length"];
const macros = ["@now", "@todayStart", "@todayEnd", "@monthStart", "@yearStart"];
const operators = ["=", "!=", ">", ">=", "<", "<=", "~", "!~", "?=", "&&", "||"];

function RuleRow({ editor, action }: { editor: RulesEditor; action: Action }) {
  const t = useT();
  const draft = editor.drafts![action];
  const error = editor.errors[action];
  const testable = previewable(action) && typedRule(draft) !== null;
  const testing = editor.preview.isPending && editor.preview.variables === action;
  const choose = (mode: typeof draft.mode) => editor.update(action, { mode, expression: mode === "expression" ? draft.expression || draft.loaded.expression || "" : draft.expression });
  const onModeKey = (event: KeyboardEvent<HTMLDivElement>) => {
    const step = event.key === "ArrowRight" || event.key === "ArrowDown" ? 1 : event.key === "ArrowLeft" || event.key === "ArrowUp" ? -1 : 0;
    if (!step) return;
    event.preventDefault();
    const at = modes.indexOf(draft.mode as (typeof modes)[number]);
    // From a custom rule (no enabled mode checked) the arrows land on the first or last mode.
    const next = modes[(Math.max(at, step > 0 ? -1 : 0) + step + modes.length) % modes.length];
    choose(next);
    event.currentTarget.querySelector<HTMLElement>(`[data-mode="${next}"]`)?.focus();
  };
  return (
    <li className="rule-row" data-invalid={error ? "" : undefined}>
      <div className="rule-head">
        <span className="rule-action mono">{action}</span>
        <span className="rule-desc">{t(`rules.action.${action}` as MessageKey)}</span>
        <div className="rule-modes" role="radiogroup" aria-label={t("rules.modeFor", { action })} onKeyDown={onModeKey}>
          {(draft.mode === "custom" ? [...modes, "custom" as const] : modes).map((mode) => (
            <button
              key={mode}
              type="button"
              role="radio"
              aria-checked={draft.mode === mode}
              // Radio group: one tab stop (the checked mode, or the first when the rule is custom); arrows move.
              tabIndex={mode === (draft.mode === "custom" ? modes[0] : draft.mode) ? 0 : -1}
              data-mode={mode}
              disabled={mode === "custom"}
              className="rule-mode"
              onClick={() => choose(mode)}
            >
              {t(`rules.mode.${mode}`)}
            </button>
          ))}
        </div>
      </div>
      {draft.mode === "expression" && (
        <Field
          kind="textarea"
          label={t("rules.expression")}
          mono
          rows={2}
          spellCheck={false}
          autoComplete="off"
          placeholder="published = true && @request.auth.id != ''"
          value={draft.expression}
          error={error}
          onFocus={(event) => {
            editor.focused.current = { action, element: event.currentTarget };
          }}
          onChange={(event) => editor.update(action, { expression: event.target.value })}
        />
      )}
      {draft.mode !== "expression" && error && <p className="field-error" role="alert">{error}</p>}
      {draft.mode === "custom" && <pre className="rule-json mono">{stringifyJson(draft.loaded)}</pre>}
      {previewable(action) && (
        <div className="rule-test">
          {testable ? (
            <Button size="sm" variant="transparent" icon={RiPlayLine} loading={testing} onClick={() => editor.preview.mutate(action)}>{t("rules.test")}</Button>
          ) : (
            expressionChanged(draft) && <span className="field-hint">{t("rules.testAfterSave")}</span>
          )}
          {editor.results[action] && <span className="rule-result">{editor.results[action]}</span>}
        </div>
      )}
    </li>
  );
}

/** Auth records use the auth collection's own rule set; shown for reference. */
function AuthRules({ name }: { name: string }) {
  const t = useT();
  const query = useQuery(authCollectionsQuery);
  const auth = query.data?.find((item) => item.name === name);
  if (!auth) return null;
  return (
    <section className="stack-section">
      <h2>{t("rules.authTitle")}</h2>
      <p className="field-hint">{t("rules.authHint")}</p>
      <Facts items={Object.entries(auth.rules).map(([key, value]) => ({ label: <span className="mono">{key}</span>, value: value === null ? <span className="na">{t("rules.mode.admins")}</span> : value === "" ? t("rules.mode.everyone") : value, mono: value !== null && value !== "" }))} />
    </section>
  );
}

function Reference({ editor }: { editor: RulesEditor }) {
  const t = useT();
  const fields = ["id", ...editor.collection.fields.filter((field) => field.type !== "file" && !field.hidden).map((field) => field.name), "created_at", "updated_at"];
  const group = (title: string, items: string[]) => (
    <div className="rules-ref-group">
      <h3>{title}</h3>
      <div className="rules-tokens">
        {items.map((item) => (
          <button key={item} type="button" className="chip mono rules-token" onMouseDown={(event) => event.preventDefault()} onClick={() => editor.insert(item)}>
            {item}
          </button>
        ))}
      </div>
    </div>
  );
  return (
    <aside className="rules-ref" aria-label={t("rules.reference")}>
      <Field label={t("rules.testAs")} mono spellCheck={false} autoComplete="off" placeholder={t("rules.testAsGuest")} value={editor.testAs} onChange={(event) => editor.setTestAs(event.target.value)} />
      <p className="field-hint">{t("rules.insertHint")}</p>
      {group(t("rules.fields"), fields)}
      {group(t("rules.variables"), variables)}
      {group(t("rules.modifiers"), modifiers)}
      {group(t("rules.macros"), macros)}
      {group(t("rules.operators"), operators)}
    </aside>
  );
}

/** The Rules tab: one row per action, saved together with the public-access gate. */
export function RulesView({ collection, basePath }: { collection: Collection; basePath: string }) {
  const t = useT();
  const source = collection.type === "view" && !collection.view?.sql ? collection.view?.source : undefined;
  const editor = useRulesEditor(source ? { ...collection, name: source, type: "base" } : collection, basePath);
  const isView = collection.type === "view";
  return (
    <>
      <QueryView query={editor.query} skeleton="form">
        {() => (
          <div className="page-form">
            <div className="toolbar">
              {source ? <p>{t("rules.inherited")} <Link to={`/collections/${encodeURIComponent(source)}/rules`}>{source}</Link></p> : <SaveBar dirty={editor.dirty} busy={editor.save.isPending} onDiscard={editor.discard} onSave={() => editor.save.mutate()} />}
            </div>
            <fieldset className="page-content rules-layout" disabled={Boolean(source) || editor.save.isPending}>
              <div className="rules-main">

                <section className="stack-section">
                  <h2>{t("rules.accessTitle")}</h2>
                  <Checkbox label={t("rules.accessLabel")} checked={editor.accessEnabled} onChange={(event) => editor.setAccess(event.target.checked)} />
                  <p className="field-hint">{t(editor.accessEnabled ? "rules.accessOn" : "rules.accessOff")}</p>
                </section>
                <section className="stack-section">
                  <h2>{t("rules.title")}</h2>
                  {isView && <p className="field-hint">{t("rules.viewHint")}</p>}
                  <ul className="rules-list">
                    {editor.visible.map((action) => <RuleRow key={action} editor={editor} action={action} />)}
                  </ul>
                </section>
                {collection.type === "auth" && <AuthRules name={collection.name} />}
              </div>
              <Reference editor={editor} />
            </fieldset>
          </div>
        )}
      </QueryView>
      <UnsavedGuard {...editor.guard} />
    </>
  );
}
