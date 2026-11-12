import { t } from "../i18n";
import { FieldOptionsEditor } from "./field-options-editor";
import { fieldTypes, relationCardinalities, relationDeleteActions, type FieldType, type RelationCardinality, type RelationDeleteAction } from "../backend/ports";
import type { DraftEdit, DraftField, RelationTargetOption } from "../designer/collection-designer";

interface CollectionFieldEditorProps {
  idPrefix?: string;
  authProfile?: boolean;
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
  idPrefix = "field",
  authProfile = false,
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
    <div className="field-card-heading"><div><h3>{field.name || t("Field {number}", { number: fieldNumber })}</h3><span className="field-type-badge">{t(field.type)}{field.required ? ` · ${t("required")}` : ""}{locked ? ` · ${t("system")}` : ""}</span></div>{fieldCount > 1 && <button className="secondary" disabled={locked || pending} onClick={onRemove} type="button">{t("Remove field")}</button>}</div>
    {locked && <p>{t("System field. Its schema is managed by the auth collection.")}</p>}
    <div className="field-basics">
    <div className="form-control">
    <label htmlFor={`${idPrefix}-${index}-name`}>{t("Field {number} name", { number: fieldNumber })}</label>
		<input disabled={locked} id={`${idPrefix}-${index}-name`} onChange={(event) => onEdit({ set: "name", value: event.target.value })} required value={field.name} />
    </div>
    <div className="form-control">
    <label htmlFor={`${idPrefix}-${index}-type`}>{t("Field {number} type", { number: fieldNumber })}</label>
    <select disabled={locked} id={`${idPrefix}-${index}-type`} onChange={(event) => onEdit({ set: "type", value: event.target.value as FieldType })} value={field.type}>
      {fieldTypes.map((type) => <option key={type} value={type}>{t(type)}</option>)}
    </select>
    </div>
    </div>
    <div className="form-control">
		<label htmlFor={`${idPrefix}-${index}-help`}>{t("Field {number} help", { number: fieldNumber })}</label>
		<textarea disabled={locked} id={`${idPrefix}-${index}-help`} rows={2} maxLength={300} onChange={(event) => onEdit({ set: "help", value: event.target.value })} value={field.help ?? ""} />
    </div>

    {field.id && <p>{t("Renaming keeps this field’s identity and stored data.")}</p>}
    <FieldOptionsEditor idPrefix={idPrefix} field={field} index={index} onEdit={onEdit} />
    {isRelation && relation !== undefined && <fieldset aria-label={t("Field {number} relation settings", { number: fieldNumber })}>
      <legend>{t("Relation")}</legend>
      <label htmlFor={`${idPrefix}-${index}-relation-target`}>{t("Field {number} relation target", { number: fieldNumber })}</label>
      <select
        disabled={locked || relationTargetsLoading}
        id={`${idPrefix}-${index}-relation-target`}
        onChange={(event) => onEdit({ set: "target", value: event.target.value })}
        required
        value={relation.targetCollection}
      >
        <option value="">{relationTargetsLoading ? t("Loading collections…") : t("Choose a collection…")}</option>
        {relationTargets.map((target) => <option key={target.name} value={target.name}>{target.label === `${target.name} (self)` ? t("{name} (self)", { name: target.name }) : target.label}</option>)}
      </select>
      <label htmlFor={`${idPrefix}-${index}-relation-cardinality`}>{t("Field {number} relation cardinality", { number: fieldNumber })}</label>
      <select disabled={locked} id={`${idPrefix}-${index}-relation-cardinality`} onChange={(event) => onEdit({ set: "cardinality", value: event.target.value as RelationCardinality })} value={relation.cardinality}>
        {relationCardinalities.map((cardinality) => <option key={cardinality} value={cardinality}>{t(cardinality)}</option>)}
      </select>
      <label htmlFor={`${idPrefix}-${index}-relation-min`}>{t("Field {number} relation minimum", { number: fieldNumber })}</label>
      <input
        disabled={locked || setNull}
        id={`${idPrefix}-${index}-relation-min`}
        inputMode="numeric"
        max={multiple ? 1000 : 1}
        min={0}
        onChange={(event) => onEdit({ set: "minSelect", value: event.target.value })}
        required
        type="number"
        value={relation.minSelect}
      />
      <label htmlFor={`${idPrefix}-${index}-relation-max`}>{t("Field {number} relation maximum", { number: fieldNumber })}</label>
      <input
        disabled={locked || !multiple}
        id={`${idPrefix}-${index}-relation-max`}
        inputMode="numeric"
        max={multiple ? 1000 : 1}
        min={multiple ? 2 : 1}
        onChange={(event) => onEdit({ set: "maxSelect", value: event.target.value })}
        required
        type="number"
        value={relation.maxSelect}
      />
      <label htmlFor={`${idPrefix}-${index}-relation-delete`}>{t("Field {number} relation delete action", { number: fieldNumber })}</label>
      <select disabled={locked} id={`${idPrefix}-${index}-relation-delete`} onChange={(event) => onEdit({ set: "onDelete", value: event.target.value as RelationDeleteAction })} value={relation.onDelete}>
        {relationDeleteActions.map((action) => <option key={action} value={action}>{t(action)}</option>)}
      </select>
      <p>{t("Restrict blocks target deletion, cascade deletes source records, and set_null clears this optional relation through record operations.")}</p>
      {showRequiredRelationMigration && field.required && <p role="note">{t("New required relations use a staged migration: add the relation as optional, populate every record, then mark it required.")}</p>}
    </fieldset>}

    {!authProfile && <div className="field-default">
    <label><input aria-label={t("Field {number} has default", { number: fieldNumber })} checked={field.hasDefault} disabled={locked || isRelation || isFile || fixed} onChange={(event) => onEdit({ set: "hasDefault", value: event.target.checked })} type="checkbox" />  {t("Use default")}</label>
    <div className="form-control">
    <label htmlFor={`${idPrefix}-${index}-default`}>{t("Field {number} default", { number: fieldNumber })}</label>
    {field.type === "boolean" ? <select disabled={locked || !field.hasDefault || isRelation || isFile || fixed} id={`${idPrefix}-${index}-default`} onChange={(event) => onEdit({ set: "defaultText", value: event.target.value })} value={field.defaultText}><option value="">{t("Choose…")}</option><option value="true">{"true"}</option><option value="false">{"false"}</option></select> :
      <input disabled={locked || !field.hasDefault || isRelation || isFile || fixed} id={`${idPrefix}-${index}-default`} onChange={(event) => onEdit({ set: "defaultText", value: event.target.value })} placeholder={field.type === "timestamptz" ? t("now or RFC 3339 timestamp") : field.type === "jsonb" ? t("Valid JSON") : undefined} value={field.defaultText} />}
    </div>
    </div>}
    {fixed ? <p>{field.type === "autodate" ? t("Automatic dates cannot be required or have a custom default.") : field.type === "geoPoint" ? t("Required rejects the empty point (0, 0). Custom defaults are not supported.") : t("Required rejects an empty value. Custom defaults are not supported.")}</p> : <p>{t("Required rejects SQL NULL. False, zero and empty text remain valid unless constrained below. JSON null is a JSON value; relation minimums control required selections.")}</p>}
    <div className="checks">
      <label><input aria-label={t("Field {number} required", { number: fieldNumber })} checked={field.required} disabled={locked || setNull || field.type === "autodate"} onChange={(event) => onEdit({ set: "required", value: event.target.checked })} type="checkbox" />  {t("required")}</label>
      {!authProfile && <label><input aria-label={t("Field {number} unique", { number: fieldNumber })} checked={field.unique} disabled={locked || authProfile || multiple || noUnique} onChange={(event) => onEdit({ set: "unique", value: event.target.checked })} type="checkbox" />  {t("unique")}</label>}
      <label><input aria-label={t("Field {number} indexed", { number: fieldNumber })} checked={field.indexed} disabled={locked || isRelation || noIndex} onChange={(event) => onEdit({ set: "indexed", value: event.target.checked })} type="checkbox" />  {t("indexed")}</label>
			<label><input aria-label={t("Field {number} hidden", { number: fieldNumber })} checked={field.hidden ?? false} disabled={locked} onChange={(event) => onEdit({ set: "hidden", value: event.target.checked })} type="checkbox" />  {t("hidden")}</label>
			<label><input aria-label={t("Field {number} presentable", { number: fieldNumber })} checked={field.presentable ?? false} disabled={locked || field.hidden || field.type === "password"} onChange={(event) => onEdit({ set: "presentable", value: event.target.checked })} type="checkbox" />  {t("presentable")}</label>
    </div>
  </div>;
}
