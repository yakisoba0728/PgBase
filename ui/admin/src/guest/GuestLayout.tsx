import type { ReactNode } from "react";
import { ApiError } from "../api/http";
import { LanguageMenu, ThemeMenu } from "../app/menus";
import { errorText } from "../app/queries";
import { Notice } from "../components/Feedback";
import { useT } from "../i18n";

/**
 * The one guest card (sign-in, reset, setup): wordmark and display controls on top, the step's title and lead,
 * its form, and the server it talks to underneath, on the hairline-grid canvas.
 */
export function GuestLayout({ title, lead, children }: { title: string; lead?: string; children: ReactNode }) {
  return (
    <div className="guest">
      <main className="guest-column">
        <header className="guest-head">
          <div className="guest-brand wordmark">
            <b>Pg</b>Base
          </div>
          <LanguageMenu />
          <ThemeMenu />
        </header>
        {/* Keyed so a step change (password to two-factor code) re-runs the heading's entrance. */}
        <div className="guest-heading" key={title}>
          <h1 className="guest-title">{title}</h1>
          {lead && <p className="guest-lead">{lead}</p>}
        </div>
        {children}
      </main>
      <p className="guest-host mono">{window.location.host}</p>
    </div>
  );
}

/** `credentialsKey` replaces the generic "wrong email or password" where the server's invalid_credentials means something else. */
export function FormError({ error, credentialsKey }: { error: unknown; credentialsKey?: "error.invalidSetupToken" | "error.invalidResetToken" | "error.invalidOtp" }) {
  const t = useT();
  if (!error) return null;
  const text = credentialsKey && error instanceof ApiError && error.code === "invalid_credentials" ? t(credentialsKey) : errorText(t, error);
  return <Notice tone="danger">{text}</Notice>;
}

/** Reads one text value from a submitted form. */
export function formValue(form: FormData, name: string): string {
  const value = form.get(name);
  return typeof value === "string" ? value : "";
}

/** Shared by setup and reset: a client-side check reported through the same error mapping. */
export function matchingPassword(form: FormData): string {
  const password = formValue(form, "password");
  if (password !== formValue(form, "passwordConfirm")) throw new ApiError(0, "password_mismatch", "passwords do not match");
  return password;
}
