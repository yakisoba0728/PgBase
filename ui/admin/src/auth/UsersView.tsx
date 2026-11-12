import { useState, type FormEvent } from "react";
import { useSearchState } from "../app/search";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiErrorWarningLine, RiRefreshLine, RiSearchLine, RiUser3Line } from "@remixicon/react";
import type { ManagedUser } from "../api/users";
import { errorText } from "../app/queries";
import { useTimeFormat } from "../app/format";
import { Button, IconButton } from "../components/Button";
import { ErrorState, LoadMore } from "../components/Feedback";
import { EmptyState, Loader } from "../components/Feedback";
import { Field, Searchbar } from "../components/Field";
import { openRow } from "../components/DataGrid";
import { SplitView } from "../components/SplitView";
import { useT, type Translate } from "../i18n";
import { NewUserPane } from "./NewUserPane";
import { userQuery, usersQuery } from "./queries";
import { isUserId, userStatusFilters, useUserScan, type UserScan, type UserStatusFilter } from "./userScan";
import { UserPane } from "./UserPane";
import "../styles/auth.css";

/** ?q= searches, ?status= filters, ?user=<uuid> opens a user, ?new=1 the create form. */
function useUsersUrl() {
  const { params, update } = useSearchState();
  const set = (user: string | null, creating = false) => update({ user, new: creating ? "1" : null });
  const status = params.get("status") as UserStatusFilter | null;
  return {
    user: params.get("user"),
    creating: params.has("new"),
    query: params.get("q") ?? "",
    status: status && userStatusFilters.includes(status) ? status : "all",
    open: (id: string) => set(id),
    create: () => set(null, true),
    close: () => set(null),
    filter: (changes: Record<string, string | null>) => update(changes),
  };
}

export function UsersActions() {
  const t = useT();
  const url = useUsersUrl();
  const queryClient = useQueryClient();
  return (
    <>
      <IconButton label={t("auth.users.refresh")} icon={RiRefreshLine} onClick={() => queryClient.invalidateQueries({ queryKey: ["auth", "users"] })} />
      <Button size="sm" icon={RiAddLine} onClick={url.create}>{t("auth.users.new")}</Button>
    </>
  );
}

/** Every user across auth collections. Plain browsing pages by id; search and filters scan all pages. */
export function UsersView() {
  const t = useT();
  const url = useUsersUrl();
  const [text, setText] = useState(url.query);
  const searching = Boolean(url.query.trim() && !isUserId(url.query)) || url.status !== "all";
  const users = useInfiniteQuery({ ...usersQuery, enabled: !searching });
  const scan = useUserScan(url.query, url.status, searching);
  const jump = isUserId(url.query) ? url.query.trim().toLowerCase() : null;
  const openId = url.user ?? null;
  // The open user is always read fresh, so actions show their effect even on scanned rows.
  const fresh = useQuery({ ...userQuery(openId ?? ""), enabled: Boolean(openId) });
  const jumped = useQuery({ ...userQuery(jump ?? ""), enabled: Boolean(jump) });
  const loaded = users.data?.pages.flatMap((page) => page.items) ?? [];
  const rows = jump ? (jumped.data ? [jumped.data] : []) : searching ? scan.matches : loaded;
  const openUser = fresh.data ?? rows.find((user) => user.id === openId);

  const onSearch = (event: FormEvent) => {
    event.preventDefault();
    url.filter({ q: text.trim() || null });
  };

  const toolbar = (
    <form className="toolbar auth-user-filter" role="search" onSubmit={onSearch}>
      <Searchbar label={t("auth.users.search")} primary placeholder={t("auth.users.searchHint")} value={text}
        onChange={(event) => {
          setText(event.target.value);
          if (!event.target.value) url.filter({ q: null });
        }} />
      <Field kind="select" label={t("auth.users.status")} value={url.status} options={userStatusFilters.map((value) => ({ value, label: t(`auth.users.filter.${value}`) }))}
        onChange={(event) => url.filter({ status: event.target.value === "all" ? null : event.target.value })} />
    </form>
  );

  let body;
  if (jump) {
    body = jumped.isPending ? <Loader /> : jumped.isError ? <ErrorState error={jumped.error} busy={jumped.isFetching} onRetry={() => void jumped.refetch()} />
      : rows.length === 0 ? <EmptyState icon={RiUser3Line}>{t("auth.users.noId")}</EmptyState> : <UserTable t={t} rows={rows} openId={openId} fresh={fresh.data} onOpen={url.open} />;
  } else if (searching) {
    body = (
      <>
        <ScanBar t={t} scan={scan} />
        {rows.length === 0 ? (
          scan.state === "running" ? <Loader /> : scan.error ? null : <EmptyState icon={RiSearchLine}>{t("auth.users.noMatch")}</EmptyState>
        ) : (
          <UserTable t={t} rows={rows} openId={openId} fresh={fresh.data} onOpen={url.open} />
        )}
      </>
    );
  } else if (users.isPending) body = <Loader />;
  else if (users.isError && !users.data) body = <ErrorState error={users.error} busy={users.isFetching} onRetry={() => void users.refetch()} />;
  else if (loaded.length === 0) body = <EmptyState icon={RiUser3Line}>{t("auth.users.empty")}</EmptyState>;
  else {
    body = (
      <>
        <p className="auth-scan txt-hint">{users.hasNextPage ? t("auth.users.loadedMore", { count: loaded.length }) : t("auth.users.count", { count: loaded.length })}</p>
        <UserTable t={t} rows={rows} openId={openId} fresh={fresh.data} onOpen={url.open} />
        <LoadMore query={users} />
      </>
    );
  }

  let pane = null;
  if (url.creating) pane = <NewUserPane onClose={url.close} onCreated={url.open} />;
  else if (openUser) pane = <UserPane key={openUser.id} user={openUser} onClose={url.close} />;
  else if (openId && fresh.isError) pane = <ErrorState error={fresh.error} busy={fresh.isFetching} onRetry={() => void fresh.refetch()} />;
  else if (openId && fresh.data === null) pane = <EmptyState icon={RiErrorWarningLine}>{t("auth.users.missing")}</EmptyState>;
  else if (openId) pane = <Loader />;
  return (
    <SplitView
      list={
        <>
          {toolbar}
          <div className="grid-scroll">{body}</div>
        </>
      }
      paneLabel={t("auth.users.detail")}
      onClose={url.close}
      pane={pane}
    />
  );
}

function ScanBar({ t, scan }: { t: Translate; scan: UserScan }) {
  return (
    <div className="auth-scan" role="status" aria-live="polite">
      {scan.state === "running" && <span className="loader" aria-hidden />}
      <span className="txt-hint">
        {scan.state === "done" ? t("auth.users.scanDone", { scanned: scan.scanned, count: scan.matches.length }) : t("auth.users.scanProgress", { scanned: scan.scanned, count: scan.matches.length })}
      </span>
      {scan.error ? <span className="field-error">{errorText(t, scan.error)}</span> : null}
      {scan.state === "running" && <Button size="sm" variant="secondary" onClick={scan.stop}>{t("auth.users.scanStop")}</Button>}
      {scan.state === "paused" && <Button size="sm" variant="secondary" onClick={scan.resume}>{scan.error ? t("common.retry") : t("auth.users.scanResume")}</Button>}
    </div>
  );
}

function UserTable({ t, rows, openId, fresh, onOpen }: { t: Translate; rows: ManagedUser[]; openId: string | null; fresh?: ManagedUser | null; onOpen: (id: string) => void }) {
  const time = useTimeFormat();
  return (
    <table className="grid" aria-label={t("nav.users")}>
      <thead>
        <tr>
          <th>{t("auth.users.email")}</th>
          <th>{t("auth.users.status")}</th>
          <th>{t("auth.users.verified")}</th>
          <th>{t("auth.users.created")}</th>
          <th>id</th>
        </tr>
      </thead>
      <tbody>
        {rows.map((row) => {
          const user = fresh && fresh.id === row.id ? fresh : row;
          return (
            <tr key={user.id} {...openRow(() => onOpen(user.id), user.id === openId)}>
              <td>{user.email}</td>
              <td><span className="dot" data-tone={user.active ? "success" : "danger"}>{user.active ? t("auth.users.active") : t("auth.users.disabled")}</span></td>
              <td><span className="dot" data-tone={user.verified ? "success" : undefined}>{user.verified ? t("auth.users.yes") : t("auth.users.no")}</span></td>
              <td className="mono">{time(user.createdAt)}</td>
              <td className="mono txt-hint">{user.id}</td>
            </tr>
          );
        })}
      </tbody>
    </table>
  );
}
