import { Link } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { RiErrorWarningLine, RiEyeLine, RiTableLine, RiUser3Line } from "@remixicon/react";
import { getCollectionScaffolds, type CollectionType } from "../api/collections";
import { errorText } from "../app/queries";
import { collectionsQuery } from "../collections/queries";
import { EmptyState, Loader } from "../components/Feedback";
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
  const t = useT();
  const catalog = useQuery(collectionsQuery);
  const scaffolds = useQuery({ queryKey: ["collection-scaffolds"], queryFn: ({ signal }) => getCollectionScaffolds(signal), staleTime: Infinity });
  const error = catalog.error ?? scaffolds.error;
  if (error) return <EmptyState icon={RiErrorWarningLine}>{errorText(t, error)}</EmptyState>;
  if (!catalog.data || !scaffolds.data) return <Loader />;
  return <SchemaView key={type} type={type} catalog={catalog.data} scaffolds={scaffolds.data} basePath={`/new-collection/${type}`} />;
}
