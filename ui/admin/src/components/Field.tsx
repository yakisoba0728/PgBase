import { useId, type InputHTMLAttributes, type ReactNode, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { RiSearchLine, type RemixiconComponentType } from "@remixicon/react";

interface FieldBase {
  label: string;
  icon?: RemixiconComponentType;
  hint?: ReactNode;
  error?: string;
  required?: boolean;
  /** Muted text at the end of the label row (e.g. the field type). */
  aside?: ReactNode;
  /** Monospace control, for ids, numbers and JSON. */
  mono?: boolean;
}

type InputField = FieldBase & { kind?: "input" } & Omit<InputHTMLAttributes<HTMLInputElement>, "className">;
type TextareaField = FieldBase & { kind: "textarea" } & Omit<TextareaHTMLAttributes<HTMLTextAreaElement>, "className">;
type SelectField = FieldBase & { kind: "select"; options: { value: string; label: string }[] } & Omit<SelectHTMLAttributes<HTMLSelectElement>, "className">;

/** A label row above a filled control, with an optional hint or error below. */
export function Field(props: InputField | TextareaField | SelectField) {
  const { label, icon: Icon, hint, error, required, aside, mono, kind, id: ownId, ...control } = props as FieldBase & { kind?: string; id?: string; options?: SelectField["options"] };
  const autoId = useId();
  const id = ownId ?? autoId;
  const noteId = `${id}-note`;
  const shared = {
    id,
    required,
    className: mono ? "field-control mono" : "field-control",
    "aria-invalid": error ? true : undefined,
    "aria-describedby": error || hint ? noteId : undefined,
  };
  let input: ReactNode;
  if (kind === "textarea") {
    input = <textarea {...(control as TextareaHTMLAttributes<HTMLTextAreaElement>)} {...shared} />;
  } else if (kind === "select") {
    const { options = [], ...select } = control as SelectField;
    input = (
      <select {...select} {...shared}>
        {options.map((option) => (
          <option key={option.value} value={option.value}>{option.label}</option>
        ))}
      </select>
    );
  } else {
    input = <input {...(control as InputHTMLAttributes<HTMLInputElement>)} {...shared} />;
  }
  return (
    <div className="field-wrap">
      <div className="field" data-invalid={error ? "" : undefined}>
        <label className="field-label" htmlFor={id}>
          {Icon && <Icon size={14} aria-hidden />}
          {label}
          {required && <span className="field-required" aria-hidden>*</span>}
          {aside && <span className="mono">{aside}</span>}
        </label>
        {input}
      </div>
      {(error || hint) && (
        <p id={noteId} className={error ? "field-error" : "field-hint"} role={error ? "alert" : undefined}>
          {error ?? hint}
        </p>
      )}
    </div>
  );
}

type CheckProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "className"> & { label: ReactNode };

export function Checkbox({ label, ...rest }: CheckProps) {
  return (
    <label className="checkbox">
      <input type="checkbox" {...rest} />
      {label}
    </label>
  );
}

type SearchbarProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "className"> & { label: string; mono?: boolean };

export function Searchbar({ label, mono, ...rest }: SearchbarProps) {
  return (
    <div className={mono ? "searchbar mono" : "searchbar"}>
      <RiSearchLine size={14} aria-hidden />
      <input type="search" aria-label={label} placeholder={label} {...rest} />
    </div>
  );
}
