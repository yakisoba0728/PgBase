import { useState, type FormEvent, type ReactNode } from "react";
import { useNavigate } from "react-router";
import { forgetSession } from "../app/signout";
import { openRow } from "../components/DataGrid";
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiLockPasswordLine, RiLogoutCircleRLine } from "@remixicon/react";
import { ApiError } from "../api/http";
import type { Administrator } from "../api/session";
import { createAdministrator, resetAdministratorPassword, revokeAdministratorSessions, setAdministratorActive } from "../api/system";
import { errorText, sessionQuery } from "../app/queries";
import { Button } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { Loader, LoadMore, Notice } from "../components/Feedback";
import { ErrorState } from "../components/Feedback";
import { Field } from "../components/Field";
import { SplitView, PaneHeader } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import { useTimeFormat } from "../app/format";
import { useSearchState } from "../app/search";
import { administratorsQuery } from "./queries";
import { TaskDialog } from "../components/Dialog";
import { Facts } from "../components/Facts";
import "../styles/system.css";

/** Server codes are shared by several refusals; the action and target pick the wording. */
function adminError(t: Translate, error: unknown, context: "create" | "disable" | "password", self: boolean): string {
  const code = error instanceof ApiError ? error.code : "";
  if (code === "administrator_conflict") return context === "create" ? t("system.admins.emailTaken") : self ? t("system.admins.selfDisable") : t("system.admins.lastActive");
  if (code === "invalid_administrator") return context === "create" ? t("system.admins.invalidCreate") : t("system.admins.invalidPassword");
  return errorText(t, error);
}

export function AdministratorsActions() {
  const t = useT();
  const { update } = useSearchState();
  return <Button icon={RiAddLine} data-new onClick={() => update({ admin: "new" })}>{t("system.admins.new")}</Button>;
}

export function AdministratorsView() {
  const t = useT();
  const time = useTimeFormat();
  const { params, update } = useSearchState();
  const session = useQuery(sessionQuery);
  const query = useInfiniteQuery(administratorsQuery);
  const items = query.data?.pages.flatMap((page) => page.items) ?? [];
  const selfId = session.data?.administrator.id;
  const openId = params.get("admin");
  const open = items.find((item) => item.id === openId);
  const close = () => update({ admin: null });

  const list = (
    <div className="grid-scroll">
      {query.isPending ? (
        <Loader />
      ) : query.isError && !query.data ? (
        <ErrorState error={query.error} busy={query.isFetching} onRetry={() => void query.refetch()} />
      ) : (
        <>
          <table className="grid" aria-label={t("nav.administrators")}>
            <thead>
              <tr>
                <th>{t("system.admins.email")}</th>
                <th>{t("system.admins.status")}</th>
                <th>{t("system.admins.created")}</th>
                <th>{t("system.admins.updated")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.id} {...openRow(() => update({ admin: item.id }), openId === item.id)}>
                  <td>
                    {item.email}{" "}
                    {item.id === selfId && <span className="chip">{t("system.admins.you")}</span>}
                  </td>
                  <td><span className="dot" data-tone={item.active ? "success" : undefined}>{item.active ? t("system.admins.active") : t("system.admins.disabled")}</span></td>
                  <td className="mono">{time(item.createdAt)}</td>
                  <td className="mono">{time(item.updatedAt)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <LoadMore query={query} />
        </>
      )}
    </div>
  );

  const pane = openId === "new" ? <NewAdministrator onClose={close} onCreated={(id) => update({ admin: id })} /> : open ? <AdministratorPane key={open.id} admin={open} self={open.id === selfId} onClose={close} /> : null;
  return <SplitView list={list} pane={pane} paneLabel={t("system.admins.detail")} onClose={close} />;
}

function NewAdministrator({ onClose, onCreated }: { onClose: () => void; onCreated: (id: string) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const create = useMutation({
    mutationFn: createAdministrator,
    onSuccess: (admin) => {
      void queryClient.invalidateQueries({ queryKey: administratorsQuery.queryKey });
      toast(t("system.admins.createdToast", { email: admin.email }));
      onCreated(admin.id);
    },
  });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    create.mutate({ email: String(form.get("email") ?? "").trim(), password: String(form.get("password") ?? "") });
  };
  return (
    <form className="pane-form" onSubmit={onSubmit}>
      <PaneHeader title={t("system.admins.new")} onClose={onClose} />
      <div className="pane-body form">
        {create.isError && <Notice tone="danger">{adminError(t, create.error, "create", false)}</Notice>}
        <Field label={t("system.admins.email")} name="email" type="email" required autoComplete="off" />
        <Field label={t("system.admins.password")} name="password" type="password" required minLength={12} maxLength={1024} autoComplete="new-password" hint={t("system.admins.passwordHint")} />
        <p className="txt-hint">{t("system.admins.newHint")}</p>
      </div>
      <footer className="pane-footer">
        <Button variant="secondary" onClick={onClose}>{t("common.cancel")}</Button>
        <Button type="submit" loading={create.isPending}>{t("records.create")}</Button>
      </footer>
    </form>
  );
}

type Pending = "disable" | "revoke" | "password" | null;

function AdministratorPane({ admin, self, onClose }: { admin: Administrator; self: boolean; onClose: () => void }) {
  const t = useT();
  const time = useTimeFormat();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const [pending, setPending] = useState<Pending>(null);
  const refresh = () => queryClient.invalidateQueries({ queryKey: administratorsQuery.queryKey });
  // Ending one's own sessions (password, revoke) signs this browser out too.
  const signedOut = () => {
    forgetSession();
    navigate("/login", { replace: true });
  };

  const active = useMutation({
    mutationFn: (value: boolean) => setAdministratorActive(admin.id, value),
    // Enabling is trivially reversible, so it runs without a confirmation and offers Undo.
    onSuccess: (value) =>
      value.active
        ? toast(t("system.admins.enabledToast"), "info", { label: t("common.undo"), run: () => void setAdministratorActive(admin.id, false).then(refresh, (error) => toast(errorText(t, error), "danger")) })
        : toast(t("system.admins.disabledToast")),
    onSettled: () => {
      setPending(null);
      void refresh();
    },
  });
  const revoke = useMutation({
    mutationFn: () => revokeAdministratorSessions(admin.id),
    onSuccess: () => (self ? signedOut() : toast(t("system.admins.revoked"))),
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => setPending(null),
  });
  const password = useMutation({
    mutationFn: (value: string) => resetAdministratorPassword(admin.id, value),
    onSuccess: () => {
      setPending(null);
      if (self) signedOut();
      else toast(t("system.admins.passwordSet"));
    },
  });
  const closePassword = () => {
    setPending(null);
    password.reset();
  };

  return (
    <div className="pane-form">
      <PaneHeader title={admin.email} onClose={onClose} />
      <div className="pane-body">
        {active.isError && <Notice tone="danger">{adminError(t, active.error, "disable", self)}</Notice>}
        <Facts
          items={[
            { label: t("system.admins.status"), value: <><span className="dot" data-tone={admin.active ? "success" : undefined}>{admin.active ? t("system.admins.active") : t("system.admins.disabled")}</span>{" "}{self && <span className="chip">{t("system.admins.you")}</span>}</> },
            { label: t("system.admins.created"), value: time(admin.createdAt, true) },
            { label: t("system.admins.updated"), value: time(admin.updatedAt, true) },
            { label: "ID", value: admin.id, mono: true, copy: true },
          ]}
        />
        <div className="sys-actions">
          <Action title={admin.active ? t("system.admins.disable") : t("system.admins.enable")} text={admin.active ? t("system.admins.disableHint") : t("system.admins.enableHint")}>
            <Button variant={admin.active ? "danger" : "secondary"} size="sm" loading={active.isPending} onClick={() => (admin.active ? setPending("disable") : active.mutate(true))}>
              {admin.active ? t("system.admins.disable") : t("system.admins.enable")}
            </Button>
          </Action>
          <Action title={t("system.admins.setPassword")} text={self ? t("system.admins.setPasswordSelf") : t("system.admins.setPasswordHint")}>
            <Button variant="secondary" size="sm" icon={RiLockPasswordLine} onClick={() => setPending("password")}>{t("system.admins.setPassword")}</Button>
          </Action>
          <Action title={t("system.admins.revoke")} text={self ? t("system.admins.revokeSelf") : t("system.admins.revokeHint")}>
            <Button variant="secondary" size="sm" icon={RiLogoutCircleRLine} onClick={() => setPending("revoke")}>{t("system.admins.revoke")}</Button>
          </Action>
        </div>
      </div>
      <ConfirmDialog open={pending === "disable"} title={t("system.admins.disableTitle", { email: admin.email })} message={t("system.admins.disableText")} confirmLabel={t("system.admins.disable")} danger busy={active.isPending}
        onConfirm={() => active.mutate(false)} onCancel={() => setPending(null)} />
      <ConfirmDialog open={pending === "revoke"} title={t("system.admins.revokeTitle", { email: admin.email })} message={self ? t("system.admins.revokeSelf") : t("system.admins.revokeHint")} confirmLabel={t("system.admins.revoke")} danger busy={revoke.isPending}
        onConfirm={() => revoke.mutate()} onCancel={() => setPending(null)} />
      <TaskDialog open={pending === "password"} title={t("system.admins.passwordTitle", { email: admin.email })} onClose={closePassword}>
        <PasswordForm self={self} busy={password.isPending} error={password.isError ? adminError(t, password.error, "password", self) : undefined} onSubmit={(value) => password.mutate(value)} onCancel={closePassword} />
      </TaskDialog>
    </div>
  );
}

function Action({ title, text, children }: { title: string; text: string; children: ReactNode }) {
  return (
    <div className="sys-action">
      <div>
        <h3>{title}</h3>
        <p className="txt-hint">{text}</p>
      </div>
      {children}
    </div>
  );
}

/** New password with confirmation; the server sets it directly and ends the account's sessions. */
export function PasswordForm({ self, busy, error, onSubmit, onCancel }: { self: boolean; busy: boolean; error?: string; onSubmit: (password: string) => void; onCancel?: () => void }) {
  const t = useT();
  const [mismatch, setMismatch] = useState(false);
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const value = String(form.get("newPassword") ?? "");
    const ok = value === String(form.get("confirmPassword") ?? "");
    setMismatch(!ok);
    if (ok) onSubmit(value);
  };
  return (
    <form className="form" onSubmit={submit}>
      <p className="txt-hint">{self ? t("system.admins.setPasswordSelf") : t("system.admins.setPasswordHint")}</p>
      {error && <p className="field-error" role="alert">{error}</p>}
      <Field label={t("system.admins.newPassword")} name="newPassword" type="password" required minLength={12} maxLength={1024} autoComplete="new-password" hint={t("system.admins.passwordHint")} />
      <Field label={t("system.admins.confirmPassword")} name="confirmPassword" type="password" required autoComplete="new-password" error={mismatch ? t("error.password_mismatch") : undefined} />
      <div className="confirm-actions">
        {onCancel && <Button variant="secondary" onClick={onCancel}>{t("common.cancel")}</Button>}
        <Button type="submit" variant={self ? "danger" : "primary"} loading={busy}>{t("system.admins.setPassword")}</Button>
      </div>
    </form>
  );
}
