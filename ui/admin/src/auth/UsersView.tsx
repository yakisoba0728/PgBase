import { useState, type FormEvent } from "react";
import { useSearchState } from "../app/search";
import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { RiAddLine, RiErrorWarningLine, RiSearchLine, RiUser3Line } from "@remixicon/react";
import type { ManagedUser } from "../api/users";
import { useTimeFormat } from "../app/format";
import { Button, RefreshButton } from "../components/Button";
import { ErrorState, LoadMore } from "../components/Feedback";
import { EmptyState, Loader } from "../components/Feedback";
import { Field, Searchbar } from "../components/Field";
import { openRow } from "../components/DataGrid";
import { SplitView } from "../components/SplitView";
import { useT, type Translate } from "../i18n";
import { NewUserPane } from "./NewUserPane";
import { authCollectionsQuery, userQuery, usersQuery } from "./queries";
import { UserPane } from "./UserPane";
import "../styles/auth.css";

/** ?q= searches (email substring or exact id), ?collection= filters, ?user=<uuid> opens a user, ?new=1 the create form. */
function useUsersUrl() {
  const { params, update } = useSearchState();
  const set = (user: string | null, creating = false) => update({ user, new: creating ? "1" : null });
  return {
    user: params.get("user"),
    creating: params.has("new"),
    query: params.get("q") ?? "",
    collection: params.get("collection") ?? "",
    open: (id: string) => set(id),
    create: () => set(null, true),
    close: () => set(null),
    filter: (changes: Record<string, string | null>) => update(changes),
  };
}

export function UsersActions() {
  const t = useT();
  const url = useUsersUrl();
  return (
    <>
      <RefreshButton queryKey={["auth", "users"]} />
      <Button size="sm" icon={RiAddLine} data-new onClick={url.create}>{t("auth.users.new")}</Button>
    </>
  );
}

/** Every user across auth collections, paged by id. Search and the collection filter run on the server. */
export function UsersView() {
  const t = useT();
  const url = useUsersUrl();
  const [text, setText] = useState(url.query);
  const users = useInfiniteQuery(usersQuery({ q: url.query.trim(), collection: url.collection }));
  const collections = useQuery(authCollectionsQuery);
  const names = new Map((collections.data ?? []).map((collection) => [collection.id, collection.name]));
  const openId = url.user ?? null;
  // The open user is always read fresh, so actions show their effect on the listed row too.
  const fresh = useQuery({ ...userQuery(openId ?? ""), enabled: Boolean(openId) });
  const rows = users.data?.pages.flatMap((page) => page.items) ?? [];
  const first = users.data?.pages[0];
  const openUser = fresh.data ?? rows.find((user) => user.id === openId);
  const filtered = Boolean(url.query.trim() || url.collection);

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
      <Field kind="select" label={t("auth.users.collection")} value={url.collection}
        options={[{ value: "", label: t("auth.users.allCollections") }, ...(collections.data ?? []).map((collection) => ({ value: collection.id, label: collection.name }))]}
        onChange={(event) => url.filter({ collection: event.target.value || null })} />
    </form>
  );

  let body;
  if (users.isPending) body = <Loader />;
  else if (users.isError && !users.data) body = <ErrorState error={users.error} busy={users.isFetching} onRetry={() => void users.refetch()} />;
  else if (rows.length === 0) body = filtered ? <EmptyState icon={RiSearchLine}>{t("auth.users.noMatch")}</EmptyState> : <EmptyState icon={RiUser3Line}>{t("auth.users.empty")}</EmptyState>;
  else {
    body = (
      <>
        <p className="auth-scan txt-hint" role="status">
          {first?.totalCapped ? t("auth.users.countCapped", { count: first.total ?? 0 }) : t("auth.users.count", { count: first?.total ?? rows.length })}
          {users.hasNextPage ? ` · ${t("auth.users.loadedMore", { count: rows.length })}` : null}
        </p>
        <UserTable t={t} rows={rows} names={names} openId={openId} fresh={fresh.data} onOpen={url.open} />
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

function UserTable({ t, rows, names, openId, fresh, onOpen }: { t: Translate; rows: ManagedUser[]; names: Map<string, string>; openId: string | null; fresh?: ManagedUser | null; onOpen: (id: string) => void }) {
  const time = useTimeFormat();
  return (
    <table className="grid" aria-label={t("nav.users")}>
      <thead>
        <tr>
          <th>{t("auth.users.email")}</th>
          <th>{t("auth.users.collection")}</th>
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
              <td className="mono">{names.get(user.authCollectionId) ?? user.authCollectionId}</td>
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
