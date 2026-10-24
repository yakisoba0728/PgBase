import { useCallback, useEffect, useRef, useState } from "react";
import { stringifyJSON } from "../backend/json";
import { useUnsavedDraft } from "../routing/unsaved-draft";
import { useBackend } from "../auth/session";
import type {
  AuthAdministration,
  AuthMailTemplate,
  AuthPolicy,
  AuthSMTPSettings,
} from "../backend/ports";
import { MailTemplateEditor } from "./auth-mail-template-editor";
import { PolicyEditor } from "./auth-policy-editor";
import { emptyProvider, ProviderEditor } from "./auth-provider-editor";
import { SMTPSettings } from "./auth-smtp-settings";

const purposes: AuthMailTemplate["purpose"][] = [
  "verify",
  "password_reset",
  "email_change",
  "otp",
];
const emptySMTP = (): AuthSMTPSettings => ({
  address: "",
  from: "",
  username: "",
  password: "",
  passwordConfigured: false,
  serverName: "",
  implicitTls: false,
  allowInsecureLoopback: false,
});

function templateFor(
  purpose: AuthMailTemplate["purpose"],
  templates: AuthMailTemplate[],
) {
  const saved = templates.find((template) => template.purpose === purpose);
  if (saved) return saved;
  return purpose === "otp"
    ? {
        purpose,
        subject: "Your login code",
        body: "Your one-time code is {{otp}}",
        htmlBody: "<p>Your one-time code is <strong>{{otp}}</strong></p>",
      }
    : {
        purpose,
        subject: "",
        body: "{{action_url}}",
        htmlBody: '<p><a class="button" href="{{action_url}}">Continue</a></p>',
      };
}

export function AuthAdministrationPanel({
  collectionId,
}: {
  collectionId: string;
}) {
  const { authCollections } = useBackend();
  const [value, setValue] = useState<AuthAdministration | null>(null);
  const [provider, setProvider] = useState(emptyProvider);
  const [smtp, setSMTP] = useState(emptySMTP);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [reloads, setReloads] = useState(0);
  const mutation = useRef<AbortController | null>(null);
  const mounted = useRef(true);
  const savedPolicy = useRef<AuthPolicy | null>(null);
  const [templateDrafts, setTemplateDrafts] = useState<Set<string>>(() => new Set());
  const onTemplateDirty = useCallback((purpose: string, dirty: boolean) => setTemplateDrafts(current => { if (current.has(purpose) === dirty) return current; const next = new Set(current); if (dirty) next.add(purpose); else next.delete(purpose); return next; }), []);
  const providerBaseline = value?.providers.find(item => item.name === provider.name);
  const dirty = stringifyJSON(provider) !== stringifyJSON(providerBaseline ? { ...providerBaseline, clientSecret: "" } : emptyProvider()) || value !== null && stringifyJSON(value.policy) !== stringifyJSON(savedPolicy.current) || stringifyJSON(smtp) !== stringifyJSON(value?.smtp ?? emptySMTP()) || templateDrafts.size > 0;
  useUnsavedDraft(`auth-administration:${collectionId}`, dirty);


  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
      mutation.current?.abort();
    };
  }, []);
  useEffect(() => {
    const controller = new AbortController();
    setValue(null);
    setMessage(null);
    setProvider(emptyProvider());
    const timeout = window.setTimeout(() => {
      controller.abort();
      setMessage("Authentication settings timed out. Retry to reload.");
    }, 10_000);
    void authCollections
      .getAdministration(collectionId, controller.signal)
      .then((next) => {
        if (controller.signal.aborted) return;
        savedPolicy.current = next.policy;
        setValue({
          ...next,
          providers: next.providers ?? [],
          templates: next.templates ?? [],
        });
        setSMTP(next.smtp ?? emptySMTP());
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setMessage("Unable to load authentication settings.");
      })
      .finally(() => window.clearTimeout(timeout));
    return () => {
      controller.abort();
      mutation.current?.abort();
      window.clearTimeout(timeout);
    };
  }, [authCollections, collectionId, reloads]);

  function run(work: (signal: AbortSignal) => Promise<void>, success: string) {
    if (mutation.current) return;
    const controller = new AbortController();
    mutation.current = controller;
    setBusy(true);
    setMessage(null);
    const timeout = window.setTimeout(() => {
      controller.abort();
      if (!mounted.current) return;
      setMessage(
        "Unable to confirm the change. Reload settings before retrying.",
      );
      setBusy(false);
      mutation.current = null;
    }, 10_000);
    void work(controller.signal)
      .then(() => {
        if (!controller.signal.aborted) setMessage(success);
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setMessage("Unable to save authentication settings.");
      })
      .finally(() => {
        window.clearTimeout(timeout);
        if (mutation.current === controller) {
          mutation.current = null;
          if (mounted.current) setBusy(false);
        }
      });
  }
  function savePolicy(policy: AuthPolicy) {
    run(async (signal) => {
      const saved = await authCollections.setPolicy(
        collectionId,
        policy,
        signal,
      );
      if (!signal.aborted) { savedPolicy.current = saved; setValue((current) => current && { ...current, policy: saved }); }
    }, "Policy saved. Existing sessions are revoked when password authentication is disabled or the minimum password length increases.");
  }
  function saveProvider() {
    run(async (signal) => {
      const saved = await authCollections.putProvider(
        collectionId,
        provider,
        signal,
      );
      if (signal.aborted) return;
      setValue(
        (current) =>
          current && {
            ...current,
            providers: [
              ...current.providers.filter((item) => item.name !== saved.name),
              saved,
            ],
          },
      );
      setProvider(emptyProvider());
    }, "OAuth provider saved.");
  }
  function removeProvider(name: string) {
    run(async (signal) => {
      await authCollections.deleteProvider(collectionId, name, signal);
      if (!signal.aborted)
        setValue(
          (current) =>
            current && {
              ...current,
              providers: current.providers.filter((item) => item.name !== name),
            },
        );
    }, "OAuth provider removed.");
  }
  function saveTemplate(template: AuthMailTemplate) {
    run(async (signal) => {
      const saved = await authCollections.putTemplate(
        collectionId,
        template,
        signal,
      );
      if (!signal.aborted)
        setValue(
          (current) =>
            current && {
              ...current,
              templates: [
                ...current.templates.filter(
                  (item) => item.purpose !== saved.purpose,
                ),
                saved,
              ],
            },
        );
    }, "Mail template saved.");
  }
  function saveSMTP() {
    run(async (signal) => {
      const saved = await authCollections.putSMTP(collectionId, smtp, signal);
      if (!signal.aborted) {
        setSMTP(saved);
        setValue((current) => current && { ...current, smtp: saved });
      }
    }, "SMTP settings saved. Stored passwords are never returned.");
  }
  function generateAppleSecret(
    input: {
      teamId: string;
      keyId: string;
      privateKey: string;
      duration: string;
    },
    reveal: (secret: string) => void,
  ) {
    if (!authCollections.generateAppleClientSecret) {
      setMessage("Apple client-secret generation is unavailable.");
      return;
    }
    run(async (signal) => {
      const generated = await authCollections.generateAppleClientSecret!(
        {
          clientId: provider.clientId,
          ...input,
          duration: Number(input.duration),
        },
        signal,
      );
      if (!signal.aborted) reveal(generated.secret);
    }, "Apple client secret generated. Save the Apple provider to store it.");
  }
  if (!value)
    return (
      <div>
        <p>{message ?? "Loading authentication settings…"}</p>
        {message && (
          <button
            type="button"
            onClick={() => setReloads((count) => count + 1)}
          >
            Retry authentication settings
          </button>
        )}
      </div>
    );
  return (
    <details className="auth-administration">
      <summary>Authentication settings</summary>
      {message && <p role="status">{message}</p>}
      <button
        type="button"
        disabled={busy || dirty}
        onClick={() => setReloads((count) => count + 1)}
      >
        Reload authentication settings
      </button>
      <PolicyEditor
        disabled={busy}
        value={value.policy}
        change={(policy) => setValue({ ...value, policy })}
        save={() => savePolicy(value.policy)}
      />
      <ProviderEditor
        disabled={busy}
        providers={value.providers}
        value={provider}
        change={setProvider}
        save={saveProvider}
        remove={removeProvider}
        generateAppleSecret={generateAppleSecret}
      />
      <fieldset disabled={busy}>
        <legend>Mail templates</legend>
        <p>
          Action templates use <code>{"{{action_url}}"}</code>. OTP templates
          use <code>{"{{otp}}"}</code> and may include{" "}
          <code>{"{{otp_id}}"}</code>.
        </p>
        {purposes.map((purpose) => {
          const template = templateFor(purpose, value.templates);
          return (
            <MailTemplateEditor
              key={JSON.stringify(template)}
              disabled={busy}
              onDirty={onTemplateDirty}
              value={template}
              save={saveTemplate}
              preview={(draft) =>
                authCollections.previewTemplate(collectionId, {
                  ...draft,
                  actionUrl: "https://app.example/auth/action",
                })
              }
            />
          );
        })}
      </fieldset>
      <SMTPSettings
        disabled={busy}
        value={smtp}
        change={setSMTP}
        save={saveSMTP}
        test={(recipient) =>
          run(
            (signal) =>
              authCollections.testSMTP(collectionId, recipient, signal),
            `Test message sent to ${recipient}.`,
          )
        }
      />
    </details>
  );
}
