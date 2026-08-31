import { APIClientError } from "../api/client";
import {
  relationCardinalities,
  relationDeleteActions,
  type CollectionCatalog,
  type CollectionChangeInput,
  type CollectionDefinition,
  type FieldInput,
  type FieldType,
  type PlannedFieldDefinition,
  type RelationCardinality,
  type RelationDeleteAction,
} from "../api/collections";
import { parseJSON, stringifyJSON } from "../api/json";

export interface DraftRelation {
  targetCollection: string;
  cardinality: RelationCardinality;
  minSelect: string;
  maxSelect: string;
  onDelete: RelationDeleteAction;
}

export interface DraftField extends Omit<FieldInput, "default" | "relation"> {
  hasDefault: boolean;
  defaultText: string;
  relation?: DraftRelation;
}

export interface RelationTargetOption {
  name: string;
  label: string;
}

export class CollectionDraftError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "CollectionDraftError";
  }
}

const newDraftRelation = (): DraftRelation => ({
  targetCollection: "",
  cardinality: "single",
  minSelect: "0",
  maxSelect: "1",
  onDelete: "restrict",
});

export const newDraftField = (): DraftField => ({
  name: "",
  type: "text",
  required: false,
  unique: false,
  indexed: false,
  hasDefault: false,
  defaultText: "",
});

export function parseDefault(type: FieldType, value: string): unknown {
  if (type === "boolean") {
    if (value !== "true" && value !== "false") throw new CollectionDraftError("Enter true or false for the boolean default.");
    return value === "true";
  }
  if (type === "integer" || type === "bigint") {
    if (!/^-?[0-9]+$/.test(value)) throw new CollectionDraftError("Enter an integer default.");
    return value;
  }
  if (type === "numeric") {
    if (!/^-?[0-9]+(?:\.[0-9]+)?$/.test(value)) throw new CollectionDraftError("Enter a decimal default without exponent notation.");
    return value;
  }
  if (type === "timestamptz" && value === "now") return { kind: "now" };
  if (type === "jsonb") {
    try {
      return parseJSON(value);
    } catch {
      throw new CollectionDraftError("Enter valid JSON for the jsonb default.");
    }
  }
  return value;
}

function parseBound(value: string, fieldNumber: number, label: "minimum" | "maximum"): number {
  if (!/^(?:0|[1-9][0-9]*)$/.test(value)) {
    throw new CollectionDraftError(`Field ${fieldNumber} relation ${label} must be a non-negative integer.`);
  }
  return Number(value);
}

function relationInput(field: DraftField, index: number): FieldInput["relation"] {
  const relation = field.relation;
  const fieldNumber = index + 1;
  if (relation === undefined || relation.targetCollection === "") {
    throw new CollectionDraftError(`Choose a target collection for field ${fieldNumber}.`);
  }
  const minSelect = parseBound(relation.minSelect, fieldNumber, "minimum");
  const maxSelect = parseBound(relation.maxSelect, fieldNumber, "maximum");
  if (relation.cardinality === "single" && (minSelect > 1 || maxSelect !== 1)) {
    throw new CollectionDraftError(`Field ${fieldNumber} single relation must use minimum 0 or 1 and maximum 1.`);
  }
  if (relation.cardinality === "multiple" && (maxSelect < 2 || maxSelect > 1000 || minSelect > maxSelect)) {
    throw new CollectionDraftError(`Field ${fieldNumber} multiple relation must use maximum 2–1000 and minimum no greater than maximum.`);
  }
  if (field.required !== (minSelect > 0)) {
    throw new CollectionDraftError(`Field ${fieldNumber} required setting must match whether its relation minimum is greater than zero.`);
  }
  if (relation.onDelete === "set_null" && minSelect !== 0) {
    throw new CollectionDraftError(`Field ${fieldNumber} set-null relation must be optional.`);
  }
  if (relation.cardinality === "multiple" && field.unique) {
    throw new CollectionDraftError(`Field ${fieldNumber} multiple relation cannot be unique.`);
  }
  return {
    targetCollection: relation.targetCollection,
    cardinality: relation.cardinality,
    minSelect,
    maxSelect,
    onDelete: relation.onDelete,
  };
}

export function buildInput(operation: "create" | "update", name: string, fields: DraftField[]): CollectionChangeInput {
  return {
    operation,
    name,
    fields: fields.map((field, index) => {
      const base: FieldInput = {
        name: field.name,
        type: field.type,
        required: field.required,
        unique: field.unique,
        indexed: field.indexed,
      };
      if (field.type === "relation") return { ...base, relation: relationInput(field, index) };
      return field.hasDefault ? { ...base, default: parseDefault(field.type, field.defaultText) } : base;
    }),
  };
}

function defaultTextFor(type: string, value: unknown): string {
  if (value === undefined) return "";
  if (type === "timestamptz" && value !== null && typeof value === "object" && (value as { kind?: string }).kind === "now") return "now";
  if (type === "jsonb") return stringifyJSON(value);
  return typeof value === "string" ? value : stringifyJSON(value);
}

export function draftFieldsFromDefinition(definition: CollectionDefinition, catalog: CollectionCatalog): DraftField[] {
  const targetNames = new Map(
    Object.values(catalog.collections)
      .filter((collection): collection is CollectionDefinition & { id: string } => collection.id !== undefined)
      .map((collection) => [collection.id, collection.name]),
  );
  return definition.fields
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .map((field) => ({
      name: field.name,
      type: field.type,
      required: field.required,
      unique: field.unique,
      indexed: field.indexed,
      hasDefault: field.default !== undefined,
      defaultText: defaultTextFor(field.type, field.default),
      relation: field.relation === undefined ? undefined : {
        targetCollection: targetNames.get(field.relation.targetCollectionId) ?? "",
        cardinality: field.relation.cardinality,
        minSelect: String(field.relation.minSelect),
        maxSelect: String(field.relation.maxSelect),
        onDelete: field.relation.onDelete,
      },
    }));
}

export function relationTargetOptions(catalog: CollectionCatalog | null, sourceCollection: string): RelationTargetOption[] {
  const names = new Set(Object.values(catalog?.collections ?? {}).map((collection) => collection.name));
  if (sourceCollection !== "") names.add(sourceCollection);
  return [...names]
    .sort((left, right) => left.localeCompare(right))
    .map((name) => ({ name, label: name === sourceCollection ? `${name} (self)` : name }));
}

export function previewField(field: PlannedFieldDefinition): string {
  const flags = [
    field.required && "required",
    field.unique && "unique",
    field.indexed && "indexed",
    field.default !== undefined && `default: ${typeof field.default === "string" ? field.default : stringifyJSON(field.default)}`,
  ].filter(Boolean);
  if (field.relation !== undefined) {
    flags.push(
      `target: ${field.relation.targetCollection}`,
      field.relation.cardinality,
      `min: ${field.relation.minSelect}`,
      `max: ${field.relation.maxSelect}`,
      `on delete: ${field.relation.onDelete}`,
    );
  }
  return `${field.name} · ${field.type}${flags.length > 0 ? ` · ${flags.join(" · ")}` : ""}`;
}

export function draftErrorMessage(error: unknown, fallback: string): string {
  return error instanceof CollectionDraftError ? error.message : fallback;
}

export function schemaErrorMessage(error: unknown, fallback: string, validationFallback = "Collection input is invalid."): string {
  if (!(error instanceof APIClientError)) return fallback;
  const detailPrefix = error.code === "collection_referenced" ? "Collection is referenced." : validationFallback;
  const details = Object.entries(error.details)
    .flatMap(([path, codes]) => codes.map((code) => `${path}: ${code}`))
    .sort();
  if (details.length > 0) return `${detailPrefix} ${details.join("; ")}`;
  if (error.code === "validation_failed") return validationFallback;
  if (error.code !== "request_failed" && error.code !== "schema_conflict") return `${fallback} (${error.code})`;
  return fallback;
}

function fieldWithType(field: DraftField, type: FieldType): DraftField {
  if (type === "relation") {
    const relation = field.relation ?? newDraftRelation();
    return {
      ...field,
      type,
      indexed: true,
      hasDefault: false,
      defaultText: "",
      unique: relation.cardinality === "single" && field.unique,
      required: Number(relation.minSelect) > 0,
      relation,
    };
  }
  return { ...field, type, relation: undefined };
}

interface CollectionFieldEditorProps {
  field: DraftField;
  index: number;
  fieldCount: number;
  pending: boolean;
  relationTargets: RelationTargetOption[];
  relationTargetsLoading?: boolean;
  showRequiredRelationMigration?: boolean;
  fieldTypes: readonly FieldType[];
  onChange(field: DraftField): void;
  onRemove(): void;
}

export function CollectionFieldEditor({
  field,
  index,
  fieldCount,
  pending,
  relationTargets,
  relationTargetsLoading = false,
  showRequiredRelationMigration = false,
  fieldTypes,
  onChange,
  onRemove,
}: CollectionFieldEditorProps) {
  const fieldNumber = index + 1;
  const relation = field.relation;

  function changeRelation(patch: Partial<DraftRelation>) {
    if (relation === undefined) return;
    onChange({ ...field, relation: { ...relation, ...patch } });
  }

  function changeCardinality(cardinality: RelationCardinality) {
    if (relation === undefined) return;
    const minSelect = field.required ? "1" : "0";
    onChange({
      ...field,
      unique: cardinality === "single" && field.unique,
      relation: {
        ...relation,
        cardinality,
        minSelect,
        maxSelect: cardinality === "single" ? "1" : relation.cardinality === "multiple" ? relation.maxSelect : "20",
      },
    });
  }

  function changeRequired(required: boolean) {
    if (relation === undefined) {
      onChange({ ...field, required });
      return;
    }
    onChange({
      ...field,
      required,
      relation: {
        ...relation,
        minSelect: required ? (Number(relation.minSelect) > 0 ? relation.minSelect : "1") : "0",
      },
    });
  }

  function changeMinimum(minSelect: string) {
    if (relation === undefined) return;
    const parsed = /^(?:0|[1-9][0-9]*)$/.test(minSelect) ? Number(minSelect) : 0;
    onChange({ ...field, required: parsed > 0, relation: { ...relation, minSelect } });
  }

  function changeDeleteAction(onDelete: RelationDeleteAction) {
    if (relation === undefined) return;
    onChange({
      ...field,
      required: onDelete === "set_null" ? false : field.required,
      relation: { ...relation, onDelete, minSelect: onDelete === "set_null" ? "0" : relation.minSelect },
    });
  }

  const isRelation = field.type === "relation";
  const setNull = relation?.onDelete === "set_null";
  const multiple = relation?.cardinality === "multiple";

  return <div className="field-card">
    <label htmlFor={`field-${index}-name`}>Field {fieldNumber} name</label>
    <input id={`field-${index}-name`} onChange={(event) => onChange({ ...field, name: event.target.value })} required value={field.name} />
    <label htmlFor={`field-${index}-type`}>Field {fieldNumber} type</label>
    <select id={`field-${index}-type`} onChange={(event) => onChange(fieldWithType(field, event.target.value as FieldType))} value={field.type}>
      {fieldTypes.map((type) => <option key={type}>{type}</option>)}
    </select>

    {isRelation && relation !== undefined && <fieldset aria-label={`Field ${fieldNumber} relation settings`}>
      <legend>Relation</legend>
      <label htmlFor={`field-${index}-relation-target`}>Field {fieldNumber} relation target</label>
      <select
        disabled={relationTargetsLoading}
        id={`field-${index}-relation-target`}
        onChange={(event) => changeRelation({ targetCollection: event.target.value })}
        required
        value={relation.targetCollection}
      >
        <option value="">{relationTargetsLoading ? "Loading collections…" : "Choose a collection…"}</option>
        {relationTargets.map((target) => <option key={target.name} value={target.name}>{target.label}</option>)}
      </select>
      <label htmlFor={`field-${index}-relation-cardinality`}>Field {fieldNumber} relation cardinality</label>
      <select id={`field-${index}-relation-cardinality`} onChange={(event) => changeCardinality(event.target.value as RelationCardinality)} value={relation.cardinality}>
        {relationCardinalities.map((cardinality) => <option key={cardinality}>{cardinality}</option>)}
      </select>
      <label htmlFor={`field-${index}-relation-min`}>Field {fieldNumber} relation minimum</label>
      <input
        disabled={setNull}
        id={`field-${index}-relation-min`}
        inputMode="numeric"
        max={multiple ? 1000 : 1}
        min={0}
        onChange={(event) => changeMinimum(event.target.value)}
        required
        type="number"
        value={relation.minSelect}
      />
      <label htmlFor={`field-${index}-relation-max`}>Field {fieldNumber} relation maximum</label>
      <input
        disabled={!multiple}
        id={`field-${index}-relation-max`}
        inputMode="numeric"
        max={multiple ? 1000 : 1}
        min={multiple ? 2 : 1}
        onChange={(event) => changeRelation({ maxSelect: event.target.value })}
        required
        type="number"
        value={relation.maxSelect}
      />
      <label htmlFor={`field-${index}-relation-delete`}>Field {fieldNumber} relation delete action</label>
      <select id={`field-${index}-relation-delete`} onChange={(event) => changeDeleteAction(event.target.value as RelationDeleteAction)} value={relation.onDelete}>
        {relationDeleteActions.map((action) => <option key={action} value={action}>{action}</option>)}
      </select>
      <p>Restrict blocks target deletion, cascade deletes source records, and set_null clears this optional relation through record operations.</p>
      {showRequiredRelationMigration && field.required && <p role="note">New required relations use a staged migration: add the relation as optional, populate every record, then mark it required.</p>}
    </fieldset>}

    <label><input aria-label={`Field ${fieldNumber} has default`} checked={field.hasDefault} disabled={isRelation} onChange={(event) => onChange({ ...field, hasDefault: event.target.checked })} type="checkbox" /> Use default</label>
    <label htmlFor={`field-${index}-default`}>Field {fieldNumber} default</label>
    {field.type === "boolean" ? <select disabled={!field.hasDefault || isRelation} id={`field-${index}-default`} onChange={(event) => onChange({ ...field, defaultText: event.target.value })} value={field.defaultText}><option value="">Choose…</option><option value="true">true</option><option value="false">false</option></select> :
      <input disabled={!field.hasDefault || isRelation} id={`field-${index}-default`} onChange={(event) => onChange({ ...field, defaultText: event.target.value })} placeholder={field.type === "timestamptz" ? "now or RFC 3339 timestamp" : field.type === "jsonb" ? "Valid JSON" : undefined} value={field.defaultText} />}
    <div className="checks">
      <label><input aria-label={`Field ${fieldNumber} required`} checked={field.required} disabled={setNull} onChange={(event) => changeRequired(event.target.checked)} type="checkbox" /> required</label>
      <label><input aria-label={`Field ${fieldNumber} unique`} checked={field.unique} disabled={multiple} onChange={(event) => onChange({ ...field, unique: event.target.checked })} type="checkbox" /> unique</label>
      <label><input aria-label={`Field ${fieldNumber} indexed`} checked={field.indexed} disabled={isRelation} onChange={(event) => onChange({ ...field, indexed: event.target.checked })} type="checkbox" /> indexed</label>
    </div>
    {fieldCount > 1 && <button className="secondary" disabled={pending} onClick={onRemove} type="button">Remove field</button>}
  </div>;
}
