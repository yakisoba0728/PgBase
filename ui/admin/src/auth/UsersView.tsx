import { useEffect, useRef, useState, type FormEvent } from "react";
import { windowOpen, windowShifted } from "../app/queries";
import { useSearchState } from "../app/search";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { RiAddLine, RiErrorWarningLine, RiSearchLine, RiUser3Line } from "@remixicon/react";
import type { ManagedUser } from "../api/users";
import { useTimeFormat } from "../app/format";
import { Button, RefreshButton } from "../components/Button";
import { EmptyState, LoadMore, WindowNotice } from "../components/Feedback";
import { Searchbar, Select } from "../components/Field";
import { QueryView } from "../components/QueryView";
import { SplitView } from "../components/SplitView";
import { Row, Table, TableScroll } from "../components/Table";
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
    filter: (changes: Record<string, string | null>, typing = false) => update(changes, { replace: typing }),
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
  useEffect(() => setText(url.query), [url.query]);
  const listQuery = usersQuery({ q: url.query.trim(), collection: url.collection });
  const users = useInfiniteQuery(listQuery);
  const queryClient = useQueryClient();
  const collections = useQuery(authCollectionsQuery);
  const names = new Map((collections.data ?? []).map((collection) => [collection.id, collection.name]));
  const openId = url.user ?? null;
  // The open user is always read fresh, so actions show their effect on the listed row too.
  const fresh = useQuery({ ...userQuery(openId ?? ""), enabled: Boolean(openId) });
  const rows = users.data?.pages.flatMap((page) => page.items) ?? [];
  // The window moved past the first page; its total is only sent with that page, so keep the last one seen.
  const shifted = windowShifted(users.data);
  const firstSeen = useRef<{ key: string; page: NonNullable<typeof users.data>["pages"][number] }>(undefined);
  const listKey = JSON.stringify(listQuery.queryKey);
  if (users.data && !shifted) firstSeen.current = { key: listKey, page: users.data.pages[0] };
  const first = firstSeen.current?.key === listKey ? firstSeen.current.page : undefined;
  // null means the user is gone; only a still-loading read falls back to the listed row.
  const openUser = fresh.data === null ? undefined : (fresh.data ?? rows.find((user) => user.id === openId));
  const filtered = Boolean(url.query.trim() || url.collection);

  const onSearch = (event: FormEvent) => {
    event.preventDefault();
    url.filter({ q: text.trim() || null });
  };

  const toolbar = (
    <form className="toolbar" role="search" onSubmit={onSearch}>
      <Searchbar size="sm" label={t("auth.users.search")} primary placeholder={t("auth.users.searchHint")} value={text}
        onChange={(event) => {
          setText(event.target.value);
          if (!event.target.value) url.filter({ q: null }, true);
        }} />
      <Select size="sm" className="control-fit" aria-label={t("auth.users.collection")} value={url.collection} onChange={(event) => url.filter({ collection: event.target.value || null })}>
        <option value="">{t("auth.users.allCollections")}</option>
        {(collections.data ?? []).map((collection) => <option key={collection.id} value={collection.id}>{collection.name}</option>)}
      </Select>
    </form>
  );

  const body = (
    <QueryView query={users} isEmpty={(data) => data.pages.every((page) => page.items.length === 0)}
      empty={filtered ? <EmptyState icon={RiSearchLine}>{t("auth.users.noMatch")}</EmptyState> : <EmptyState icon={RiUser3Line}>{t("auth.users.empty")}</EmptyState>}>
      {() => (
        <>
          <p className="auth-scan txt-hint" role="status">
            {first?.totalCapped ? t("auth.users.countCapped", { count: first.total ?? 0 }) : t("auth.users.count", { count: first?.total ?? rows.length })}
            {users.hasNextPage || shifted ? ` · ${t("auth.users.loadedMore", { count: rows.length })}` : null}
          </p>
          {shifted && (
            <WindowNotice action={t("auth.users.toStart")} onReset={(button) => {
              button.closest(".grid-scroll")?.scrollTo(0, 0);
              void queryClient.resetQueries({ queryKey: listQuery.queryKey });
            }}>{t("auth.users.shifted", { count: rows.length })}</WindowNotice>
          )}
          <UserTable t={t} rows={rows} names={names} openId={openId} fresh={fresh.data} onOpen={url.open} />
          {/* Once the window is full each further page takes a click, as in the logs list. */}
          <LoadMore query={users} auto={windowOpen(users.data)} />
        </>
      )}
    </QueryView>
  );

  let pane = null;
  if (url.creating) pane = <NewUserPane onClose={url.close} onCreated={url.open} />;
  else if (openUser) pane = <UserPane key={openUser.id} user={openUser} onClose={url.close} />;
  // Not in the loaded rows: the single lookup decides (null is "no such user").
  else if (openId) pane = <QueryView query={fresh} skeleton="form" empty={<EmptyState icon={RiErrorWarningLine}>{t("common.notFound")}</EmptyState>} isEmpty={(data) => data === null}>{() => null}</QueryView>;
  return (
    <SplitView
      list={
        <>
          {toolbar}
          <TableScroll>{body}</TableScroll>
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
    <Table label={t("nav.users")}>
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
            <Row key={user.id} current={user.id === openId} onOpen={() => onOpen(user.id)}>
              <td>{user.email}</td>
              <td className="mono">{names.get(user.authCollectionId) ?? user.authCollectionId}</td>
              <td><span className="dot" data-tone={user.active ? "success" : "danger"}>{user.active ? t("auth.users.active") : t("auth.users.disabled")}</span></td>
              <td><span className="dot" data-tone={user.verified ? "success" : undefined}>{user.verified ? t("auth.users.yes") : t("auth.users.no")}</span></td>
              <td className="mono">{time(user.createdAt)}</td>
              <td className="mono txt-hint">{user.id}</td>
            </Row>
          );
        })}
      </tbody>
    </Table>
  );
}
