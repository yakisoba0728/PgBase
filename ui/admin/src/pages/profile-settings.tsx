import { useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { AdministratorProfile } from "../backend/ports";

type ProfileActionStatus = {
  message: string;
  savingProfile: boolean;
  savingAllowlist: boolean;
};

export function ProfileSettings() {
  const { profile: port } = useBackend();
  const [profile, setProfile] = useState<AdministratorProfile | null>(null);
  const [draft, setDraft] = useState({ email: "", prefixes: "" });
  const [action, setAction] = useState<ProfileActionStatus>({
    message: "",
    savingProfile: false,
    savingAllowlist: false,
  });
  const profileSave = useRef(0);
  const allowlistSave = useRef(0);
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
          setAction((current) => ({
            ...current,
            message: "Unable to load administrator profile.",
          }));
      });
    return () => controller.abort();
  }, [port]);
  if (!port || !profile) return null;
  return (
    <section aria-label="Administrator profile">
      <h3>Administrator profile</h3>
      {action.message && <p role="status">{action.message}</p>}
      <fieldset>
        <legend>Identity</legend>
        <label>
          Email
          <input
            disabled={action.savingProfile}
            type="email"
            value={draft.email}
            onChange={(event) =>
              setDraft((current) => ({ ...current, email: event.target.value }))
            }
          />
        </label>
        <button
          disabled={action.savingProfile}
          type="button"
          onClick={() => {
            const revision = profileSave.current + 1;
            profileSave.current = revision;
            setAction((current) => ({ ...current, savingProfile: true }));
            void port
              .update({ email: draft.email })
              .then((next) => {
                if (revision !== profileSave.current) return;
                setProfile(next);
                setAction((current) => ({
                  ...current,
                  message: "Administrator email updated.",
                }));
              })
              .catch(() => {
                if (revision !== profileSave.current) return;
                setAction((current) => ({
                  ...current,
                  message: "Unable to update administrator email.",
                }));
              })
              .finally(() => {
                if (revision === profileSave.current)
                  setAction((current) => ({ ...current, savingProfile: false }));
              });
          }}
        >
          {action.savingProfile ? "Saving administrator email…" : "Save administrator email"}
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
            disabled={action.savingAllowlist}
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
          disabled={action.savingAllowlist}
          type="button"
          onClick={() => {
            const revision = allowlistSave.current + 1;
            allowlistSave.current = revision;
            setAction((current) => ({ ...current, savingAllowlist: true }));
            void port
              .setIPAllowlist(
                draft.prefixes
                  .split("\n")
                  .map((value) => value.trim())
                  .filter(Boolean),
              )
              .then((items) => {
                if (revision !== allowlistSave.current) return;
                setDraft((current) => ({
                  ...current,
                  prefixes: items.join("\n"),
                }));
                setAction((current) => ({
                  ...current,
                  message: "IP allowlist updated.",
                }));
              })
              .catch(() => {
                if (revision !== allowlistSave.current) return;
                setAction((current) => ({
                  ...current,
                  message: "Allowlist was rejected. Use canonical CIDR ranges.",
                }));
              })
              .finally(() => {
                if (revision === allowlistSave.current)
                  setAction((current) => ({ ...current, savingAllowlist: false }));
              });
          }}
        >
          {action.savingAllowlist ? "Saving IP allowlist…" : "Save IP allowlist"}
        </button>
      </fieldset>
    </section>
  );
}
