import type { FormEvent } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import { useMutation } from "@tanstack/react-query";
import { RiLockPasswordLine, RiMailLine } from "@remixicon/react";
import { confirmPasswordReset, requestPasswordReset } from "../api/session";
import { Button } from "../components/Button";
import { Field } from "../components/Field";
import { toast } from "../components/Toast";
import { useT } from "../i18n";
import { FormError, formValue, GuestLayout, matchingPassword } from "./GuestLayout";

/** The emailed link carries the token in the fragment (#token=…), so it never reaches server logs. */
export function ResetPasswordScreen() {
  const token = new URLSearchParams(useLocation().hash.slice(1)).get("token");
  return token ? <ConfirmReset token={token} /> : <RequestReset />;
}

function RequestReset() {
  const t = useT();
  const mutation = useMutation({ mutationFn: requestPasswordReset });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    mutation.mutate(formValue(new FormData(event.currentTarget), "email"));
  };
  return (
    <GuestLayout title={t("reset.requestTitle")}>
      {mutation.isSuccess ? (
        <p className="notice" role="status">{t("reset.requestSent")}</p>
      ) : (
        <form className="form" onSubmit={onSubmit} onInput={() => mutation.isError && mutation.reset()}>
          <Field label={t("field.email")} icon={RiMailLine} name="email" type="email" required autoFocus autoComplete="username" />
          <FormError error={mutation.error} />
          <Button type="submit" block loading={mutation.isPending}>{t("reset.requestSubmit")}</Button>
        </form>
      )}
      <div className="guest-links">
        <Link className="link" to="/login">{t("reset.toLogin")}</Link>
      </div>
    </GuestLayout>
  );
}

function ConfirmReset({ token }: { token: string }) {
  const t = useT();
  const navigate = useNavigate();
  const mutation = useMutation({
    mutationFn: (form: FormData) => confirmPasswordReset(token, matchingPassword(form)),
    onSuccess: () => {
      toast(t("reset.done"));
      navigate("/login", { replace: true });
    },
  });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    mutation.mutate(new FormData(event.currentTarget));
  };
  return (
    <GuestLayout title={t("reset.confirmTitle")}>
      <form className="form" onSubmit={onSubmit} onInput={() => mutation.isError && mutation.reset()}>
        <Field label={t("field.newPassword")} icon={RiLockPasswordLine} name="password" type="password" required autoFocus autoComplete="new-password" />
        <Field label={t("field.passwordConfirm")} icon={RiLockPasswordLine} name="passwordConfirm" type="password" required autoComplete="new-password" />
        <FormError error={mutation.error} credentialsKey="error.invalidResetToken" />
        <Button type="submit" block loading={mutation.isPending}>{t("reset.confirmSubmit")}</Button>
      </form>
      <div className="guest-links">
        <Link className="link" to="/login">{t("reset.toLogin")}</Link>
      </div>
    </GuestLayout>
  );
}
