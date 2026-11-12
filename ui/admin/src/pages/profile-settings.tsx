import { t, useLocale } from "../i18n";
import { useEffect, useRef, useState } from "react";
import { useBackend } from "../auth/session";
import type { AdministratorProfile } from "../backend/ports";
import { Button, FormSection } from "../components/ui";
import { useUnsavedDraft } from "../routing/unsaved-draft";

type ProfileActionStatus = {
  message: string;
  savingProfile: boolean;
  savingAllowlist: boolean;
};

export function ProfileSettings() {
  useLocale();
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
  const savedPrefixes = useRef("");
  useUnsavedDraft("administrator-profile", profile !== null &&
    (draft.email !== profile.email || draft.prefixes !== savedPrefixes.current));
  useEffect(() => {
    profileSave.current += 1; allowlistSave.current += 1;
    setProfile(null); setAction({ message: "", savingProfile: false, savingAllowlist: false });
    if (!port) return;
    const controller = new AbortController();
    void Promise.all([
      port.get(controller.signal),
      port.ipAllowlist(controller.signal),
    ])
      .then(([next, ips]) => {
        if (!controller.signal.aborted) {
          setProfile(next);
          savedPrefixes.current = ips.join("\n");
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
    return () => { controller.abort(); profileSave.current += 1; allowlistSave.current += 1; };
  }, [port]);
  if (!port) return null;
  if (!profile) return <section id="settings-profile" className="settings-profile" aria-label={t("Administrator profile")}>
    <h3>{t("Administrator profile")}</h3>
    <p role="status">{t(action.message || "Loading administrator profile…")}</p>
  </section>;
  return (
    <section id="settings-profile" className="settings-profile" aria-label={t("Administrator profile")}>
      <h3>{t("Administrator profile")}</h3>
      <p className="settings-help">{t("Your email and sign-in networks are saved separately from application settings.")}</p>
      {action.message && <p className="settings-feedback" role="status">{t(action.message)}</p>}
      <FormSection title={t("Identity")} className="settings-identity">
        <label>
          {t("Email")}
          <input
            disabled={action.savingProfile}
            type="email"
            value={draft.email}
            onChange={(event) =>
              setDraft((current) => ({ ...current, email: event.target.value }))
            }
          />
        </label>
        <Button
          className="settings-local-save"
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
                setDraft(current => ({ ...current, email: next.email }));
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
          {action.savingProfile ? t("Saving administrator email…") : t("Save administrator email")}
        </Button>
      </FormSection>
      <FormSection title={t("IP allowlist")}>
        <p id="settings-allowlist-help" className="settings-help">
          {t("One CIDR per line. When entries exist, this administrator can sign in only from these networks.")}
        </p>
        <label>
          {t("Allowed CIDRs")}
          <textarea
            rows={3}
            className="settings-code"
            autoCapitalize="none"
            spellCheck={false}
            aria-describedby="settings-allowlist-help"
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
        <Button
          className="settings-local-save"
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
                savedPrefixes.current = items.join("\n");
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
          {action.savingAllowlist ? t("Saving IP allowlist…") : t("Save IP allowlist")}
        </Button>
      </FormSection>
    </section>
  );
}
