import { t } from "../i18n";
import { useState, type KeyboardEvent } from "react";
import type { CollectionDefinition, FieldDefinition } from "../backend/ports";
import { useRelationPicker } from "./relation-picker";

/** Two targets may share a label, so an id disambiguates every name a screen
 *  reader announces and every name a test can address. */
function describeTarget(label: string, id: string): string {
  return label === id ? id : `${label} — ${id}`;
}

export function RelationTargetPicker({ disabled, draft, field, onChange, target }: {
  disabled: boolean;
  draft: string;
  field: FieldDefinition;
  onChange(value: string): void;
  target: CollectionDefinition;
}) {
  const picker = useRelationPicker(target, field, draft, onChange);
  const [newValue, setNewValue] = useState("");
  const [newRecordID, setNewRecordID] = useState("");
  const [edits, setEdits] = useState<Record<string, string>>({});
  const searchField = picker.searchField;
  const base = `record-${field.name}-target`;
  const statusID = `${base}-search-status`;
  const listID = `${base}-results`;
  const results = picker.results;
  const open = results !== null && results.length > 0;
  const active = open && picker.activeIndex >= 0 ? `${base}-option-${picker.activeIndex}` : undefined;

  // The listbox never takes focus: the textbox keeps it and points at the
  // active option, so arrow keys browse results without losing the query the
  // administrator is still editing. Home and End stay caret keys until the
  // list is actually being browsed.
  function navigate(event: KeyboardEvent<HTMLInputElement>): void {
    const browsing = picker.activeIndex >= 0;
    if (event.key === "ArrowDown") picker.moveActive("next");
    else if (event.key === "ArrowUp") picker.moveActive("previous");
    else if (event.key === "Home" && browsing) picker.moveActive("first");
    else if (event.key === "End" && browsing) picker.moveActive("last");
    else if (event.key === "Escape") picker.dismiss();
    else if (event.key === "Enter") { if (browsing) picker.chooseActive(); else picker.search(); }
    else return;
    event.preventDefault();
  }

  return <fieldset>
    <legend>{t("{field} target lookup", { field: field.name })}</legend>
    <label htmlFor={`${base}-search`}>
      {searchField === undefined ? t("{field} target {type}", { field: field.name, type: target.recordId?.kind === "text" ? t("Record ID") : "UUID" }) : t("{field} target {name}", { field: field.name, name: searchField.name })}
    </label>
    <input
      aria-activedescendant={active}
      aria-autocomplete="list"
      aria-controls={open ? listID : undefined}
      aria-describedby={picker.status ? statusID : undefined}
      aria-expanded={open}
      disabled={disabled || picker.searching}
      id={`${base}-search`}
      maxLength={1024}
      onChange={(event) => picker.setQuery(event.target.value)}
      onKeyDown={navigate}
      placeholder={searchField === undefined ? target.recordId?.kind === "text" ? t("Exact target record ID") : t("Canonical target UUID") : t("Exact {field}", { field: searchField.name })}
      role="combobox"
      value={picker.query}
    />
    <button disabled={disabled || picker.searching} onClick={picker.search} type="button">
      {picker.searching ? t("Searching…") : t("Search {field} targets", { field: field.name })}
    </button>
    {picker.status && <p aria-live="polite" id={statusID}>{picker.status}</p>}
    {open && <ul aria-label={t("{field} target results", { field: field.name })} className="target-results" id={listID} role="listbox">
      {results.map((option, index) => <li
        aria-selected={index === picker.activeIndex}
        className={index === picker.activeIndex ? "active-option" : undefined}
        id={`${base}-option-${index}`}
        key={option.id}
        onClick={() => { if (!disabled) picker.select(option.id); }}
        role="option"
      >{describeTarget(option.label, option.id)}</li>)}
    </ul>}
    {picker.hasMore && <button disabled={disabled || picker.loadingMore} onClick={picker.loadMore} type="button">
      {picker.loadingMore ? t("Loading more…") : t("Load more {field} targets", { field: field.name })}
    </button>}
    {searchField !== undefined && target.view === undefined && <details>
      <summary>{t("Create {collection} inline", { collection: target.name })}</summary>
      {target.recordId?.kind === "text" && <>
        <label htmlFor={`${base}-new-id`}>{t("New {collection} record ID", { collection: target.name })}</label>
        <input id={`${base}-new-id`} value={newRecordID} onChange={(event) => setNewRecordID(event.target.value)} placeholder={target.recordId.autogeneratePattern ? t("Leave empty to generate an ID") : t("Exact record ID (required)")} />
      </>}
      <label htmlFor={`${base}-new`}>{t("New {collection} {field}", { collection: target.name, field: searchField.name })}</label>
      <input id={`${base}-new`} value={newValue} onChange={(event) => setNewValue(event.target.value)} />
      <button disabled={disabled || picker.mutating || newValue.trim() === "" || (target.recordId?.kind === "text" && !target.recordId.autogeneratePattern && newRecordID === "")} onClick={() => { picker.createTarget(newValue, newRecordID); setNewValue(""); }} type="button">{t("Create and select")}</button>
    </details>}
    {picker.selections.length > 0 && <ul aria-label={t("{field} selected targets", { field: field.name })}>
      {picker.selections.map((selection) => <li key={selection.id}>
        {describeTarget(selection.label, selection.id)}{selection.available ? "" : t(" — unavailable")}
        <button disabled={disabled} onClick={() => picker.deselect(selection.id)} type="button">
          {t("Remove {target}", { target: describeTarget(selection.label, selection.id) })}
        </button>
        {searchField !== undefined && target.view === undefined && <details><summary>{t("Edit target inline")}</summary>
          <label htmlFor={`${base}-edit-${selection.id}`}>{searchField.name}</label>
          <input id={`${base}-edit-${selection.id}`} value={edits[selection.id] ?? selection.label} onChange={(event) => setEdits((current) => ({ ...current, [selection.id]: event.target.value }))} />
          <button disabled={disabled || picker.mutating} onClick={() => picker.editTarget(selection.id, edits[selection.id] ?? selection.label)} type="button">{t("Save target")}</button>
        </details>}
      </li>)}
    </ul>}
  </fieldset>;
}
