import { useState, type FormEvent } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiCloseLine, RiErrorWarningLine, RiKey2Line } from "@remixicon/react";
import { ApiError } from "../api/http";
import { issueServiceToken, revokeServiceToken, type ServiceToken, type ServiceTokenScope } from "../api/system";
import { errorText } from "../app/queries";
import { collectionsQuery } from "../collections/queries";
import { Button, IconButton } from "../components/Button";
import { ConfirmDialog } from "../components/Dialog";
import { EmptyState, Loader } from "../components/Feedback";
import { Field } from "../components/Field";
import { SplitView } from "../components/SplitView";
import { toast } from "../components/Toast";
import { useT, type Translate } from "../i18n";
import { useParams, useTimeFormat } from "../ops/queries";
import { serviceTokensQuery } from "./queries";
import { SecretView, TaskDialog } from "./SecretDialog";
import "../styles/system.css";

const actions: ServiceTokenScope["action"][] = ["read", "create", "update", "delete"];

function tokenState(t: Translate, token: ServiceToken): { label: string; tone?: string } {
  if (token.revokedAt) return { label: t("system.tokens.revoked"), tone: "danger" };
  if (Date.parse(token.expiresAt) <= Date.now()) return { label: t("system.tokens.expired"), tone: "warning" };
  if (!token.active) return { label: t("system.tokens.inactive") };
  return { label: t("system.tokens.active"), tone: "success" };
}

export function ServiceTokensActions() {
  const t = useT();
  const { update } = useParams();
  return <Button icon={RiAddLine} onClick={() => update({ token: "new" })}>{t("system.tokens.new")}</Button>;
}

export function ServiceTokensView() {
  const t = useT();
  const time = useTimeFormat();
  const { params, update } = useParams();
  const query = useQuery(serviceTokensQuery);
  const collections = useQuery(collectionsQuery);
  const names = new Map((collections.data ?? []).map((collection) => [collection.id, collection.name]));
  const items = query.data?.items ?? [];
  const openId = params.get("token");
  const open = items.find((item) => item.id === openId);
  const close = () => update({ token: null });

  const list = (
    <div className="grid-scroll">
      {query.isPending ? (
        <Loader />
      ) : query.isError ? (
        <EmptyState icon={RiErrorWarningLine}>{errorText(t, query.error)}</EmptyState>
      ) : items.length === 0 ? (
        <EmptyState icon={RiKey2Line}>{t("system.tokens.empty")}</EmptyState>
      ) : (
        <>
          <table className="grid" aria-label={t("nav.tokens")}>
            <thead>
              <tr>
                <th>{t("system.tokens.name")}</th>
                <th>{t("system.tokens.state")}</th>
                <th>{t("system.tokens.scopes")}</th>
                <th>{t("system.tokens.expires")}</th>
                <th>{t("system.tokens.created")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => {
                const state = tokenState(t, item);
                return (
                  <tr key={item.id} tabIndex={0} aria-current={openId === item.id ? "true" : undefined} onClick={() => update({ token: item.id })}
                    onKeyDown={(event) => { if (event.key === "Enter") update({ token: item.id }); }}>
                    <td>{item.name}</td>
                    <td><span className="sys-dot" data-tone={state.tone}>{state.label}</span></td>
                    <td className="sys-scope-cell">{[...new Set(item.scopes.map((scope) => names.get(scope.collectionId) ?? scope.collectionId))].join(", ")}</td>
                    <td className="mono">{time(item.expiresAt)}</td>
                    <td className="mono">{time(item.createdAt)}</td>
                  </tr>
                );
              })}
            </tbody>
          </table>
          {items.length >= 64 && <p className="grid-foot txt-hint">{t("system.tokens.capped")}</p>}
        </>
      )}
    </div>
  );

  const pane = openId === "new" ? <IssueToken onClose={close} onIssued={(id) => update({ token: id })} /> : open ? <TokenPane key={open.id} token={open} names={names} onClose={close} /> : null;
  return <SplitView list={list} pane={pane} paneLabel={t("system.tokens.detail")} onClose={close} />;
}

function TokenPane({ token, names, onClose }: { token: ServiceToken; names: Map<string, string>; onClose: () => void }) {
  const t = useT();
  const time = useTimeFormat();
  const queryClient = useQueryClient();
  const [confirming, setConfirming] = useState(false);
  const state = tokenState(t, token);
  const revoke = useMutation({
    mutationFn: () => revokeServiceToken(token.id),
    onSuccess: () => toast(t("system.tokens.revokedToast", { name: token.name })),
    onError: (error) => toast(errorText(t, error), "danger"),
    onSettled: () => {
      setConfirming(false);
      void queryClient.invalidateQueries({ queryKey: serviceTokensQuery.queryKey });
    },
  });
  const byCollection = new Map<string, string[]>();
  for (const scope of token.scopes) byCollection.set(scope.collectionId, [...(byCollection.get(scope.collectionId) ?? []), scope.action]);
  return (
    <div className="sys-pane">
      <header className="pane-header">
        <h2 className="pane-title">{token.name}</h2>
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
      </header>
      <div className="pane-body">
        <dl className="sys-props">
          <div><dt>{t("system.tokens.state")}</dt><dd><span className="sys-dot" data-tone={state.tone}>{state.label}</span></dd></div>
          <div><dt>{t("system.tokens.expires")}</dt><dd>{time(token.expiresAt, true)}</dd></div>
          <div><dt>{t("system.tokens.created")}</dt><dd>{time(token.createdAt, true)}</dd></div>
          {token.revokedAt && <div><dt>{t("system.tokens.revokedAt")}</dt><dd>{time(token.revokedAt, true)}</dd></div>}
          <div><dt>ID</dt><dd className="mono">{token.id}</dd></div>
        </dl>
        <h3 className="sys-subtitle">{t("system.tokens.scopes")}</h3>
        <dl className="sys-props">
          {[...byCollection].map(([id, granted]) => (
            <div key={id}>
              <dt className={names.has(id) ? undefined : "mono"}>{names.get(id) ?? id}</dt>
              <dd>{granted.map((action) => t(`system.tokens.action.${action as ServiceTokenScope["action"]}`)).join(", ")}</dd>
            </div>
          ))}
        </dl>
      </div>
      <footer className="pane-footer">
        <Button variant="danger" className="pane-footer-start" disabled={Boolean(token.revokedAt)} onClick={() => setConfirming(true)}>{t("system.tokens.revoke")}</Button>
        <Button variant="secondary" onClick={onClose}>{t("common.close")}</Button>
      </footer>
      <ConfirmDialog open={confirming} title={t("system.tokens.revokeTitle", { name: token.name })} message={t("system.tokens.revokeText")} confirmLabel={t("system.tokens.revoke")} danger busy={revoke.isPending}
        onConfirm={() => revoke.mutate()} onCancel={() => setConfirming(false)} />
    </div>
  );
}

function IssueToken({ onClose, onIssued }: { onClose: () => void; onIssued: (id: string) => void }) {
  const t = useT();
  const queryClient = useQueryClient();
  const collections = useQuery(collectionsQuery);
  const [scopes, setScopes] = useState<Set<string>>(new Set());
  const issue = useMutation({
    mutationFn: issueServiceToken,
    onSuccess: () => void queryClient.invalidateQueries({ queryKey: serviceTokensQuery.queryKey }),
  });
  const toggle = (key: string, on: boolean) =>
    setScopes((current) => {
      const next = new Set(current);
      if (on) next.add(key);
      else next.delete(key);
      return next;
    });
  const onSubmit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    issue.mutate({
      name: String(form.get("name") ?? "").trim(),
      ttlSeconds: Math.round(Number(form.get("days"))) * 86400,
      scopes: [...scopes].map((key) => {
        const [collectionId, action] = key.split("/");
        return { collectionId, action: action as ServiceTokenScope["action"] };
      }),
    });
  };
  // The raw secret lives only in the mutation result; Done drops it.
  const done = () => {
    const id = issue.data?.id;
    issue.reset();
    if (id) onIssued(id);
  };
  const invalid = issue.error instanceof ApiError && issue.error.status === 400;
  return (
    <form className="sys-pane" onSubmit={onSubmit}>
      <header className="pane-header">
        <h2 className="pane-title">{t("system.tokens.new")}</h2>
        <IconButton label={t("common.close")} icon={RiCloseLine} onClick={onClose} />
      </header>
      <div className="pane-body form">
        {issue.isError && <div className="pane-notice" data-tone="danger" role="alert"><p>{invalid ? t("system.tokens.invalid") : errorText(t, issue.error)}</p></div>}
        <Field label={t("system.tokens.name")} name="name" required maxLength={128} autoComplete="off" />
        <Field label={t("system.tokens.days")} name="days" type="number" required min={1} max={365} step={1} defaultValue={30} hint={t("system.tokens.daysHint")} />
        <fieldset className="sys-scopes">
          <legend>{t("system.tokens.scopes")}</legend>
          {collections.isPending ? (
            <Loader />
          ) : (collections.data ?? []).length === 0 ? (
            <p className="txt-hint">{t("system.tokens.noCollections")}</p>
          ) : (
            <table className="grid">
              <thead>
                <tr>
                  <th>{t("system.tokens.collection")}</th>
                  {actions.map((action) => <th key={action} className="sys-center">{t(`system.tokens.action.${action}`)}</th>)}
                </tr>
              </thead>
              <tbody>
                {(collections.data ?? []).map((collection) => (
                  <tr key={collection.id}>
                    <td className="mono">{collection.name}</td>
                    {actions.map((action) => {
                      const key = `${collection.id}/${action}`;
                      return (
                        <td key={action} className="sys-center">
                          <input type="checkbox" aria-label={`${collection.name} ${t(`system.tokens.action.${action}`)}`} checked={scopes.has(key)} onChange={(event) => toggle(key, event.target.checked)} />
                        </td>
                      );
                    })}
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </fieldset>
      </div>
      <footer className="pane-footer">
        <span className="txt-hint pane-footer-start">{t("system.tokens.selected", { count: scopes.size })}</span>
        <Button variant="secondary" onClick={onClose}>{t("common.cancel")}</Button>
        <Button type="submit" disabled={scopes.size === 0} loading={issue.isPending}>{t("system.tokens.issue")}</Button>
      </footer>
      <TaskDialog open={Boolean(issue.data)} title={t("system.tokens.issuedTitle", { name: issue.data?.name ?? "" })} onClose={done}>
        {issue.data && <SecretView label={t("system.tokens.secret")} value={issue.data.token} warning={t("system.tokens.once")} onDone={done} />}
      </TaskDialog>
    </form>
  );
}
