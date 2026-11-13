import { useState, type FormEvent } from "react";
import { useMutation } from "@tanstack/react-query";
import { RiAppleLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { generateAppleClientSecret } from "../api/system";
import { errorText } from "../app/queries";
import { Button } from "../components/Button";
import { Field } from "../components/Field";
import { useT } from "../i18n";
import { SecretView, TaskDialog } from "../components/Dialog";

/** Apple allows about six months (15,777,000 s); whole days keep the input simple. */
const maxDays = 182;

/**
 * Signs an Apple client secret. With onGenerated the secret goes straight into the caller's
 * form (never displayed); otherwise it is shown once for copying.
 */
export function AppleSecretButton({ clientId, onGenerated }: { clientId?: string; onGenerated?: (secret: string) => void }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const generate = useMutation({
    mutationFn: generateAppleClientSecret,
    onSuccess: (value, input) => {
      if (!onGenerated) return;
      if (input.clientId === clientId) onGenerated(value.secret);
      setOpen(false);
      generate.reset();
    },
  });
  // Drops the key and the generated secret with the dialog.
  const close = () => {
    if (generate.isPending) return;
    setOpen(false);
    generate.reset();
  };
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const text = (name: string) => String(form.get(name) ?? "").trim();
    generate.mutate({
      clientId: text("clientId"),
      teamId: text("teamId"),
      keyId: text("keyId"),
      privateKey: String(form.get("privateKey") ?? ""),
      duration: Math.round(Number(form.get("days"))) * 86400,
    });
  };
  return (
    <>
      <Button variant="secondary" icon={RiAppleLine} onClick={() => setOpen(true)}>{t("system.apple.open")}</Button>
      <TaskDialog open={open} title={t("system.apple.title")} onClose={close}>
        {generate.data && !onGenerated ? (
          <SecretView label={t("system.apple.secret")} value={generate.data.secret} warning={t("system.apple.once")} onDone={close} />
        ) : (
          <form className="form" onSubmit={onSubmit}><fieldset disabled={generate.isPending} className="form">
            <p className="txt-hint">{t("system.apple.hint")}</p>
            {generate.isError && (
              <p className="field-error" role="alert">
                {generate.error instanceof ApiError && generate.error.status === 400 ? t("system.apple.invalid") : errorText(t, generate.error)}
              </p>
            )}
            <Field label={t("system.apple.clientId")} name="clientId" required mono autoComplete="off" spellCheck={false} maxLength={255} placeholder="com.example.app" defaultValue={clientId} readOnly={Boolean(onGenerated)} />
            <div className="sys-pair">
              <Field label={t("system.apple.teamId")} name="teamId" required mono autoComplete="off" spellCheck={false} pattern="[A-Za-z0-9]{10}" maxLength={10} />
              <Field label={t("system.apple.keyId")} name="keyId" required mono autoComplete="off" spellCheck={false} pattern="[A-Za-z0-9]{10}" maxLength={10} />
            </div>
            <Field kind="textarea" label={t("system.apple.privateKey")} name="privateKey" required mono rows={5} spellCheck={false} placeholder="-----BEGIN PRIVATE KEY-----" />
            <Field label={t("system.apple.days")} name="days" type="number" required min={1} max={maxDays} step={1} defaultValue={maxDays} hint={t("system.apple.daysHint", { max: maxDays })} />
            <div className="confirm-actions">
              <Button variant="secondary" disabled={generate.isPending} onClick={close}>{t("common.cancel")}</Button>
              <Button type="submit" loading={generate.isPending}>{t("system.apple.generate")}</Button>
            </div>
          </fieldset></form>
        )}
      </TaskDialog>
    </>
  );
}
