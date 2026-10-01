import { parseJSON, stringifyJSON } from "../backend/json";
import type { CollectionDefinition, SchemaPort, ViewDefinition, ViewFilter } from "../backend/ports";
import { ViewSamplePreview } from "./view-sample-preview";

export function ViewDefinitionEditor({ view, sources, disabled, modeLocked = false, onChange, schema }: {
	view: ViewDefinition; sources: CollectionDefinition[]; disabled: boolean; modeLocked?: boolean; onChange(view: ViewDefinition): void; schema?: SchemaPort;
}) {
	const sqlMode = "sql" in view;
	if (sqlMode) return <fieldset disabled={disabled}>
		<legend>View definition</legend>
		<label>Definition mode<select disabled={modeLocked} value="sql" onChange={() => onChange({ source: "", projection: [] })}><option value="builder">Source and projection</option><option value="sql">SQL query</option></select></label>
		<label>SQL SELECT<textarea required rows={12} maxLength={65_536} value={view.sql} onChange={(event) => onChange({ sql: event.target.value })} placeholder={'SELECT id, title\nFROM app.notes\nWHERE published = true'} /></label>
		<p>Enter one read-only SELECT query using app collection and physical column names. Preview asks PostgreSQL to validate it and infer the fields before anything is applied.</p>
		<ViewSamplePreview disabled={disabled} query={view.sql ?? ""} schema={schema} />
		{view.dependencies && view.dependencies.length > 0 && <p>Depends on: {view.dependencies.join(", ")}</p>}
	</fieldset>;
  const source = sources.find((collection) => collection.name === view.source);
  const fields = (source?.fields ?? []).filter((field) => field.type !== "relation" && field.type !== "file");
  const filterField = fields.find((field) => field.name === view.filter?.field);
  // Number inputs discard valid arbitrary-precision tokens outside the
  // JavaScript number range; keep the text and let the server validate it.
  const numeric = filterField?.type === "integer" || filterField?.type === "bigint" || filterField?.type === "numeric";
  const filterValue = (value: string): unknown => {
    if (!numeric) return value;
    try { return parseJSON(value); } catch { return value; }
  };
  const setFilter = (filter?: ViewFilter) => onChange({ ...view, filter });
  const preservedFilter = view.filter?.op === "all" || view.filter?.op === "any" || view.filter?.value === null;
  return <fieldset disabled={disabled}>
    <legend>View definition</legend>
	<label>Definition mode<select disabled={modeLocked} value="builder" onChange={() => onChange({ sql: "" })}><option value="builder">Source and projection</option><option value="sql">SQL query</option></select></label>
    <label>Source collection <select value={view.source} onChange={(event) => onChange({ source: event.target.value, projection: [] })} required>
      <option value="">Choose a source</option>
      {sources.map((collection) => <option key={collection.name} value={collection.name}>{collection.name}</option>)}
    </select></label>
    <p>Choose projected fields. The view inherits access from its source and cannot modify records.</p>
    {fields.map((field) => <label key={field.name}><input type="checkbox" checked={view.projection.includes(field.name)} onChange={(event) => onChange({ ...view, projection: event.target.checked ? [...view.projection, field.name] : view.projection.filter((name) => name !== field.name) })} />Project {field.name}</label>)}
    {preservedFilter ? <div>
      <p>This filter is preserved when you change projected fields.</p>
      <pre aria-label="Preserved static filter">{stringifyJSON(view.filter, 2)}</pre>
      <button type="button" onClick={() => setFilter({ op: "eq", field: "", value: "" })}>Replace filter</button>
      <button type="button" onClick={() => setFilter(undefined)}>Remove filter</button>
    </div> : <>
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
        : <label>Filter value <input required={filterField?.type !== "text" && filterField?.type !== "editor"} type="text" inputMode={numeric ? "decimal" : undefined} value={String(view.filter.value ?? "")} onChange={(event) => setFilter({ ...view.filter!, value: filterValue(event.target.value) })} /></label>}
    </>}
    </>}
  </fieldset>;
}
