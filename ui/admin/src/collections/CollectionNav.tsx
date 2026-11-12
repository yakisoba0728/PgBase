import { useState } from "react";
import { useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { RiAddLine, RiDatabase2Line, RiEyeLine, RiTableLine, RiUser3Line } from "@remixicon/react";
import type { CollectionType } from "../api/collections";
import { IconButton } from "../components/Button";
import { Searchbar } from "../components/Field";
import { NavGroup, NavItem } from "../components/Nav";
import { useT } from "../i18n";
import { collectionsQuery } from "./queries";
import { collectionPath, useRecentCollections } from "../app/recent";

const recentThreshold = 6;

const collectionIcons: Record<CollectionType, typeof RiTableLine> = { base: RiTableLine, auth: RiUser3Line, view: RiEyeLine };

/** The Data group: every collection, filterable, with a "new collection" action. */
export function CollectionNav() {
  const t = useT();
  const navigate = useNavigate();
  const [query, setQuery] = useState("");
  const { data, isPending, isError } = useQuery(collectionsQuery);
  const needle = query.trim().toLowerCase();
  const items = (data ?? []).filter((collection) => collection.name.toLowerCase().includes(needle));
  const recent = useRecentCollections(data?.map((collection) => collection.name));
  // A shortcut list only pays off once the full list no longer fits at a glance.
  const showRecent = !needle && recent.length > 0 && (data?.length ?? 0) > recentThreshold;
  const iconOf = (name: string) => collectionIcons[data?.find((collection) => collection.name === name)?.type ?? "base"];
  const add = <IconButton className="nav-full-only" label={t("collections.new")} icon={RiAddLine} onClick={() => navigate("/new-collection")} />;
  return (
    <NavGroup title={t("nav.data")} action={add}>
      <NavItem className="nav-rail-only" to="/collections" icon={RiDatabase2Line}>{t("nav.collections")}</NavItem>
      <div className="nav-full-only">
        <div className="nav-filter">
          <Searchbar label={t("collections.filter")} value={query} onChange={(event) => setQuery(event.target.value)} />
        </div>
        {showRecent && (
          <div className="nav-recent" role="group" aria-label={t("nav.recent")}>
            <div className="nav-subtitle">{t("nav.recent")}</div>
            {recent.map((name) => (
              <NavItem key={name} to={`/collections/${encodeURIComponent(name)}`} target={() => collectionPath(name)} icon={iconOf(name)} label={`${t("nav.recent")}: ${name}`}>{name}</NavItem>
            ))}
          </div>
        )}
        {items.map((collection) => (
          <NavItem key={collection.id} to={`/collections/${encodeURIComponent(collection.name)}`} target={() => collectionPath(collection.name)} icon={collectionIcons[collection.type ?? "base"]}>
            {collection.name}
          </NavItem>
        ))}
        {isPending && <div className="nav-empty">{t("common.loading")}</div>}
        {isError && <div className="nav-empty">{t("collections.loadFailed")}</div>}
        {data && items.length === 0 && <div className="nav-empty">{t(needle ? "collections.noMatch" : "collections.empty")}</div>}
      </div>
    </NavGroup>
  );
}
