import { useEffect, useState } from "react";
import { useBackend } from "../auth/session";
import type { AdministratorProfile } from "../backend/ports";

export function ProfileSettings() {
  const { profile: port } = useBackend();
  const [profile, setProfile] = useState<AdministratorProfile | null>(null);
  const [draft, setDraft] = useState({ email: "", prefixes: "" });
  const [message, setMessage] = useState("");
  useEffect(() => {
    if (!port) return;
    const controller = new AbortController();
    void Promise.all([
      port.get(controller.signal),
      port.ipAllowlist(controller.signal),
    ])
      .then(([next, ips]) => {
        if (!controller.signal.aborted) {
          setProfile(next);
          setDraft({ email: next.email, prefixes: ips.join("\n") });
        }
      })
      .catch(() => {
        if (!controller.signal.aborted)
          setMessage("Unable to load administrator profile.");
      });
    return () => controller.abort();
  }, [port]);
  if (!port || !profile) return null;
  return (
    <section aria-label="Administrator profile">
      <h3>Administrator profile</h3>
      {message && <p role="status">{message}</p>}
      <fieldset>
        <legend>Identity</legend>
        <label>
          Email
          <input
            type="email"
            value={draft.email}
            onChange={(event) =>
              setDraft((current) => ({ ...current, email: event.target.value }))
            }
          />
        </label>
        <button
          type="button"
          onClick={() =>
            void port
              .update({ email: draft.email })
              .then(setProfile)
              .then(() => setMessage("Administrator email updated."))
              .catch(() => setMessage("Unable to update administrator email."))
          }
        >
          Save administrator email
        </button>
      </fieldset>
      <fieldset>
        <legend>IP allowlist</legend>
        <p>
          One CIDR per line. When entries exist, this administrator can sign in
          only from these networks.
        </p>
        <label>
          Allowed CIDRs
          <textarea
            value={draft.prefixes}
            onChange={(event) =>
              setDraft((current) => ({
                ...current,
                prefixes: event.target.value,
              }))
            }
          />
        </label>
        <button
          type="button"
          onClick={() =>
            void port
              .setIPAllowlist(
                draft.prefixes
                  .split("\n")
                  .map((value) => value.trim())
                  .filter(Boolean),
              )
              .then((items) => {
                setDraft((current) => ({
                  ...current,
                  prefixes: items.join("\n"),
                }));
                setMessage("IP allowlist updated.");
              })
              .catch(() =>
                setMessage(
                  "Allowlist was rejected. Use canonical CIDR ranges.",
                ),
              )
          }
        >
          Save IP allowlist
        </button>
      </fieldset>
    </section>
  );
}
