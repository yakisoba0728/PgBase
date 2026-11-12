import { t, message as bilingual } from "../i18n";
import { useUnsavedDraft } from "../routing/unsaved-draft";
import { stringifyJSON } from "../backend/json";
import { useEffect, useId, useState } from "react";
import type { AuthMailPreview, AuthMailTemplate } from "../backend/ports";

export function MailTemplateEditor({
  value,
  disabled,
  save,
  preview,
  onDirty,
}: {
  value: AuthMailTemplate;
  disabled: boolean;
  onDirty?(purpose: string, dirty: boolean): void;
  save(value: AuthMailTemplate): void;
  preview(value: AuthMailTemplate): Promise<AuthMailPreview>;
}) {
  const [draft, setDraft] = useState(value);
  const [rendered, setRendered] = useState<AuthMailPreview | null>(null);
  const resource = useId();
  const dirty = stringifyJSON(draft) !== stringifyJSON(value);
  useUnsavedDraft(`mail-template:${resource}`, dirty);
  useEffect(() => { onDirty?.(value.purpose, dirty); return () => onDirty?.(value.purpose, false); }, [dirty, value.purpose, onDirty]);
  const [previewError, setPreviewError] = useState("");
  function runPreview() {
    setPreviewError("");
    void preview(draft)
      .then(setRendered)
      .catch(() =>
        setPreviewError("Template is invalid or could not be previewed."),
      );
  }
  return (
    <div>
      <h4>{t(({ verify: "Email verification", password_reset: "Password reset", email_change: "Email change", otp: "One-time password" })[value.purpose])}</h4>
      <label>{t("Subject")}{" "}
        <input
          value={draft.subject}
          onChange={(e) => setDraft({ ...draft, subject: e.target.value })}
        />
      </label>
      <label>{t("Plain text alternative")}{" "}
        <textarea
          value={draft.body}
          onChange={(e) => setDraft({ ...draft, body: e.target.value })}
        />
      </label>
      <label>{t("HTML body")}{" "}
        <textarea
          value={draft.htmlBody}
          onChange={(e) => setDraft({ ...draft, htmlBody: e.target.value })}
        />
      </label>
      <button disabled={disabled} type="button" onClick={runPreview}>{t("Validate and preview")}</button>
      {previewError && <p role="alert">{t(previewError)}</p>}
      {rendered && (
        <details open>
          <summary>{rendered.subject}</summary>
          <pre>{rendered.text}</pre>
          {rendered.html && (
            <iframe
              title={t(bilingual(`${draft.purpose} email preview`, `${draft.purpose} 이메일 미리 보기`))}
              sandbox=""
              srcDoc={rendered.html}
            />
          )}
        </details>
      )}
      <button disabled={disabled} type="button" onClick={() => save(draft)}>{t("Save template")}</button>
    </div>
  );
}
