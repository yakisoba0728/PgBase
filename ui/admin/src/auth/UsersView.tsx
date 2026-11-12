import { useCallback } from "react";
import { useSearchParams } from "react-router";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiErrorWarningLine, RiRefreshLine, RiUser3Line } from "@remixicon/react";
import { errorText } from "../app/queries";
import { shortDate } from "../collections/fields";
import { Button, IconButton } from "../components/Button";
import { EmptyState, Loader } from "../components/Feedback";
import { SplitView } from "../components/SplitView";
import { useT } from "../i18n";
import { NewUserPane } from "./NewUserPane";
import { usersQuery } from "./queries";
import { UserPane } from "./UserPane";
import "../styles/auth.css";

/** ?user=<uuid> opens a user, ?new=1 the create form. */
function useUsersUrl() {
  const [params, setParams] = useSearchParams();
  const set = useCallback((user: string | null, creating = false) =>
    setParams((previous) => {
      const next = new URLSearchParams(previous);
      next.delete("user");
      next.delete("new");
      if (user) next.set("user", user);
      if (creating) next.set("new", "1");
      return next;
    }), [setParams]);
  return { user: params.get("user"), creating: params.has("new"), open: (id: string) => set(id), create: () => set(null, true), close: () => set(null) };
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

/** Every user across auth collections, in id order with cursor paging (the API has no filters). */
export function UsersView() {
  const t = useT();
  const url = useUsersUrl();
  const users = useInfiniteQuery(usersQuery);
  const items = users.data?.pages.flatMap((page) => page.items) ?? [];
  const openUser = items.find((user) => user.id === url.user);

  const list = (
    <>
      <div className="toolbar">
        {users.data && <span className="toolbar-count">{users.hasNextPage ? t("auth.users.loadedMore", { count: items.length }) : t("auth.users.count", { count: items.length })}</span>}
      </div>
      <div className="grid-scroll">
        {users.isPending ? (
          <Loader />
        ) : users.isError ? (
          <EmptyState icon={RiErrorWarningLine}>{errorText(t, users.error)}</EmptyState>
        ) : items.length === 0 ? (
          <EmptyState icon={RiUser3Line}>{t("auth.users.empty")}</EmptyState>
        ) : (
          <>
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
                {items.map((user) => (
                  <tr key={user.id} className="auth-link-row" aria-current={user.id === url.user ? "true" : undefined} onClick={() => url.open(user.id)}>
                    <td>
                      <button type="button" className="auth-cell-button" onClick={(event) => { event.stopPropagation(); url.open(user.id); }}>{user.email}</button>
                    </td>
                    <td><span className="auth-chip" data-tone={user.active ? undefined : "danger"}>{user.active ? t("auth.users.active") : t("auth.users.disabled")}</span></td>
                    <td><span className="auth-chip" data-tone={user.verified ? "on" : undefined}>{user.verified ? t("auth.users.yes") : t("auth.users.no")}</span></td>
                    <td className="txt-hint">{shortDate(user.createdAt)}</td>
                    <td className="mono txt-hint">{user.id}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {users.hasNextPage && (
              <div className="grid-foot">
                <Button size="sm" variant="secondary" loading={users.isFetchingNextPage} onClick={() => users.fetchNextPage()}>{t("records.loadMore")}</Button>
              </div>
            )}
          </>
        )}
      </div>
    </>
  );

  let pane = null;
  if (url.creating) pane = <NewUserPane onClose={url.close} onCreated={url.open} />;
  else if (openUser) pane = <UserPane key={openUser.id} user={openUser} onClose={url.close} />;
  else if (url.user && !users.isPending && !users.hasNextPage) pane = <EmptyState icon={RiErrorWarningLine}>{t("auth.users.missing")}</EmptyState>;
  return <SplitView list={list} paneLabel={t("auth.users.detail")} onClose={url.close} pane={pane} />;
}
