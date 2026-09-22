import { parseJSON, stringifyJSON } from "../backend/json";
import {
  CollectionCatalog,
  SchemaError,
  type CollectionChange,
  type CollectionDefinition,
  type CompoundIndex,
  type FieldInput,
  type FieldType,
  type CollectionType,
  type PlannedField,
  type RelationCardinality,
  type RelationDeleteAction,
  type RecordIDPolicy,
  type ViewDefinition,
} from "../backend/ports";
import {
  fieldOptionsInput,
  draftFieldOptions,
  type DraftFieldOptions,
} from "./field-options";

interface DraftRelation {
  targetCollection: string;
  cardinality: RelationCardinality;
  minSelect: string;
  maxSelect: string;
  onDelete: RelationDeleteAction;
}

export interface DraftField {
  id?: string;
  options?: DraftFieldOptions;
  name: string;
  type: FieldType;
  required: boolean;
  unique: boolean;
  indexed: boolean;
  hidden?: boolean;
  presentable?: boolean;
  help?: string;
  system?: boolean;
  hasDefault: boolean;
  defaultText: string;
  relation?: DraftRelation;
  // Keeps an explicit self choice from being inferred from a half-typed name.
  selfRelation?: boolean;
}

export interface RelationTargetOption {
  name: string;
  label: string;
}

export type DraftEdit =
  | { set: "options"; value: DraftFieldOptions }
  | {
      set:
        | "name"
        | "defaultText"
        | "target"
        | "minSelect"
        | "maxSelect"
        | "help";
      value: string;
    }
  | {
      set:
        | "required"
        | "unique"
        | "indexed"
        | "hasDefault"
        | "hidden"
        | "presentable";
      value: boolean;
    }
  | { set: "type"; value: FieldType }
  | { set: "cardinality"; value: RelationCardinality }
  | { set: "onDelete"; value: RelationDeleteAction };

export type DesignerMode =
  | { kind: "create" }
  | { kind: "edit" | "duplicate"; collection: string }
  | { kind: "scaffold"; scaffold: "base" | "auth" | "view" };

export class DraftError extends Error {}

export const dataBlockedText =
  "Existing data blocks this change (for example missing or duplicate values).";
export const catalogMovedText =
  "The catalog changed. Reload this page before making another schema change.";
export const appliedText =
  "The change was applied. The edits you made after sending it are not saved yet — leave and reopen the collection before applying them.";
export const createdText =
  "The collection was created. The edits you made after sending it are not part of it and are not saved — open the collection from the list to apply them.";

const newRelation = (): DraftRelation => ({
  targetCollection: "",
  cardinality: "single",
  minSelect: "0",
  maxSelect: "1",
  onDelete: "restrict",
});
export const newField = (): DraftField => ({
  name: "",
  type: "text",
  required: false,
  unique: false,
  indexed: false,
  hidden: false,
  presentable: false,
  system: false,
  help: "",
  hasDefault: false,
  defaultText: "",
});

function parseDefault(type: FieldType, value: string): unknown {
  if (type === "boolean") {
    if (value !== "true" && value !== "false")
      throw new DraftError("Enter true or false for the boolean default.");
    return value === "true";
  }
  if (type === "integer" || type === "bigint") {
    if (!/^-?[0-9]+$/.test(value))
      throw new DraftError("Enter an integer default.");
    return value;
  }
  if (type === "numeric") {
    if (!/^-?[0-9]+(?:\.[0-9]+)?$/.test(value))
      throw new DraftError(
        "Enter a decimal default without exponent notation.",
      );
    return value;
  }
  if (type === "timestamptz" && value === "now") return { kind: "now" };
  if (type === "jsonb") {
    try {
      return parseJSON(value);
    } catch {
      throw new DraftError("Enter valid JSON for the jsonb default.");
    }
  }
  return value;
}

function parseBound(
  value: string,
  fieldNumber: number,
  label: "minimum" | "maximum",
): number {
  if (!/^(?:0|[1-9][0-9]*)$/.test(value))
    throw new DraftError(
      `Field ${fieldNumber} relation ${label} must be a non-negative integer.`,
    );
  return Number(value);
}

function relationInput(
  field: DraftField,
  index: number,
): FieldInput["relation"] {
  const relation = field.relation;
  const fieldNumber = index + 1;
  if (relation === undefined || relation.targetCollection === "") {
    throw new DraftError(
      `Choose a target collection for field ${fieldNumber}.`,
    );
  }
  const minSelect = parseBound(relation.minSelect, fieldNumber, "minimum");
  const maxSelect = parseBound(relation.maxSelect, fieldNumber, "maximum");
  if (relation.cardinality === "single" && (minSelect > 1 || maxSelect !== 1)) {
    throw new DraftError(
      `Field ${fieldNumber} single relation must use minimum 0 or 1 and maximum 1.`,
    );
  }
  if (
    relation.cardinality === "multiple" &&
    (maxSelect < 2 || maxSelect > 1000 || minSelect > maxSelect)
  ) {
    throw new DraftError(
      `Field ${fieldNumber} multiple relation must use maximum 2–1000 and minimum no greater than maximum.`,
    );
  }
  if (field.required !== minSelect > 0) {
    throw new DraftError(
      `Field ${fieldNumber} required setting must match whether its relation minimum is greater than zero.`,
    );
  }
  if (relation.onDelete === "set_null" && minSelect !== 0) {
    throw new DraftError(
      `Field ${fieldNumber} set-null relation must be optional.`,
    );
  }
  if (relation.cardinality === "multiple" && field.unique) {
    throw new DraftError(
      `Field ${fieldNumber} multiple relation cannot be unique.`,
    );
  }
  return {
    targetCollection: relation.targetCollection,
    cardinality: relation.cardinality,
    minSelect,
    maxSelect,
    onDelete: relation.onDelete,
  };
}

export function buildInput(
  operation: "create" | "update",
  name: string,
  fields: DraftField[],
  indexes?: CompoundIndex[],
  view?: ViewDefinition,
  type?: CollectionType,
  recordId?: RecordIDPolicy,
): CollectionChange {
  if (indexes !== undefined) {
    for (const index of indexes) {
      if (
        index.name.trim() === "" ||
        index.fields.length < 2 ||
        index.fields.length > 8
      )
        throw new DraftError(
          "Each compound index needs a name and two to eight fields.",
        );
      const known = new Set(
        fields
          .filter(
            (field) =>
              !["relation", "file", "jsonb", "geoPoint", "password"].includes(
                field.type,
              ) &&
              (field.type !== "editor" ||
                (Number(field.options?.maxSize) || 5 * 1024 * 1024) <= 2048),
          )
          .map((field) => field.name),
      );
      if (
        new Set(index.fields).size !== index.fields.length ||
        index.fields.some((field) => !known.has(field))
      )
        throw new DraftError(
          "Compound indexes can use each scalar field once.",
        );
    }
  }
  if (view !== undefined) {
    if (operation === "update") {
      return { operation, name, view };
    }
    return { operation, name, fields: [], view };
  }
  const fieldInputs = fields.map((field, index) => {
    if (Array.from(field.help ?? "").length > 300)
      throw new DraftError(
        `Field ${index + 1} help must be at most 300 characters.`,
      );
    if (field.hidden && field.presentable)
      throw new DraftError(
        `Field ${index + 1} cannot be hidden and presentable.`,
      );
    const base: FieldInput = {
      name: field.name,
      type: field.type,
      required: field.required,
      unique: field.unique,
      indexed: field.indexed,
    };
    if (field.system) base.system = true;
    if (field.hidden) base.hidden = true;
    if (field.presentable) base.presentable = true;
    if (field.help) base.help = field.help;
    if (operation === "update" && field.id) base.id = field.id;
    const options =
      fieldOptionsInput(field.options) ?? (field.id ? {} : undefined);
    if (options !== undefined) base.options = options;
    if (field.type === "relation")
      return { ...base, relation: relationInput(field, index) };
    return field.hasDefault
      ? { ...base, default: parseDefault(field.type, field.defaultText) }
      : base;
  });
  return {
    operation,
    name,
    fields: fieldInputs,
    ...(indexes === undefined ? {} : { indexes }),
    ...(type === undefined ? {} : { type }),
    ...(recordId === undefined ? {} : { recordId }),
  };
}

export function previewField(field: PlannedField): string {
  const flags = [
    field.required && "required",
    field.unique && "unique",
    field.indexed && "indexed",
    field.default !== undefined &&
      `default: ${typeof field.default === "string" ? field.default : stringifyJSON(field.default)}`,
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
  if (field.options && Object.keys(field.options).length)
    flags.push(`validation: ${JSON.stringify(field.options)}`);
  return `${field.name} · ${field.type}${flags.length > 0 ? ` · ${flags.join(" · ")}` : ""}`;
}

function defaultTextFor(type: string, value: unknown): string {
  if (value === undefined) return "";
  if (
    type === "timestamptz" &&
    value !== null &&
    typeof value === "object" &&
    (value as { kind?: string }).kind === "now"
  )
    return "now";
  if (type === "jsonb") return stringifyJSON(value);
  return typeof value === "string" ? value : stringifyJSON(value);
}

export function draftFields(
  definition: CollectionDefinition,
  catalog: CollectionCatalog,
): DraftField[] {
  const names = new Map(
    catalog
      .all()
      .filter((entry) => entry.id !== undefined)
      .map((entry) => [entry.id as string, entry.name]),
  );
  return definition.fields
    .slice()
    .sort((left, right) => left.ordinal - right.ordinal)
    .map((field) => ({
      id: field.id,
      options: draftFieldOptions(field.options),
      name: field.name,
      type: field.type,
      required: field.required,
      unique: field.unique,
      indexed: field.indexed,
      hidden: field.hidden,
      presentable: field.presentable,
      help: field.help,
      system: field.system,
      hasDefault:
        field.default !== undefined &&
        !["editor", "geoPoint", "autodate", "password"].includes(field.type),
      defaultText: defaultTextFor(field.type, field.default),
      relation:
        field.relation === undefined
          ? undefined
          : {
              targetCollection:
                names.get(field.relation.targetCollectionId) ?? "",
              cardinality: field.relation.cardinality,
              minSelect: String(field.relation.minSelect),
              maxSelect: String(field.relation.maxSelect),
              onDelete: field.relation.onDelete,
            },
    }));
}

export function scaffoldFields(fields: FieldInput[] | undefined): DraftField[] {
  if (fields === undefined || fields.length === 0) return [newField()];
  return fields.map((field) => ({
    options: draftFieldOptions(field.options),
    name: field.name,
    type: field.type,
    required: field.required,
    unique: field.unique,
    indexed: field.indexed,
    hidden: field.hidden,
    presentable: field.presentable,
    system: field.system,
    help: field.help,
    hasDefault:
      field.default !== undefined &&
      !["editor", "geoPoint", "autodate", "password"].includes(field.type),
    defaultText: defaultTextFor(field.type, field.default),
    relation:
      field.relation === undefined
        ? undefined
        : {
            targetCollection: field.relation.targetCollection,
            cardinality: field.relation.cardinality,
            minSelect: String(field.relation.minSelect),
            maxSelect: String(field.relation.maxSelect),
            onDelete: field.relation.onDelete,
          },
  }));
}

function withType(field: DraftField, type: FieldType): DraftField {
  if (type !== "relation") {
    const fixed = [
      "file",
      "editor",
      "geoPoint",
      "autodate",
      "password",
    ].includes(type);
    return {
      ...field,
      type,
      options:
        type === "autodate"
          ? { onCreate: "true", onUpdate: "true" }
          : undefined,
      relation: undefined,
      selfRelation: false,
      ...(fixed
        ? { hasDefault: false, defaultText: "", unique: false, indexed: false }
        : {}),
      ...(type === "autodate" ? { required: false } : {}),
      ...(type === "password" ? { presentable: false } : {}),
    };
  }
  const relation = field.relation ?? newRelation();
  return {
    ...field,
    type,
    options: undefined,
    indexed: true,
    hasDefault: false,
    defaultText: "",
    selfRelation: false,
    unique: relation.cardinality === "single" && field.unique,
    required: Number(relation.minSelect) > 0,
    relation,
  };
}

export function applyEdit(
  field: DraftField,
  edit: DraftEdit,
  collectionName: string,
): DraftField {
  if (field.system) return field;
  const relation = field.relation;
  if (edit.set === "options")
    return {
      ...field,
      options: edit.value,
      ...(field.type === "editor" &&
      (Number(edit.value?.maxSize) || 5 * 1024 * 1024) > 2048
        ? { indexed: false, unique: false }
        : {}),
    };
  if (edit.set === "name") return { ...field, name: edit.value };
  if (edit.set === "defaultText") return { ...field, defaultText: edit.value };
  if (edit.set === "hasDefault") return { ...field, hasDefault: edit.value };
  if (edit.set === "unique") return { ...field, unique: edit.value };
  if (edit.set === "indexed") return { ...field, indexed: edit.value };
  if (edit.set === "help") return { ...field, help: edit.value };
  if (edit.set === "hidden")
    return {
      ...field,
      hidden: edit.value,
      presentable: edit.value ? false : field.presentable,
    };
  if (edit.set === "presentable") return { ...field, presentable: edit.value };
  if (edit.set === "type") return withType(field, edit.value);
  if (edit.set === "required") {
    if (relation === undefined) return { ...field, required: edit.value };
    return {
      ...field,
      required: edit.value,
      relation: {
        ...relation,
        minSelect: edit.value
          ? Number(relation.minSelect) > 0
            ? relation.minSelect
            : "1"
          : "0",
      },
    };
  }
  if (relation === undefined) return field;
  if (edit.set === "target") {
    return {
      ...field,
      selfRelation: edit.value !== "" && edit.value === collectionName,
      relation: { ...relation, targetCollection: edit.value },
    };
  }
  if (edit.set === "maxSelect")
    return { ...field, relation: { ...relation, maxSelect: edit.value } };
  if (edit.set === "minSelect") {
    return {
      ...field,
      required:
        /^(?:0|[1-9][0-9]*)$/.test(edit.value) && Number(edit.value) > 0,
      relation: { ...relation, minSelect: edit.value },
    };
  }
  if (edit.set === "cardinality") {
    return {
      ...field,
      unique: edit.value === "single" && field.unique,
      relation: {
        ...relation,
        cardinality: edit.value,
        minSelect: field.required ? "1" : "0",
        maxSelect:
          edit.value === "single"
            ? "1"
            : relation.cardinality === "multiple"
              ? relation.maxSelect
              : "20",
      },
    };
  }
  if (edit.set === "onDelete") {
    return {
      ...field,
      required: edit.value === "set_null" ? false : field.required,
      relation: {
        ...relation,
        onDelete: edit.value,
        minSelect: edit.value === "set_null" ? "0" : relation.minSelect,
      },
    };
  }
  return field;
}

export function relationTargets(
  catalog: CollectionCatalog | null,
  sourceCollection: string,
): RelationTargetOption[] {
  const names = new Set(
    (catalog?.all() ?? []).map((collection) => collection.name),
  );
  if (sourceCollection !== "") names.add(sourceCollection);
  return [...names]
    .sort((left, right) => left.localeCompare(right))
    .map((name) => ({
      name,
      label: name === sourceCollection ? `${name} (self)` : name,
    }));
}

export function draftMessage(error: unknown, fallback: string): string {
  return error instanceof Error ? error.message : fallback;
}

export function schemaMessage(
  error: unknown,
  fallback: string,
  validationFallback = "Collection input is invalid.",
): string {
  if (!(error instanceof SchemaError)) return fallback;
  const detailPrefix =
    error.code === "collection_referenced"
      ? "Collection is referenced."
      : validationFallback;
  const details = Object.entries(error.details)
    .flatMap(([path, codes]) => codes.map((code) => `${path}: ${code}`))
    .sort();
  if (details.length > 0) return `${detailPrefix} ${details.join("; ")}`;
  if (error.code === "validation_failed") return validationFallback;
  if (error.code !== "request_failed" && error.code !== "schema_conflict")
    return `${fallback} (${error.code})`;
  return fallback;
}

// The whole last-agreed draft distinguishes an untouched form.
export const draftSignature = (
  name: string,
  fields: DraftField[],
  indexes?: CompoundIndex[],
  view?: ViewDefinition,
  type?: CollectionType,
  recordId?: RecordIDPolicy,
): string => stringifyJSON({ name, fields, indexes, view, type, recordId });
