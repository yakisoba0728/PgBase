import { useState } from "react";
import { RiAddLine, RiArrowDownSLine, RiArrowUpSLine, RiCloseLine } from "@remixicon/react";
import type { IndexKey } from "../api/collections";
import { Button, IconButton } from "../components/Button";
import { CodeArea } from "../components/CodeArea";
import { Checkbox, Field } from "../components/Field";
import { Tabs } from "../components/Tabs";
import { useT } from "../i18n";
import { indexable, indexCollations, rawIndexTemplate, type DraftIndex } from "./draft";
import { describeErrors, indexColumns } from "./SchemaRows";
import type { SchemaEditor } from "./useSchemaEditor";

/** Compound, expression and partial indexes, or one written in SQL. Single-column indexes are the field's own flags. */
export function IndexPane({ editor, index }: { editor: SchemaEditor; index: DraftIndex }) {
  const t = useT();
  const [expression, setExpression] = useState("");
  const errors = editor.errors.indexes[index.key];
  const update = (patch: Partial<DraftIndex>) => editor.updateIndex(index.key, patch);
  const keys = indexColumns(index);
  // Edited keys always use the keys shape: it carries order, collation, expressions and a predicate.
  const setKeys = (next: IndexKey[]) => update({ keys: next, fields: undefined });
  const types = Object.fromEntries(editor.draft.fields.map((field) => [field.name, field.type]));
  const candidates = editor.draft.fields.filter((field) => field.name && indexable(field.type) && !keys.some((key) => key.field === field.name));
  const sql = index.rawSql !== undefined;
  const collection = editor.draft.name || "collection";

  const setMode = (mode: "keys" | "sql") => {
    if (mode === "sql") update({ rawSql: rawIndexTemplate(index, collection), keys: undefined, fields: undefined, where: undefined });
    else update({ rawSql: undefined, keys: [] });
  };
  const patchKey = (position: number, patch: Partial<IndexKey>) => setKeys(keys.map((key, index) => (index === position ? { ...key, ...patch } : key)));
  const move = (position: number, step: -1 | 1) => {
    const next = [...keys];
    [next[position], next[position + step]] = [next[position + step], next[position]];
    setKeys(next);
  };
  const addExpression = () => {
    if (!expression.trim()) return;
    setKeys([...keys, { expression: expression.trim() }]);
    setExpression("");
  };

  return (
    <>
      <header className="pane-header">
        <h2 className="pane-title mono">{index.name || t("schema.index")}</h2>
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={() => editor.setPane(null)} />
      </header>
      <div className="pane-body">
        {errors && (
          <div className="pane-notice" data-tone="danger" role="alert">
            <strong>{t("schema.serverRejected")}</strong>
            <p>{describeErrors(t, errors)}</p>
          </div>
        )}
        <div className="form">
          <Field label={t("schema.indexName")} mono spellCheck={false} value={index.name} onChange={(event) => update({ name: event.target.value })} />
          <Checkbox label={t("schema.flag.unique")} checked={index.unique} onChange={(event) => update({ unique: event.target.checked })} />
          <Tabs label={t("schema.index.kind")} value={sql ? "sql" : "keys"} onChange={setMode}
            tabs={[{ value: "keys", label: t("schema.index.keys") }, { value: "sql", label: t("schema.index.sql") }]} />
          {sql ? (
            <div className="field-wrap">
              <CodeArea label={t("schema.rawIndex")} rows={5} value={index.rawSql} onChange={(event) => update({ rawSql: event.target.value })} />
              <p className="field-hint">{t("schema.index.sqlHint", { table: `app.${collection}` })}</p>
            </div>
          ) : (
            <>
              <fieldset className="schema-group">
                <legend>{t("schema.indexColumns")}</legend>
                {keys.length === 0 && <p className="field-hint">{t("schema.index.noKeys")}</p>}
                <ol className="schema-keys">
                  {keys.map((key, position) => {
                    const name = key.field ?? key.expression ?? "";
                    const textual = key.expression !== undefined || types[key.field ?? ""] === "text" || types[key.field ?? ""] === "editor";
                    return (
                      <li key={`${position}:${name}`}>
                        <span className="schema-count">{position + 1}</span>
                        <span className="mono schema-key-name" title={name}>{key.expression !== undefined ? `(${name})` : name}</span>
                        {textual && (
                          <select className="schema-order" aria-label={t("schema.index.collation", { name })} value={key.collation ?? ""} onChange={(event) => patchKey(position, { collation: event.target.value || undefined })}>
                            {indexCollations.map((collation) => <option key={collation} value={collation}>{collation || t("schema.index.defaultCollation")}</option>)}
                          </select>
                        )}
                        <select className="schema-order" aria-label={t("schema.order", { name })} value={key.order ?? "asc"} onChange={(event) => patchKey(position, { order: event.target.value as "asc" | "desc" })}>
                          <option value="asc">asc</option>
                          <option value="desc">desc</option>
                        </select>
                        <IconButton label={t("schema.moveUp", { name })} icon={RiArrowUpSLine} disabled={position === 0} onClick={() => move(position, -1)} />
                        <IconButton label={t("schema.moveDown", { name })} icon={RiArrowDownSLine} disabled={position === keys.length - 1} onClick={() => move(position, 1)} />
                        <IconButton label={t("schema.index.removeKey", { name })} icon={RiCloseLine} onClick={() => setKeys(keys.filter((_, index) => index !== position))} />
                      </li>
                    );
                  })}
                </ol>
                <div className="schema-key-add">
                  <select className="schema-input" aria-label={t("schema.index.addColumn")} value="" disabled={candidates.length === 0 || keys.length >= 8}
                    onChange={(event) => event.target.value && setKeys([...keys, { field: event.target.value }])}>
                    <option value="">{t("schema.index.addColumn")}</option>
                    {candidates.map((field) => <option key={field.key} value={field.name}>{field.name}</option>)}
                  </select>
                  <input className="schema-input mono" aria-label={t("schema.index.expression")} placeholder="lower(title)" spellCheck={false} value={expression} disabled={keys.length >= 8}
                    onChange={(event) => setExpression(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); addExpression(); } }} />
                  <Button size="sm" variant="secondary" icon={RiAddLine} disabled={!expression.trim() || keys.length >= 8} onClick={addExpression}>{t("schema.index.addExpression")}</Button>
                </div>
                <p className="field-hint">{t("schema.index.keysHint")}</p>
              </fieldset>
              <Field label={t("schema.where")} mono spellCheck={false} placeholder="published = true" hint={t("schema.whereHint")} value={index.where ?? ""}
                onChange={(event) => update({ where: event.target.value || undefined, keys, fields: undefined })} />
            </>
          )}
        </div>
      </div>
      <footer className="pane-footer">
        <Button variant="transparent" className="txt-danger pane-footer-start" onClick={() => editor.removeIndex(index.key)}>{t("schema.removeIndex")}</Button>
        <Button variant="secondary" onClick={() => editor.setPane(null)}>{t("common.close")}</Button>
      </footer>
    </>
  );
}
