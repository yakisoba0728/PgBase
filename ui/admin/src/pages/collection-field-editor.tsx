import { FieldOptionsEditor } from "./field-options-editor";
import { fieldTypes, relationCardinalities, relationDeleteActions, type FieldType, type RelationCardinality, type RelationDeleteAction } from "../backend/ports";
import type { DraftEdit, DraftField, RelationTargetOption } from "../designer/collection-designer";

interface CollectionFieldEditorProps {
  field: DraftField;
  index: number;
  fieldCount: number;
  pending: boolean;
  relationTargets: RelationTargetOption[];
  relationTargetsLoading: boolean;
  showRequiredRelationMigration: boolean;
  onEdit(edit: DraftEdit): void;
  onRemove(): void;
}

export function CollectionFieldEditor({
  field,
  index,
  fieldCount,
  pending,
  relationTargets,
  relationTargetsLoading,
  showRequiredRelationMigration,
  onEdit,
  onRemove,
}: CollectionFieldEditorProps) {
  const fieldNumber = index + 1;
  const relation = field.relation;
  const isRelation = field.type === "relation";
  const isFile = field.type === "file";
  const fixed = ["editor", "geoPoint", "autodate", "password"].includes(field.type);
  const noIndex = isFile || field.type === "password" || (field.type === "editor" && (Number(field.options?.maxSize) || 5 * 1024 * 1024) > 2048);
  const noUnique = noIndex || field.type === "geoPoint" || field.type === "autodate";
  const setNull = relation?.onDelete === "set_null";
  const multiple = relation?.cardinality === "multiple";
  const locked = field.system === true;

  return <div className="field-card">
    {locked && <p>System field. Its schema is managed by the auth collection.</p>}
    <label htmlFor={`field-${index}-name`}>Field {fieldNumber} name</label>
		<input disabled={locked} id={`field-${index}-name`} onChange={(event) => onEdit({ set: "name", value: event.target.value })} required value={field.name} />
		<label htmlFor={`field-${index}-help`}>Field {fieldNumber} help</label>
		<textarea disabled={locked} id={`field-${index}-help`} maxLength={300} onChange={(event) => onEdit({ set: "help", value: event.target.value })} value={field.help ?? ""} />
    <label htmlFor={`field-${index}-type`}>Field {fieldNumber} type</label>
    <select disabled={locked} id={`field-${index}-type`} onChange={(event) => onEdit({ set: "type", value: event.target.value as FieldType })} value={field.type}>
      {fieldTypes.map((type) => <option key={type}>{type}</option>)}
    </select>

    {field.id && <p>Renaming keeps this field’s identity and stored data.</p>}
    <FieldOptionsEditor field={field} index={index} onEdit={onEdit} />
    {isRelation && relation !== undefined && <fieldset aria-label={`Field ${fieldNumber} relation settings`}>
      <legend>Relation</legend>
      <label htmlFor={`field-${index}-relation-target`}>Field {fieldNumber} relation target</label>
      <select
        disabled={locked || relationTargetsLoading}
        id={`field-${index}-relation-target`}
        onChange={(event) => onEdit({ set: "target", value: event.target.value })}
        required
        value={relation.targetCollection}
      >
        <option value="">{relationTargetsLoading ? "Loading collections…" : "Choose a collection…"}</option>
        {relationTargets.map((target) => <option key={target.name} value={target.name}>{target.label}</option>)}
      </select>
      <label htmlFor={`field-${index}-relation-cardinality`}>Field {fieldNumber} relation cardinality</label>
      <select disabled={locked} id={`field-${index}-relation-cardinality`} onChange={(event) => onEdit({ set: "cardinality", value: event.target.value as RelationCardinality })} value={relation.cardinality}>
        {relationCardinalities.map((cardinality) => <option key={cardinality}>{cardinality}</option>)}
      </select>
      <label htmlFor={`field-${index}-relation-min`}>Field {fieldNumber} relation minimum</label>
      <input
        disabled={locked || setNull}
        id={`field-${index}-relation-min`}
        inputMode="numeric"
        max={multiple ? 1000 : 1}
        min={0}
        onChange={(event) => onEdit({ set: "minSelect", value: event.target.value })}
        required
        type="number"
        value={relation.minSelect}
      />
      <label htmlFor={`field-${index}-relation-max`}>Field {fieldNumber} relation maximum</label>
      <input
        disabled={locked || !multiple}
        id={`field-${index}-relation-max`}
        inputMode="numeric"
        max={multiple ? 1000 : 1}
        min={multiple ? 2 : 1}
        onChange={(event) => onEdit({ set: "maxSelect", value: event.target.value })}
        required
        type="number"
        value={relation.maxSelect}
      />
      <label htmlFor={`field-${index}-relation-delete`}>Field {fieldNumber} relation delete action</label>
      <select disabled={locked} id={`field-${index}-relation-delete`} onChange={(event) => onEdit({ set: "onDelete", value: event.target.value as RelationDeleteAction })} value={relation.onDelete}>
        {relationDeleteActions.map((action) => <option key={action} value={action}>{action}</option>)}
      </select>
      <p>Restrict blocks target deletion, cascade deletes source records, and set_null clears this optional relation through record operations.</p>
      {showRequiredRelationMigration && field.required && <p role="note">New required relations use a staged migration: add the relation as optional, populate every record, then mark it required.</p>}
    </fieldset>}

    <label><input aria-label={`Field ${fieldNumber} has default`} checked={field.hasDefault} disabled={locked || isRelation || isFile || fixed} onChange={(event) => onEdit({ set: "hasDefault", value: event.target.checked })} type="checkbox" /> Use default</label>
    <label htmlFor={`field-${index}-default`}>Field {fieldNumber} default</label>
    {field.type === "boolean" ? <select disabled={locked || !field.hasDefault || isRelation || isFile || fixed} id={`field-${index}-default`} onChange={(event) => onEdit({ set: "defaultText", value: event.target.value })} value={field.defaultText}><option value="">Choose…</option><option value="true">true</option><option value="false">false</option></select> :
      <input disabled={locked || !field.hasDefault || isRelation || isFile || fixed} id={`field-${index}-default`} onChange={(event) => onEdit({ set: "defaultText", value: event.target.value })} placeholder={field.type === "timestamptz" ? "now or RFC 3339 timestamp" : field.type === "jsonb" ? "Valid JSON" : undefined} value={field.defaultText} />}
    {fixed ? <p>{field.type === "autodate" ? "Automatic dates cannot be required or have a custom default." : field.type === "geoPoint" ? "Required rejects the empty point (0, 0). Custom defaults are not supported." : "Required rejects an empty value. Custom defaults are not supported."}</p> : <p>Required rejects SQL NULL. False, zero and empty text remain valid unless constrained below. JSON null is a JSON value; relation minimums control required selections.</p>}
    <div className="checks">
      <label><input aria-label={`Field ${fieldNumber} required`} checked={field.required} disabled={locked || setNull || field.type === "autodate"} onChange={(event) => onEdit({ set: "required", value: event.target.checked })} type="checkbox" /> required</label>
      <label><input aria-label={`Field ${fieldNumber} unique`} checked={field.unique} disabled={locked || multiple || noUnique} onChange={(event) => onEdit({ set: "unique", value: event.target.checked })} type="checkbox" /> unique</label>
      <label><input aria-label={`Field ${fieldNumber} indexed`} checked={field.indexed} disabled={locked || isRelation || noIndex} onChange={(event) => onEdit({ set: "indexed", value: event.target.checked })} type="checkbox" /> indexed</label>
			<label><input aria-label={`Field ${fieldNumber} hidden`} checked={field.hidden ?? false} disabled={locked} onChange={(event) => onEdit({ set: "hidden", value: event.target.checked })} type="checkbox" /> hidden</label>
			<label><input aria-label={`Field ${fieldNumber} presentable`} checked={field.presentable ?? false} disabled={locked || field.hidden || field.type === "password"} onChange={(event) => onEdit({ set: "presentable", value: event.target.checked })} type="checkbox" /> presentable</label>
    </div>
    {fieldCount > 1 && <button className="secondary" disabled={locked || pending} onClick={onRemove} type="button">Remove field</button>}
  </div>;
}
