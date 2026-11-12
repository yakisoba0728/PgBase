import type { ReactNode } from "react";
import { ApiError } from "../api/http";
import { LanguageMenu, ThemeMenu } from "../app/menus";
import { errorText } from "../app/queries";
import { useT } from "../i18n";

export function GuestLayout({ title, children }: { title: string; children: ReactNode }) {
  return (
    <div className="guest">
      <main className="guest-column">
        <div className="guest-brand wordmark">
          <b>Pg</b>Base
        </div>
        <h1 className="guest-title">{title}</h1>
        {children}
        <div className="guest-footer">
          <LanguageMenu />
          <ThemeMenu />
        </div>
      </main>
    </div>
  );
}

/** `credentialsKey` replaces the generic "wrong email or password" where the server's invalid_credentials means something else. */
export function FormError({ error, credentialsKey }: { error: unknown; credentialsKey?: "error.invalidSetupToken" | "error.invalidResetToken" | "error.invalidOtp" }) {
  const t = useT();
  if (!error) return null;
  const text = credentialsKey && error instanceof ApiError && error.code === "invalid_credentials" ? t(credentialsKey) : errorText(t, error);
  return <p className="field-error" role="alert">{text}</p>;
}

/** Reads one text value from a submitted form. */
export function formValue(form: FormData, name: string): string {
  const value = form.get(name);
  return typeof value === "string" ? value : "";
}
