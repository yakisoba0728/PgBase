import { useMutation } from "@tanstack/react-query";
import { RiAddLine, RiCloseLine, RiPlayLine } from "@remixicon/react";
import { sampleView, type Collection, type FieldType } from "../api/collections";
import { stringifyJson } from "../api/http";
import { errorText } from "../app/queries";
import { Button, IconButton } from "../components/Button";
import { CodeArea } from "../components/CodeArea";
import { CodeBlock } from "../components/CodeBlock";
import { DataGrid } from "../components/DataGrid";
import { Checkbox, Field } from "../components/Field";
import { Tabs } from "../components/Tabs";
import { useT } from "../i18n";
import { compareOps, filterable, filterFromRows, filterRows, projectable, type DeclarativeView, type FilterRow } from "./draft";
import { describeErrors, Section, viewSqlProblem } from "./SchemaRows";
import type { SchemaEditor } from "./useSchemaEditor";

function sampleText(value: unknown): string | null {
  if (value === null || value === undefined) return null;
  return typeof value === "object" ? stringifyJson(value) : String(value);
}

function SqlView({ editor }: { editor: SchemaEditor }) {
  const t = useT();
  const sample = useMutation({ mutationFn: (query: string) => sampleView(query) });
  const query = editor.draft.viewQuery;
  const fresh = sample.variables === query;
  const columns = sample.data && fresh ? ["id", ...sample.data.fields.map((field) => field.Name).filter((name) => name !== "id")] : [];
  const sampleError = fresh && sample.error && (viewSqlProblem(t, sample.error) ?? errorText(t, sample.error));
  const planCodes = editor.errors.collection.viewQuery ?? editor.errors.collection.view;
  const planError = viewSqlProblem(t, editor.planQuery.error) ?? (planCodes && describeErrors(t, planCodes));
  const run = () => query.trim() && sample.mutate(query);
  return (
    <>
      <div className="field-wrap">
        <CodeArea label={t("schema.sqlLabel")} rows={8} maxLength={5000} placeholder="select id, title from app.posts where published" value={query} onChange={(event) => editor.setMeta({ viewQuery: event.target.value })} onSubmit={run} />
        <p className={sampleError || planError ? "field-error" : "field-hint"} role={sampleError || planError ? "alert" : undefined}>
          {sampleError || planError || t("schema.sqlHint")}
        </p>
      </div>
      <div className="schema-sample-bar">
        <Button size="sm" variant="secondary" icon={RiPlayLine} loading={sample.isPending} disabled={!query.trim()} onClick={run}>{t("schema.sample")}</Button>
      </div>
      {sample.data && fresh && (
        <div className="schema-sample">
          <p className="field-hint">{t("schema.sampleResult", { columns: sample.data.fields.length, rows: sample.data.items?.length ?? 0 })}</p>
          <div className="grid-scroll">
            <DataGrid
              label={t("schema.sample")}
              columns={columns.map((name) => ({ name, type: sample.data.fields.find((field) => field.Name === name)?.Type }))}
              rows={(sample.data.items ?? []).map((row) => columns.map((name) => ({ text: sampleText(row[name]) })))}
            />
          </div>
        </div>
      )}
    </>
  );
}

/** Source, projected fields and a list of comparisons, as the planner takes them. */
function DeclarativeEditor({ editor, catalog }: { editor: SchemaEditor; catalog: Collection[] }) {
  const t = useT();
  const view = editor.draft.view;
  const set = (patch: Partial<DeclarativeView>) => editor.setMeta({ view: { ...view, ...patch } });
  const sources = catalog.filter((collection) => collection.type !== "view");
  const source = sources.find((collection) => collection.name === view.source);
  const fields = (source?.fields ?? []).filter((field) => projectable(field.type));
  const types: Record<string, FieldType> = Object.fromEntries((source?.fields ?? []).map((field) => [field.name, field.type]));
  const editable = filterRows(view.filter);
  const error = editor.errors.collection.view;

  const toggle = (name: string, on: boolean) => {
    const chosen = new Set(view.projection);
    if (on) chosen.add(name);
    else chosen.delete(name);
    // Source order keeps the view's columns stable.
    set({ projection: fields.map((field) => field.name).filter((candidate) => chosen.has(candidate)) });
  };
  const setRows = (join: "all" | "any", rows: FilterRow[]) => set({ filter: filterFromRows(join, rows, types) });
  const filterFields = (source?.fields ?? []).filter((field) => filterable(field.type));

  return (
    <div className="schema-view">
      <Field kind="select" label={t("schema.view.source")} value={view.source} onChange={(event) => editor.setMeta({ view: { source: event.target.value, projection: [] } })}
        options={[{ value: "", label: t("schema.view.pickSource") }, ...sources.map((collection) => ({ value: collection.name, label: collection.name }))]} />
      {source && (
        <>
          <fieldset className="schema-group">
            <legend>{t("schema.view.projection", { count: view.projection.length })}</legend>
            <ul className="schema-columns-pick">
              {fields.map((field) => (
                <li key={field.id}>
                  <Checkbox label={<><span className="mono">{field.name}</span> <span className="txt-hint">{field.type}</span></>} checked={view.projection.includes(field.name)} onChange={(event) => toggle(field.name, event.target.checked)} />
                </li>
              ))}
            </ul>
            <p className="field-hint">{t("schema.view.projectionHint")}</p>
          </fieldset>
          <fieldset className="schema-group">
            <legend>{t("schema.view.filter")}</legend>
            {editable ? (
              <>
                {editable.rows.length > 1 && (
                  <select className="schema-input schema-join" aria-label={t("schema.view.join")} value={editable.join} onChange={(event) => setRows(event.target.value as "all" | "any", editable.rows)}>
                    <option value="all">{t("schema.view.all")}</option>
                    <option value="any">{t("schema.view.any")}</option>
                  </select>
                )}
                <ul className="schema-filter">
                  {editable.rows.map((row, position) => {
                    const change = (patch: Partial<FilterRow>) => setRows(editable.join, editable.rows.map((item, index) => (index === position ? { ...item, ...patch } : item)));
                    const type = types[row.field];
                    return (
                      <li key={position}>
                        <select className="schema-input" aria-label={t("schema.view.field")} value={row.field} onChange={(event) => change({ field: event.target.value })}>
                          {filterFields.map((field) => <option key={field.id} value={field.name}>{field.name}</option>)}
                        </select>
                        <select className="schema-input mono" aria-label={t("schema.view.op")} value={row.op} onChange={(event) => change({ op: event.target.value as FilterRow["op"] })}>
                          {compareOps.filter((op) => (type !== "boolean" && type !== "uuid") || op === "eq" || op === "ne").map((op) => <option key={op} value={op}>{t(`schema.view.op.${op}`)}</option>)}
                        </select>
                        {type === "boolean" ? (
                          <select className="schema-input mono" aria-label={t("schema.view.value")} value={row.value === "false" ? "false" : row.value === "null" ? "null" : "true"} onChange={(event) => change({ value: event.target.value })}>
                            <option value="true">true</option>
                            <option value="false">false</option>
                            <option value="null">null</option>
                          </select>
                        ) : (
                          <input className="schema-input mono" aria-label={t("schema.view.value")} spellCheck={false} placeholder={type === "timestamptz" || type === "autodate" ? "2026-01-01T00:00:00Z" : "null"} value={row.value} onChange={(event) => change({ value: event.target.value })} />
                        )}
                        <IconButton label={t("schema.view.removeCondition")} icon={RiCloseLine} onClick={() => setRows(editable.join, editable.rows.filter((_, index) => index !== position))} />
                      </li>
                    );
                  })}
                </ul>
                <Button size="sm" variant="transparent" className="schema-start" icon={RiAddLine} disabled={filterFields.length === 0 || editable.rows.length >= 16}
                  onClick={() => setRows(editable.join, [...editable.rows, { field: filterFields[0].name, op: "eq", value: filterFields[0].type === "boolean" ? "true" : "" }])}>
                  {t("schema.view.addCondition")}
                </Button>
                <p className="field-hint">{t("schema.view.filterHint")}</p>
              </>
            ) : (
              <>
                <CodeBlock label={t("schema.view.filter")} text={stringifyJson(view.filter)} />
                <p className="field-hint">{t("schema.view.nested")}</p>
                <Button size="sm" variant="transparent" className="schema-start" onClick={() => set({ filter: undefined })}>{t("schema.view.clearFilter")}</Button>
              </>
            )}
          </fieldset>
        </>
      )}
      {error && <p className="field-error" role="alert">{describeErrors(t, error)}</p>}
    </div>
  );
}

/** A view's definition: SQL with a sample run, or declarative (create picks the kind; it is fixed afterwards). */
export function ViewSection({ editor, catalog }: { editor: SchemaEditor; catalog: Collection[] }) {
  const t = useT();
  const mode = editor.draft.viewMode;
  const picker = editor.creating && (
    <Tabs label={t("schema.view.mode")} value={mode} onChange={(viewMode) => editor.setMeta({ viewMode })}
      tabs={[{ value: "declarative", label: t("schema.view.declarative") }, { value: "sql", label: t("schema.view.sql") }]} />
  );
  return (
    <Section title={t("schema.viewQuery")} action={picker}>
      {mode === "declarative" ? <DeclarativeEditor editor={editor} catalog={catalog} /> : <SqlView editor={editor} />}
    </Section>
  );
}
