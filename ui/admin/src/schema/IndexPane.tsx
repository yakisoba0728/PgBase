import { RiCloseLine } from "@remixicon/react";
import type { IndexKey } from "../api/collections";
import { Button, IconButton } from "../components/Button";
import { Checkbox, Field } from "../components/Field";
import { useT } from "../i18n";
import type { DraftIndex } from "./draft";
import { describeErrors, indexColumns } from "./SchemaRows";
import type { SchemaEditor } from "./useSchemaEditor";

/** Compound index editor. Single-column indexes are the field's own unique/indexed flags. */
export function IndexPane({ editor, index }: { editor: SchemaEditor; index: DraftIndex }) {
  const t = useT();
  const errors = editor.errors.indexes[index.key];
  const update = (patch: Partial<DraftIndex>) => editor.updateIndex(index.key, patch);
  const columns = indexColumns(index);
  // Edited columns always use the keys shape: it takes one column, a sort order and a predicate.
  const setColumns = (keys: IndexKey[]) => update({ keys, fields: undefined });
  const candidates = editor.draft.fields.filter((field) => field.type !== "file" && field.type !== "password" && field.name);
  const toggle = (name: string, on: boolean) =>
    setColumns(on ? [...columns, { field: name }] : columns.filter((key) => key.field !== name));
  const setOrder = (name: string, order: "asc" | "desc") => setColumns(columns.map((key) => (key.field === name ? { ...key, order } : key)));

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
          {index.rawSql ? (
            <Field kind="textarea" label={t("schema.rawIndex")} mono rows={4} value={index.rawSql} readOnly hint={t("schema.rawIndexHint")} />
          ) : (
            <>
              <fieldset className="schema-group">
                <legend>{t("schema.indexColumns")}</legend>
                <ul className="schema-columns-pick">
                  {columns.filter((key) => key.expression).map((key) => (
                    <li key={key.expression}><span className="mono">{key.expression}</span></li>
                  ))}
                  {candidates.map((field) => {
                    const key = columns.find((candidate) => candidate.field === field.name);
                    const position = key ? columns.indexOf(key) + 1 : undefined;
                    return (
                      <li key={field.key}>
                        <Checkbox label={<span className="mono">{field.name}</span>} checked={Boolean(key)} onChange={(event) => toggle(field.name, event.target.checked)} />
                        {key && (
                          <>
                            <span className="schema-count">{position}</span>
                            <select className="schema-order" aria-label={t("schema.order", { name: field.name })} value={key.order ?? "asc"} onChange={(event) => setOrder(field.name, event.target.value as "asc" | "desc")}>
                              <option value="asc">asc</option>
                              <option value="desc">desc</option>
                            </select>
                          </>
                        )}
                      </li>
                    );
                  })}
                </ul>
              </fieldset>
              <Field label={t("schema.where")} mono spellCheck={false} placeholder="published = true" hint={t("schema.whereHint")} value={index.where ?? ""}
                onChange={(event) => update({ where: event.target.value || undefined, keys: columns, fields: undefined })} />
            </>
          )}
          <Checkbox label={t("schema.flag.unique")} checked={index.unique} onChange={(event) => update({ unique: event.target.checked })} />
        </div>
      </div>
      <footer className="pane-footer">
        <Button variant="transparent" className="txt-danger pane-footer-start" onClick={() => editor.removeIndex(index.key)}>{t("schema.removeIndex")}</Button>
        <Button variant="secondary" onClick={() => editor.setPane(null)}>{t("common.close")}</Button>
      </footer>
    </>
  );
}
