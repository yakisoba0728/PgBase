import { parseJSON } from "../backend/json";
import type { CollectionDefinition, ViewDefinition, ViewFilter } from "../backend/ports";

export function ViewDefinitionEditor({ view, sources, disabled, onChange }: {
  view: ViewDefinition; sources: CollectionDefinition[]; disabled: boolean; onChange(view: ViewDefinition): void;
}) {
  const source = sources.find((collection) => collection.name === view.source);
  const fields = (source?.fields ?? []).filter((field) => field.type !== "relation" && field.type !== "file");
  const filterField = fields.find((field) => field.name === view.filter?.field);
  const numeric = filterField?.type === "integer" || filterField?.type === "bigint" || filterField?.type === "numeric";
  const filterValue = (value: string): unknown => {
    if (!numeric) return value;
    try { return parseJSON(value); } catch { return value; }
  };
  const setFilter = (filter?: ViewFilter) => onChange({ ...view, filter });
  return <fieldset disabled={disabled}>
    <legend>View definition</legend>
    <label>Source collection <select value={view.source} onChange={(event) => onChange({ source: event.target.value, projection: [] })} required>
      <option value="">Choose a source</option>
      {sources.map((collection) => <option key={collection.name} value={collection.name}>{collection.name}</option>)}
    </select></label>
    <p>Choose projected fields. The view inherits access from its source and cannot modify records.</p>
    {fields.map((field) => <label key={field.name}><input type="checkbox" checked={view.projection.includes(field.name)} onChange={(event) => onChange({ ...view, projection: event.target.checked ? [...view.projection, field.name] : view.projection.filter((name) => name !== field.name) })} />Project {field.name}</label>)}
    <label>Static filter <select value={view.filter === undefined ? "none" : "compare"} onChange={(event) => setFilter(event.target.value === "none" ? undefined : { op: "eq", field: "", value: "" })}>
      <option value="none">All source rows</option><option value="compare">Compare a field</option>
    </select></label>
    {view.filter !== undefined && <>
      <label>Filter field <select required value={view.filter.field ?? ""} onChange={(event) => {
        const field = fields.find((entry) => entry.name === event.target.value);
        setFilter({ op: "eq", field: event.target.value, value: field?.type === "boolean" ? false : field?.type === "integer" || field?.type === "bigint" || field?.type === "numeric" ? 0 : "" });
      }}><option value="">Choose a field</option>{fields.filter((field) => field.type !== "jsonb").map((field) => <option key={field.name} value={field.name}>{field.name}</option>)}</select></label>
      <label>Comparison <select value={view.filter.op} onChange={(event) => setFilter({ ...view.filter!, op: event.target.value as ViewFilter["op"] })}>
        {(["eq", "ne", "lt", "lte", "gt", "gte"] as const).map((op) => <option key={op} value={op}>{op}</option>)}
      </select></label>
      {filterField?.type === "boolean" ? <label>Filter value <select value={String(view.filter.value)} onChange={(event) => setFilter({ ...view.filter!, value: event.target.value === "true" })}><option value="false">false</option><option value="true">true</option></select></label>
        : <label>Filter value <input required type={numeric ? "number" : "text"} step="any" value={String(view.filter.value ?? "")} onChange={(event) => setFilter({ ...view.filter!, value: filterValue(event.target.value) })} /></label>}
    </>}
  </fieldset>;
}
