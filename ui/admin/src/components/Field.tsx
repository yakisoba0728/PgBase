import { useId, useState, type ComponentProps, type InputHTMLAttributes, type KeyboardEvent, type ReactNode, type Ref } from "react";
import { RiEyeLine, RiEyeOffLine, RiSearchLine, type RemixiconComponentType } from "@remixicon/react";
import { useT } from "../i18n";

/**
 * The look every bordered control shares. Two heights only: `md` (--control, forms) and `sm` (--control-sm, toolbars
 * and dense rows, level with `Button size="sm"`). `className` is for layout (width, flex), never for restyling the box.
 */
interface ControlLook {
  size?: "md" | "sm";
  /** Monospace, for ids, numbers, operators and JSON. */
  mono?: boolean;
  invalid?: boolean;
  className?: string;
}

const controlClass = ({ size, mono, className }: ControlLook, base = "field-control") =>
  [base, size === "sm" && "control-sm", mono && "mono", className].filter(Boolean).join(" ");

type InputProps = Omit<ComponentProps<"input">, "size"> & ControlLook;
/** `rows` is the native `size` (visible rows of a multiple select); `size` is the control size, as everywhere. */
type SelectProps = Omit<ComponentProps<"select">, "size"> & ControlLook & { rows?: number };
/** One height only: it grows with its rows. */
type TextareaProps = ComponentProps<"textarea"> & Omit<ControlLook, "size">;

/** A bare text, number, date or datetime input; give it an `aria-label` (or use `Field` for a visible label). */
export function Input({ size, mono, invalid, className, ...rest }: InputProps) {
  return <input className={controlClass({ size, mono, className })} aria-invalid={invalid || undefined} {...rest} />;
}

/** A bare select with the shared chevron; options are its children. */
export function Select({ size, rows, mono, invalid, className, ...rest }: SelectProps) {
  return <select size={rows} className={controlClass({ size, mono, className })} aria-invalid={invalid || undefined} {...rest} />;
}

export function Textarea({ mono, invalid, className, ...rest }: TextareaProps) {
  return <textarea className={controlClass({ mono, className })} aria-invalid={invalid || undefined} {...rest} />;
}

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

type InputField = FieldBase & { kind?: "input" } & Omit<InputProps, "className" | "size" | "mono" | "invalid">;
type TextareaField = FieldBase & { kind: "textarea" } & Omit<TextareaProps, "className" | "size" | "mono" | "invalid">;
type SelectField = FieldBase & { kind: "select"; options: { value: string; label: string }[] } & Omit<SelectProps, "className" | "size" | "mono" | "invalid">;

/** A label row above one of the bare controls above, with an optional hint or error below. */
export function Field(props: InputField | TextareaField | SelectField) {
  const { label, icon: Icon, hint, error, required, aside, mono, kind, id: ownId, ...control } = props as FieldBase & { kind?: string; id?: string; options?: SelectField["options"] };
  const autoId = useId();
  const id = ownId ?? autoId;
  const noteId = `${id}-note`;
  const shared = { id, required, mono, invalid: Boolean(error), "aria-describedby": error || hint ? noteId : undefined };
  let input: ReactNode;
  if (kind === "textarea") {
    input = <Textarea {...(control as TextareaProps)} {...shared} />;
  } else if (kind === "select") {
    const { options = [], ...select } = control as SelectField;
    input = (
      <Select {...select} {...shared}>
        {options.map((option) => (
          <option key={option.value} value={option.value}>{option.label}</option>
        ))}
      </Select>
    );
  } else if ((control as InputProps).type === "password") {
    return <PasswordField {...(props as InputField)} id={id} />;
  } else {
    input = <Input {...(control as InputProps)} {...shared} />;
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
      <Input
        {...rest}
        id={id}
        type={shown ? "text" : "password"}
        required={required}
        mono={mono}
        className="field-control-reveal"
        invalid={Boolean(error)}
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

type CheckProps = Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "className"> & { label?: ReactNode };

/** A checkbox with its text beside it; without `label` the bare box (table cells), named by `aria-label`. */
export function Checkbox({ label, ...rest }: CheckProps) {
  if (label === undefined) return <input type="checkbox" {...rest} />;
  return (
    <label className="checkbox">
      <input type="checkbox" {...rest} />
      {label}
    </label>
  );
}

/** A box for a group of `total` items: checked when all are chosen, indeterminate when some are; a click picks all or none. */
export function GroupCheckbox({ label, chosen, total, onChange }: { label: string; chosen: number; total: number; onChange: (on: boolean) => void }) {
  const all = total > 0 && chosen === total;
  return (
    <input type="checkbox" aria-label={label} checked={all} disabled={total === 0}
      ref={(input) => { if (input) input.indeterminate = chosen > 0 && !all; }}
      onChange={() => onChange(!all)} />
  );
}

type SearchbarProps = Omit<InputHTMLAttributes<HTMLInputElement>, "className" | "size"> & {
  label: string;
  icon?: RemixiconComponentType;
  /** As for the other controls; toolbars use "sm". */
  size?: "md" | "sm";
  mono?: boolean;
  invalid?: boolean;
  inputRef?: Ref<HTMLInputElement>;
  /** The screen's main filter: "/" focuses it. */
  primary?: boolean;
  /** Trailing controls inside the pill (e.g. a help button). */
  children?: ReactNode;
};

/** The one search box (icon, input, trailing controls) in the shared control box; the label doubles as the placeholder unless one is given. */
export function Searchbar({ label, icon: Icon = RiSearchLine, size, mono, invalid, inputRef, primary, children, type = "search", ...rest }: SearchbarProps) {
  return (
    <div className={controlClass({ size, mono }, "searchbar")} data-invalid={invalid ? "" : undefined}>
      <Icon size={14} aria-hidden />
      <input ref={inputRef} type={type} aria-label={label} placeholder={label} aria-invalid={invalid || undefined} data-filter={primary ? "" : undefined} spellCheck={false} autoComplete="off" {...rest} />
      {children}
    </div>
  );
}
