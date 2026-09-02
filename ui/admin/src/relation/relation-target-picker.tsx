import type { CollectionDefinition, FieldDefinition } from "../backend/ports";
import { useRelationPicker } from "./relation-picker";

export function RelationTargetPicker({ disabled, draft, field, onChange, target }: {
  disabled: boolean;
  draft: string;
  field: FieldDefinition;
  onChange(value: string): void;
  target: CollectionDefinition;
}) {
  const picker = useRelationPicker(target, field, draft, onChange);
  const searchField = picker.searchField;
  const statusID = `record-${field.name}-target-search-status`;
  return <fieldset>
    <legend>{field.name} target lookup</legend>
    <label htmlFor={`record-${field.name}-target-search`}>
      {searchField === undefined ? `${field.name} target UUID` : `${field.name} target ${searchField.name}`}
    </label>
    <input
      aria-describedby={picker.status ? statusID : undefined}
      disabled={disabled || picker.searching}
      id={`record-${field.name}-target-search`}
      maxLength={1024}
      onChange={(event) => picker.setQuery(event.target.value)}
      onKeyDown={(event) => { if (event.key === "Enter") { event.preventDefault(); picker.search(); } }}
      placeholder={searchField === undefined ? "Canonical target UUID" : `Exact ${searchField.name}`}
      value={picker.query}
    />
    <button disabled={disabled || picker.searching} onClick={picker.search} type="button">
      {picker.searching ? "Searching…" : `Search ${field.name} targets`}
    </button>
    {picker.status && <p aria-live="polite" id={statusID}>{picker.status}</p>}
    {picker.results !== null && picker.results.length > 0 && <ul aria-label={`${field.name} target results`}>
      {picker.results.map((option) => <li key={option.id}>
        <button disabled={disabled} onClick={() => picker.select(option.id)} type="button">
          Select {option.label}{searchField === undefined || option.label === option.id ? "" : ` — ${option.id}`}
        </button>
      </li>)}
    </ul>}
  </fieldset>;
}
