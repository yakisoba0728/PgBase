import type { FieldDefinition } from "../backend/ports";
import { RelationTargetPicker } from "../relation/relation-target-picker";
import type { AdminRoute } from "../routing/admin-location";
import { useRecordEditor, type RecordEditor } from "../workspace/record-workspace";
import type { FieldState } from "../workspace/record-codec";

function RecordField({ editor, field }: { editor: RecordEditor; field: FieldDefinition }) {
  const state = editor.stateOf(field);
  const relation = field.type === "relation" ? field.relation : undefined;
  const multipleRelation = relation?.cardinality === "multiple";
  const relationHintID = relation === undefined ? undefined : `record-${field.name}-hint`;
  const error = editor.errors[field.name];
  const errorID = error ? `record-${field.name}-error` : undefined;
  const describedBy = [relationHintID, errorID].filter(Boolean).join(" ") || undefined;
  const target = editor.targetOf(field);

  return <div className="record-field">
    <label htmlFor={`record-${field.name}-state`}>{field.name} state</label>
    <select aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}-state`} onChange={(event) => editor.setKind(field, event.target.value as FieldState["kind"])} value={state.kind}>
      {editor.isCreate && <option value="unset">Use default / omit</option>}
      {!multipleRelation && (field.type !== "relation" || (relation?.minSelect ?? 0) === 0 || state.kind === "null") && <option value="null">NULL</option>}
      <option value="value">Value</option>
    </select>
    {relation !== undefined && <p id={relationHintID}>
      Target: {target?.name ?? "unknown collection"}. {multipleRelation
        ? `Enter one canonical UUID per line; order is preserved (${relation.minSelect}–${relation.maxSelect} selections).`
        : "Enter one canonical UUID."}
    </p>}
    {state.kind === "value" && <>
      <label htmlFor={`record-${field.name}`}>{field.name}</label>
      {field.type === "boolean" ? <input checked={state.value === true} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.checked)} type="checkbox" /> :
        field.type === "jsonb" || multipleRelation ? <textarea aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} onChange={(event) => editor.setValue(field, event.target.value)} rows={multipleRelation ? Math.min(8, Math.max(3, relation?.maxSelect ?? 3)) : 5} value={String(state.value)} /> :
          <input aria-describedby={describedBy} aria-invalid={error ? "true" : undefined} id={`record-${field.name}`} inputMode={["integer", "bigint", "numeric"].includes(field.type) ? "decimal" : undefined} maxLength={field.type === "text" && (field.indexed || field.unique) ? 2048 : undefined} onChange={(event) => editor.setValue(field, event.target.value)} value={String(state.value)} />}
      {relation !== undefined && target !== undefined && <RelationTargetPicker
        disabled={editor.pending || editor.stale}
        draft={String(state.value)}
        field={field}
        key={`${field.name}:${relation.targetCollectionId}`}
        onChange={(value) => editor.setValue(field, value)}
        target={target}
      />}
    </>}
    {error && <p className="field-error" id={`record-${field.name}-error`}>{error}</p>}
  </div>;
}

export function RecordEditPage({ collectionName, id, navigate }: { collectionName: string; id?: string; navigate(route: AdminRoute): void }) {
  const editor = useRecordEditor(collectionName, id, navigate);

  return <section>
    <h2>{editor.isCreate ? "Create" : "Edit"} {collectionName} record</h2>
    {editor.message && <p role="alert">{editor.message}</p>}
    {editor.stale && <button onClick={editor.reload} type="button">Reload record</button>}
    {!editor.collection && !editor.message && <p aria-live="polite">Loading record…</p>}
    {editor.collection && <form onSubmit={(event) => { event.preventDefault(); editor.save(); }}>
      {editor.fields.map((field) => <RecordField editor={editor} field={field} key={field.name} />)}
      <button disabled={editor.pending || editor.stale} type="submit">{editor.pending ? "Saving…" : editor.isCreate ? "Create record" : "Save changes"}</button>
      {!editor.isCreate && <button className="danger" disabled={editor.pending || editor.stale} onClick={editor.remove} type="button">Delete record</button>}
    </form>}
  </section>;
}
