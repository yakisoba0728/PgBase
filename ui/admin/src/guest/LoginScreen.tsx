import { useEffect, useRef, useState, type FormEvent } from "react";
import { Link, Navigate, useLocation, useNavigate } from "react-router";
import { useMutation, useQuery } from "@tanstack/react-query";
import { RiLockPasswordLine, RiMailLine, RiShieldKeyholeLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import { login, type LoginInput } from "../api/session";
import { setupStatusQuery } from "../app/queries";
import { adoptSession, finishSignOut } from "../app/signout";
import { Button } from "../components/Button";
import { Field } from "../components/Field";
import { useT } from "../i18n";
import { FormError, formValue, GuestLayout } from "./GuestLayout";

const otpRequired = (error: unknown) => error instanceof ApiError && error.code === "otp_required";

export function LoginScreen() {
  const t = useT();
  const navigate = useNavigate();
  const location = useLocation();
  const status = useQuery(setupStatusQuery);
  // A sign-out reaches here only once the navigation committed (an unsaved-changes Cancel keeps the session).
  // The state is consumed once, so a reload of /login does not end a session signed in since.
  const leaving = location.state as { signedOut?: boolean; end?: boolean } | null;
  const finished = useRef(false);
  useEffect(() => {
    if (!leaving?.signedOut || finished.current) return;
    finished.current = true;
    finishSignOut(leaving.end === true, t);
    void navigate(".", { replace: true, state: null });
  }, [leaving, navigate, t]);
  // Set once the server asks for a TOTP code; the code is then re-posted with these credentials.
  const [pending, setPending] = useState<LoginInput | null>(null);
  const mutation = useMutation({
    mutationFn: login,
    onSuccess: (session) => {
      adoptSession(session);
      const from = (location.state as { from?: string } | null)?.from;
      navigate(from ?? "/collections", { replace: true });
    },
    onError: (error, input) => {
      if (otpRequired(error) && !input.otpCode) setPending(input);
    },
  });

  if (status.data?.setupRequired) return <Navigate to="/setup" replace />;

  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    if (pending) mutation.mutate({ ...pending, otpCode: formValue(form, "otp").replace(/\s+/g, "") });
    else mutation.mutate({ email: formValue(form, "email"), password: formValue(form, "password") });
  };
  const back = () => {
    setPending(null);
    mutation.reset();
  };
  // The otp_required answer is the step change itself, not an error to show.
  const error = pending && !mutation.variables?.otpCode ? null : mutation.error;

  return (
    <GuestLayout title={pending ? t("login.otpTitle") : t("login.title")}>
      <form className="form" onSubmit={onSubmit} onInput={() => error && mutation.reset()}>
        {pending ? (
          <Field key="otp" label={t("field.otp")} icon={RiShieldKeyholeLine} name="otp" required autoFocus inputMode="numeric" autoComplete="one-time-code" pattern="[0-9 ]*" />
        ) : (
          <>
            <Field label={t("field.email")} icon={RiMailLine} name="email" type="email" required autoFocus autoComplete="username" />
            <Field label={t("field.password")} icon={RiLockPasswordLine} name="password" type="password" required autoComplete="current-password" />
          </>
        )}
        <FormError error={error} credentialsKey={pending ? "error.invalidOtp" : undefined} />
        <Button type="submit" block loading={mutation.isPending}>
          {pending ? t("login.otpSubmit") : t("login.submit")}
        </Button>
      </form>
      <div className="guest-links">
        {pending ? (
          <button type="button" className="link" onClick={back}>{t("login.back")}</button>
        ) : (
          <Link className="link" to="/reset-password">{t("login.forgot")}</Link>
        )}
      </div>
    </GuestLayout>
  );
}
