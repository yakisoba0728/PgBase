import { useId, type ReactNode } from "react";
import "../styles/kit.css";

/** A titled group of settings: title and hint on the left, controls on the right. */
export function FormSection({ title, hint, children }: { title: string; hint?: ReactNode; children: ReactNode }) {
  const id = useId();
  return (
    <section className="form-section" aria-labelledby={id}>
      <header>
        <h2 id={id}>{title}</h2>
        {hint && <p className="txt-hint">{hint}</p>}
      </header>
      {children}
    </section>
  );
}
