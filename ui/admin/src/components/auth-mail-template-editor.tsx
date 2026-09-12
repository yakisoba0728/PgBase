import { useState } from "react";
import type { AuthMailPreview, AuthMailTemplate } from "../backend/ports";

export function MailTemplateEditor({
  value,
  disabled,
  save,
  preview,
}: {
  value: AuthMailTemplate;
  disabled: boolean;
  save(value: AuthMailTemplate): void;
  preview(value: AuthMailTemplate): Promise<AuthMailPreview>;
}) {
  const [draft, setDraft] = useState(value);
  const [rendered, setRendered] = useState<AuthMailPreview | null>(null);
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
      <h4>{value.purpose.replace("_", " ")}</h4>
      <label>
        Subject{" "}
        <input
          value={draft.subject}
          onChange={(e) => setDraft({ ...draft, subject: e.target.value })}
        />
      </label>
      <label>
        Plain text alternative{" "}
        <textarea
          value={draft.body}
          onChange={(e) => setDraft({ ...draft, body: e.target.value })}
        />
      </label>
      <label>
        HTML body{" "}
        <textarea
          value={draft.htmlBody}
          onChange={(e) => setDraft({ ...draft, htmlBody: e.target.value })}
        />
      </label>
      <button disabled={disabled} type="button" onClick={runPreview}>
        Validate and preview
      </button>
      {previewError && <p role="alert">{previewError}</p>}
      {rendered && (
        <details open>
          <summary>{rendered.subject}</summary>
          <pre>{rendered.text}</pre>
          {rendered.html && (
            <iframe
              title={`${draft.purpose} email preview`}
              sandbox=""
              srcDoc={rendered.html}
            />
          )}
        </details>
      )}
      <button disabled={disabled} type="button" onClick={() => save(draft)}>
        Save template
      </button>
    </div>
  );
}
