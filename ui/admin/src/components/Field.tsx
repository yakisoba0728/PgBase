import { useId, useState, type InputHTMLAttributes, type KeyboardEvent, type ReactNode, type Ref, type SelectHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { RiEyeLine, RiEyeOffLine, RiSearchLine, type RemixiconComponentType } from "@remixicon/react";
import { useT } from "../i18n";

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
  } else if ((control as InputHTMLAttributes<HTMLInputElement>).type === "password") {
    return <PasswordField {...(props as InputField)} id={id} />;
  } else {
    input = <input {...(control as InputHTMLAttributes<HTMLInputElement>)} {...shared} />;
  }
  return (
    <FieldFrame id={id} label={label} icon={Icon} required={required} aside={aside} error={error} hint={hint}>
      {input}
    </FieldFrame>
  );
}

function FieldFrame({ id, label, icon: Icon, required, aside, error, hint, extra, children }: FieldBase & { id: string; extra?: ReactNode; children: ReactNode }) {
  const note = error ?? hint;
  return (
    <div className="field-wrap">
      <div className="field" data-invalid={error ? "" : undefined}>
        <label className="field-label" htmlFor={id}>
          {Icon && <Icon size={14} aria-hidden />}
          {label}
          {required && <span className="field-required" aria-hidden>*</span>}
          {aside && <span className="mono">{aside}</span>}
        </label>
        {children}
      </div>
      {note && (
        <p id={`${id}-note`} className={error ? "field-error" : "field-hint"} role={error ? "alert" : undefined}>
          {note}
        </p>
      )}
      {extra}
    </div>
  );
}

/** Password input: a reveal toggle, and a Caps Lock warning while it is on. */
function PasswordField(props: InputField & { id: string }) {
  const t = useT();
  const { label, icon, hint, error, required, aside, mono, kind: _kind, id, type: _type, onKeyDown, onKeyUp, onBlur, ...rest } = props;
  const [shown, setShown] = useState(false);
  const [caps, setCaps] = useState(false);
  const track = (event: KeyboardEvent<HTMLInputElement>) => setCaps(event.getModifierState?.("CapsLock") ?? false);
  return (
    <FieldFrame id={id} label={label} icon={icon} required={required} aside={aside} error={error} hint={hint}
      extra={caps && <p className="field-hint" role="status">{t("field.capsLock")}</p>}>
      <input
        {...rest}
        id={id}
        type={shown ? "text" : "password"}
        required={required}
        className={mono ? "field-control field-control-reveal mono" : "field-control field-control-reveal"}
        aria-invalid={error ? true : undefined}
        aria-describedby={error || hint ? `${id}-note` : undefined}
        onKeyDown={(event) => {
          track(event);
          onKeyDown?.(event);
        }}
        onKeyUp={(event) => {
          track(event);
          onKeyUp?.(event);
        }}
        onBlur={(event) => {
          setCaps(false);
          onBlur?.(event);
        }}
      />
      <button type="button" className="icon-btn field-reveal" aria-label={t("field.reveal")} title={t("field.reveal")} aria-pressed={shown} onClick={() => setShown(!shown)}>
        {shown ? <RiEyeOffLine size={16} aria-hidden /> : <RiEyeLine size={16} aria-hidden />}
      </button>
    </FieldFrame>
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

type SearchbarProps = Omit<InputHTMLAttributes<HTMLInputElement>, "className"> & {
  label: string;
  icon?: RemixiconComponentType;
  mono?: boolean;
  invalid?: boolean;
  inputRef?: Ref<HTMLInputElement>;
  /** The screen's main filter: "/" focuses it. */
  primary?: boolean;
  /** Trailing controls inside the pill (e.g. a help button). */
  children?: ReactNode;
};

/** The one search/filter pill for toolbars; the label doubles as the placeholder unless one is given. */
export function Searchbar({ label, icon: Icon = RiSearchLine, mono, invalid, inputRef, primary, children, type = "search", ...rest }: SearchbarProps) {
  return (
    <div className={mono ? "searchbar mono" : "searchbar"} data-invalid={invalid ? "" : undefined}>
      <Icon size={14} aria-hidden />
      <input ref={inputRef} type={type} aria-label={label} placeholder={label} aria-invalid={invalid || undefined} data-filter={primary ? "" : undefined} spellCheck={false} autoComplete="off" {...rest} />
      {children}
    </div>
  );
}
