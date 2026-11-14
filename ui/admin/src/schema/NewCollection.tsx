import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { RiEyeLine, RiTableLine, RiUser3Line } from "@remixicon/react";
import { getCollectionScaffolds, type CollectionType } from "../api/collections";
import { collectionsQuery } from "../collections/queries";
import { QueryView } from "../components/QueryView";
import { useT } from "../i18n";
import { SchemaView } from "./SchemaView";
import "../styles/schema.css";

const choices: { type: CollectionType; icon: typeof RiTableLine }[] = [
  { type: "base", icon: RiTableLine },
  { type: "auth", icon: RiUser3Line },
  { type: "view", icon: RiEyeLine },
];

export const isCollectionType = (value: string | undefined): value is CollectionType => value === "base" || value === "auth" || value === "view";

/** Step one of a new collection: pick its kind. */
export function NewCollectionChooser() {
  const t = useT();
  return (
    <div className="page-content">
      <ul className="schema-choices">
        {choices.map(({ type, icon: Icon }) => (
          <li key={type}>
            <Link className="schema-choice" to={`/new-collection/${type}`}>
              <Icon size={20} aria-hidden />
              <strong>{t(`collections.type.${type}`)}</strong>
              <span>{t(`schema.choice.${type}`)}</span>
            </Link>
          </li>
        ))}
      </ul>
    </div>
  );
}

/** Step two: the Schema editor in its "new" state, seeded from the server scaffolds. */
export function NewCollectionEditor({ type }: { type: CollectionType }) {
  const catalog = useQuery(collectionsQuery);
  const scaffolds = useQuery({ queryKey: ["collection-scaffolds"], queryFn: ({ signal }) => getCollectionScaffolds(signal), staleTime: Infinity });
  return (
    <div className="page-form">
      <QueryView query={catalog} skeleton="form">
        {(collections) => (
          <QueryView query={scaffolds} skeleton="form">
            {(seeds) => <SchemaView key={type} type={type} catalog={collections} scaffolds={seeds} basePath={`/new-collection/${type}`} />}
          </QueryView>
        )}
      </QueryView>
    </div>
  );
}
